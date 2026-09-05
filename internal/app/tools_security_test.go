package app

import (
	"context"
	"strings"
	"testing"
)

func TestCallUnixCommandAdversarialArguments(t *testing.T) {
	shim := localToolShim{
		allowlist: map[string]struct{}{
			"ls":   {},
			"echo": {},
			"cat":  {},
		},
		agents: newAgentBudget(6),
	}

	adversarialArgs := []struct {
		name string
		args map[string]any
		want string
	}{
		{
			name: "blocked semicolon chain",
			args: map[string]any{
				"command": "ls",
				"args":    []any{"; rm -rf /"},
			},
			want: "argument contains blocked shell control pattern",
		},
		{
			name: "blocked pipe character",
			args: map[string]any{
				"command": "ls",
				"args":    []any{"| cat"},
			},
			want: "argument contains blocked shell control pattern",
		},
		{
			name: "blocked subshell execution",
			args: map[string]any{
				"command": "echo",
				"args":    []any{"$(whoami)"},
			},
			want: "argument contains blocked shell control pattern",
		},
		{
			name: "blocked backticks execution",
			args: map[string]any{
				"command": "echo",
				"args":    []any{"`whoami`"},
			},
			want: "argument contains blocked shell control pattern",
		},
		{
			name: "blocked output redirection",
			args: map[string]any{
				"command": "ls",
				"args":    []any{"> /tmp/pwned"},
			},
			want: "argument contains blocked shell control pattern",
		},
		{
			name: "blocked background operator",
			args: map[string]any{
				"command": "echo",
				"args":    []any{"foo && bar"},
			},
			want: "argument contains blocked shell control pattern",
		},
		{
			name: "blocked newline character",
			args: map[string]any{
				"command": "echo",
				"args":    []any{"foo\nbar"},
			},
			want: "argument contains blocked shell control pattern",
		},
		{
			name: "blocked null byte",
			args: map[string]any{
				"command": "echo",
				"args":    []any{"foo\x00bar"},
			},
			want: "argument contains blocked shell control pattern",
		},
		{
			name: "blocked hidden dotfile reference",
			args: map[string]any{
				"command": "ls",
				"args":    []any{".ssh_id_rsa"},
			},
			want: "argument references a hidden dotfile",
		},
		{
			name: "blocked dot env file",
			args: map[string]any{
				"command": "ls",
				"args":    []any{".env"},
			},
			want: "argument references a hidden dotfile",
		},
		{
			name: "non-allowlisted command",
			args: map[string]any{
				"command": "curl",
				"args":    []any{"https://example.com"},
			},
			want: "command is not allowlisted",
		},
		{
			name: "malformed command non-string",
			args: map[string]any{
				"command": 12345,
			},
			want: "command must be a string",
		},
		{
			name: "malformed args non-slice",
			args: map[string]any{
				"command": "ls",
				"args":    12345,
			},
			want: "must be an array",
		},
	}

	for _, tt := range adversarialArgs {
		t.Run(tt.name, func(t *testing.T) {
			res := shim.callUnixCommand(context.Background(), tt.args)
			if res.OK {
				t.Fatalf("expected command failure for %s, got OK=true", tt.name)
			}
			if !strings.Contains(res.Error, tt.want) {
				t.Errorf("error mismatch: got %q, want substring %q", res.Error, tt.want)
			}
		})
	}
}

