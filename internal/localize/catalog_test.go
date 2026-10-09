package localize

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveLocale(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "en_US.UTF-8", want: "en_US"},
		{input: "en_GB.UTF-8", want: "en_US"},
		{input: "es_MX.UTF-8", want: "es_ES"},
		{input: "zh_TW.UTF-8", want: "zh_TW"},
		{input: "zh-Hant-HK", want: "zh_TW"},
		{input: "zh_CN.UTF-8", want: "zh_CN"},
		{input: "id_ID", want: "id_ID"},
		{input: "fr_FR.UTF-8", want: "en_US"},
		{input: "C", want: "en_US"},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			if got := ResolveLocale(test.input); got != test.want {
				t.Fatalf("ResolveLocale(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestValidateCatalogLocaleRejectsUnsafePathComponents(t *testing.T) {
	for _, locale := range []string{"../outside", "zh/../../outside", "en-US", "en_US.json"} {
		if err := ValidateCatalogLocale(locale); err == nil {
			t.Errorf("ValidateCatalogLocale(%q) unexpectedly succeeded", locale)
		}
	}
	if err := ValidateCatalogLocale("zh_Hant"); err != nil {
		t.Fatalf("valid script locale rejected: %v", err)
	}
}

func TestLoadFormatsFromParentOverride(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "es_ES.json"), []byte(`{
		"locale": "es_ES",
		"messages": {"greeting": "Hola %s", "farewell": "Adiós"}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "es_MX.json"), []byte(`{
		"locale": "es_MX",
		"parent": "es_ES",
		"messages": {"greeting": "Qué onda %s"}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	translator, err := Load("es_MX.UTF-8", dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := translator.printer.Sprintf("greeting", "Alex"); got != "Qué onda Alex" {
		t.Fatalf("override = %q", got)
	}
	if got := translator.printer.Sprintf("farewell"); got != "Adiós" {
		t.Fatalf("parent = %q", got)
	}
}

func TestValidateDirReportsMissingKeysByLocale(t *testing.T) {
	dir := t.TempDir()
	entries, err := os.ReadDir("languages")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, readErr := os.ReadFile(filepath.Join("languages", entry.Name()))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if entry.Name() == "es_ES.json" {
			parsed, parseErr := parseCatalog(data, entry.Name())
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			delete(parsed.Messages, "install.already_present")
			data, err = json.Marshal(parsed)
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, entry.Name()), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	err = ValidateDir(dir)
	if err == nil || !strings.Contains(err.Error(), "language catalog es_ES missing keys: install.already_present") {
		t.Fatalf("ValidateDir error = %v", err)
	}
}

func TestValidateBundledCatalogs(t *testing.T) {
	if err := ValidateDir("languages"); err != nil {
		t.Fatal(err)
	}
}
