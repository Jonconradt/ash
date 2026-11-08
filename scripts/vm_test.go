package scripts

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTartAIEndpointDNS(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		answer   string
		wantHost string
		wantErr  bool
	}{
		{
			name:     "bonjour",
			endpoint: "http://model.local:11434/v1",
			answer:   "ip_address: 192.0.2.3\nip_address: 192.0.2.4\n",
			wantHost: "192.0.2.3 model.local model.local.\n",
		},
		{
			name:     "normalized hostname",
			endpoint: "http://MODEL.local.:11434/v1",
			answer:   "ipv6_address: 2001:db8::1\nip_address: 192.0.2.3\n",
			wantHost: "192.0.2.3 model.local model.local.\n",
		},
		{name: "cloud", endpoint: "https://api.example.com/v1"},
		{name: "literal address", endpoint: "http://192.0.2.3:11434"},
		{name: "unconfigured"},
		{name: "unresolved", endpoint: "http://missing.local:11434", wantErr: true},
		{
			name:     "ipv6 only",
			endpoint: "http://model.local:11434",
			answer:   "ipv6_address: 2001:db8::1\n",
			wantErr:  true,
		},
		{name: "invalid hostname", endpoint: "http://bad name.local:11434", wantErr: true},
	}
	script, err := filepath.Abs("vm.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeMock := func(name, body string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/bash\nset -euo pipefail\n"+body), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			writeMock("uname", `if [[ "${1:-}" == "-m" ]]; then echo arm64; else echo Darwin; fi`)
			writeMock("dscacheutil", `printf '%s' "$VM_TEST_DNS"`)
			writeMock("sudo", `
				[[ "$1" == tee && "$2" == -a && "$3" == /etc/hosts ]]
				cat >>"$VM_TEST_HOSTS"
			`)
			writeMock("tart", `
				case "$1" in
					clone|run|stop|delete) exit 0 ;;
					exec)
						shift
						if [[ "$1" == -i ]]; then cat >/dev/null; exit 0; fi
						shift
						if [[ "${4:-}" == ash-vm-dns ]]; then
							"$@"
						else
							printf 'setup\n' >>"$VM_TEST_SETUP"
						fi
						;;
					*) exit 1 ;;
				esac
			`)
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", script, "--platform", "ubuntu")
			for _, variable := range os.Environ() {
				name, _, _ := strings.Cut(variable, "=")
				if strings.HasPrefix(name, "ASH_AI") || strings.HasPrefix(name, "ASH_TEST_") ||
					name == "BASH_ENV" || name == "ENV" || name == "PATH" {
					continue
				}
				cmd.Env = append(cmd.Env, variable)
			}
			hosts := filepath.Join(dir, "hosts")
			cmd.Env = append(cmd.Env,
				"PATH="+dir+":"+os.Getenv("PATH"),
				"ASH_AI_ENDPOINT="+tt.endpoint,
				"VM_TEST_DNS="+tt.answer,
				"VM_TEST_HOSTS="+hosts,
				"VM_TEST_SETUP="+filepath.Join(dir, "setup"),
			)
			output, runErr := cmd.CombinedOutput()
			if (runErr != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr = %v\n%s", runErr, tt.wantErr, output)
			}
			content, readErr := os.ReadFile(hosts)
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
			if string(content) != tt.wantHost {
				t.Fatalf("hosts = %q, want %q\n%s", content, tt.wantHost, output)
			}
		})
	}
}