func TestCallUnixPipelineAdversarialInputs(t *testing.T) {
	shim := localToolShim{
		allowlist: map[string]struct{}{
			"ls":   {},
			"grep": {},
			"wc":   {},
		},
		agents: newAgentBudget(6),
	}

	tests := []struct {
		name     string
		pipeline string
		want     string
	}{
		{
			name:     "pipeline with non-allowlisted command",
			pipeline: "ls | rm -rf /",
			want:     "command is not allowlisted",
		},
		{
			name:     "pipeline with blocked argument injection",
			pipeline: "ls | grep '$(whoami)'",
			want:     "argument contains blocked shell control pattern",
		},
		{
			name:     "pipeline with dotfile access",
			pipeline: "cat .env | grep secret",
			want:     "command is not allowlisted", // cat not in allowlist
		},
		{
			name:     "single command pipeline (under min stages)",
			pipeline: "ls",
			want:     "must contain between 2 and 16 commands",
		},
		{
			name:     "empty pipeline",
			pipeline: "",
			want:     "must contain between 2 and 16 commands",
		},
		{
			name:     "oversized pipeline (>16 commands)",
			pipeline: strings.Repeat("ls | ", 17) + "wc",
			want:     "must contain between 2 and 16 commands",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := shim.callUnixPipeline(context.Background(), map[string]any{"pipeline": tt.pipeline})
			if res.OK {
				t.Fatalf("expected pipeline failure for %s", tt.name)
			}
			if !strings.Contains(res.Error, tt.want) {
				t.Errorf("error mismatch: got %q, want substring %q", res.Error, tt.want)
			}
		})
	}
}

func TestWorkspaceAndScratchFileAdversarialPathTraversal(t *testing.T) {
	shim := localToolShim{
		allowlist: map[string]struct{}{},
		agents:    newAgentBudget(6),
	}

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("SESSION_ID", "test_session_123")

	traversalPaths := []string{
		"../../../../etc/passwd",
		"/etc/passwd",
		"nested/../../../../var/log/syslog",
		".ssh_id_rsa",
		"dir/.hidden",
		"\x00nullbyte_traversal",
	}

	for _, p := range traversalPaths {
		t.Run("read_workspace_"+p, func(t *testing.T) {
			res := shim.callReadWorkspaceFile(map[string]any{"path": p})
			if res.OK {
				t.Errorf("expected read workspace failure for traversal path %q", p)
			}
		})

		t.Run("write_workspace_"+p, func(t *testing.T) {
			res := shim.callWriteWorkspaceFile(map[string]any{
				"path":    p,
				"content": "injected payload",
				"purpose": "test",
			})
			if res.OK {
				t.Errorf("expected write workspace failure for traversal path %q", p)
			}
		})

		t.Run("read_scratch_"+p, func(t *testing.T) {
			res := shim.callReadScratchFile(map[string]any{"path": p})
			if res.OK {
				t.Errorf("expected read scratch failure for traversal path %q", p)
			}
		})

		t.Run("write_scratch_"+p, func(t *testing.T) {
			res := shim.callWriteScratchFile(context.Background(), map[string]any{
				"path":    p,
				"content": "injected payload",
			})
			if res.OK {
				t.Errorf("expected write scratch failure for traversal path %q", p)
			}
		})
	}
}

func TestSubAgentBudgetAndRecursionDefense(t *testing.T) {
	budget := newAgentBudget(2)
	shim := localToolShim{
		allowlist: map[string]struct{}{},
		agents:    budget,
	}

	t.Run("budget limits parallel children", func(t *testing.T) {
		if !budget.reserve() {
			t.Fatal("expected first reservation to succeed")
		}
		if !budget.reserve() {
			t.Fatal("expected second reservation to succeed")
		}
		if budget.reserve() {
			t.Fatal("expected third reservation to fail when cap is 2")
		}
		budget.release()
		if !budget.reserve() {
			t.Fatal("expected reservation to succeed after release")
		}
	})

	t.Run("child agent cannot spawn further agents", func(t *testing.T) {
		t.Setenv("ASH_CHILD_AGENT", "1")
		res := shim.callSubAgent(context.Background(), map[string]any{
			"prompt": "do something recursive",
		})
		if res.OK {
			t.Fatal("child agent should be blocked from spawning sub-agents")
		}
		if !strings.Contains(res.Error, "child agents cannot") {
			t.Errorf("expected recursion block error, got %q", res.Error)
		}
	})
}
