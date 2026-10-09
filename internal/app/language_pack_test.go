package app

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ash/internal/localize"
)

func TestInstallPreferredLanguageFallsBackFromRegionalPack(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		locale := strings.TrimPrefix(r.URL.Path, "/")
		if locale == "es_MX" {
			http.NotFound(w, r)
			return
		}
		if locale != "es_ES" {
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(filepath.Join("..", "localize", "languages", locale+".json"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()

	oldURL := languagePackDownloadURL
	languagePackDownloadURL = func(version, locale string) string {
		if version != "v1.2.3" {
			t.Errorf("unexpected release version %q", version)
		}
		return fmt.Sprintf("%s/%s", server.URL, locale)
	}
	t.Cleanup(func() { languagePackDownloadURL = oldURL })

	oldVersion := ashVersion
	ashVersion = "v1.2.3"
	t.Cleanup(func() { ashVersion = oldVersion })

	home := t.TempDir()
	oldHome := osUserHomeDir
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { osUserHomeDir = oldHome })

	t.Setenv("LANG", "es_MX.UTF-8")
	if err := localize.Init("en_US", ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := localize.Init("en_US", ""); err != nil {
			t.Errorf("restore English translator: %v", err)
		}
	})

	var output bytes.Buffer
	if err := installPreferredLanguage(&output); err != nil {
		t.Fatal(err)
	}
	languageDir := filepath.Join(home, ".ash", "languages")
	if err := localize.ValidateLocale("es_ES", languageDir); err != nil {
		t.Fatalf("installed fallback catalog is invalid: %v", err)
	}
	if got := localize.Format("install.language_downloaded", []any{"es_ES"}); !strings.Contains(got, "Paquete de idioma instalado") {
		t.Fatalf("active language was not updated after download: %q", got)
	}
	if got := output.String(); !strings.Contains(got, "Paquete de idioma instalado: es_ES") {
		t.Fatalf("unexpected install output: %q", got)
	}
}

func TestFetchLanguageCatalogChainIncludesParent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		locale := strings.TrimPrefix(r.URL.Path, "/")
		if locale != "zh_TW" && locale != "zh_CN" {
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(filepath.Join("..", "localize", "languages", locale+".json"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()

	oldURL := languagePackDownloadURL
	languagePackDownloadURL = func(_, locale string) string {
		return fmt.Sprintf("%s/%s", server.URL, locale)
	}
	t.Cleanup(func() { languagePackDownloadURL = oldURL })

	dir := t.TempDir()
	if err := fetchLanguageCatalogChain("zh_TW", dir, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"zh_CN", "zh_TW"} {
		if _, err := os.Stat(filepath.Join(dir, locale+".json")); err != nil {
			t.Fatalf("parent catalog %s not downloaded: %v", locale, err)
		}
	}
	if err := localize.ValidateLocale("zh_TW", dir); err != nil {
		t.Fatalf("downloaded parent chain is invalid: %v", err)
	}
}

func TestFetchLanguageCatalogChainRejectsUnsafeParent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"locale":"de_DE","parent":"../outside","messages":{"key":"value"}}`))
	}))
	defer server.Close()

	oldURL := languagePackDownloadURL
	languagePackDownloadURL = func(_, locale string) string {
		return fmt.Sprintf("%s/%s", server.URL, locale)
	}
	t.Cleanup(func() { languagePackDownloadURL = oldURL })

	dir := t.TempDir()
	if err := fetchLanguageCatalogChain("de_DE", dir, map[string]bool{}); err == nil {
		t.Fatal("expected unsafe catalog parent to be rejected")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "outside.json")); !os.IsNotExist(err) {
		t.Fatalf("unsafe parent wrote outside the staging directory: %v", err)
	}
}
