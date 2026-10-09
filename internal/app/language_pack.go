package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"ash/internal/localize"
)

const maxLanguagePackBytes = 1 << 20

var languagePackDownloadURL = func(version, locale string) string {
	return fmt.Sprintf("https://github.com/Jonconradt/ash/releases/download/%s/ash-language-%s.json", version, locale)
}

var errLanguagePackUnavailable = errors.New("language pack is not published")

type languagePackMetadata struct {
	Locale string `json:"locale"`
	Parent string `json:"parent"`
}

func installPreferredLanguage(stdout io.Writer) error {
	requested := localize.EnvironmentLocale()
	baseLocale := localize.ResolveLocale(requested)
	candidates := make([]string, 0, 2)
	if exact := localize.ExactLocale(requested); exact != "" && exact != "en_US" {
		candidates = append(candidates, exact)
	}
	if baseLocale != "en_US" && (len(candidates) == 0 || candidates[0] != baseLocale) {
		candidates = append(candidates, baseLocale)
	}
	if len(candidates) == 0 {
		return nil
	}
	root, err := ashWorkspaceDir()
	if err != nil {
		return err
	}
	languageDir := filepath.Join(root, "languages")
	if err := osMkdirAll(languageDir, 0o700); err != nil {
		return fmt.Errorf("create language catalog directory: %w", err)
	}
	var downloadErr error
	for _, locale := range candidates {
		if err := localize.ValidateLocale(locale, languageDir); err == nil {
			return localize.Init(requested, languageDir)
		}
		if ashVersion == "" || ashVersion == "dev" {
			downloadErr = errLanguagePackUnavailable
			continue
		}
		stagingDir, err := os.MkdirTemp(languageDir, ".language-pack-")
		if err != nil {
			return fmt.Errorf("create language pack staging directory: %w", err)
		}
		err = fetchLanguageCatalogChain(locale, stagingDir, map[string]bool{})
		if err == nil {
			err = localize.ValidateLocale(locale, stagingDir)
			if err != nil {
				_, _ = fmt.Fprintln(stdout, localize.Format("install.language_invalid", []any{locale, err}))
				_ = os.RemoveAll(stagingDir)
				return nil
			}
			if err = commitLanguageCatalogs(stagingDir, languageDir); err == nil {
				err = localize.Init(requested, languageDir)
			}
			_ = os.RemoveAll(stagingDir)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(stdout, localize.Format("install.language_downloaded", []any{locale}))
			return nil
		}
		_ = os.RemoveAll(stagingDir)
		if !errors.Is(err, errLanguagePackUnavailable) {
			downloadErr = err
		}
	}
	if downloadErr != nil && !errors.Is(downloadErr, errLanguagePackUnavailable) {
		_, _ = fmt.Fprintln(stdout, localize.Format("install.language_download_failed", []any{baseLocale, downloadErr}))
		return nil
	}
	if baseLocale != "en_US" {
		_, _ = fmt.Fprintln(stdout, localize.Format("install.language_unavailable", []any{baseLocale, ashVersion}))
	}
	return nil
}

func commitLanguageCatalogs(stagingDir, languageDir string) error {
	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		return fmt.Errorf("read staged language catalogs: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		source := filepath.Join(stagingDir, entry.Name())
		target := filepath.Join(languageDir, entry.Name())
		if err := os.Rename(source, target); err != nil {
			return fmt.Errorf("install language catalog %s: %w", target, err)
		}
	}
	return nil
}

func fetchLanguageCatalogChain(locale, dir string, visiting map[string]bool) (resultErr error) {
	if err := localize.ValidateCatalogLocale(locale); err != nil {
		return err
	}
	if locale == "en_US" {
		return nil
	}
	if visiting[locale] {
		return fmt.Errorf("language catalog parent cycle at %s", locale)
	}
	visiting[locale] = true
	defer delete(visiting, locale)

	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open language catalog directory %s: %w", dir, err)
	}
	defer func() {
		if closeErr := root.Close(); resultErr == nil && closeErr != nil {
			resultErr = fmt.Errorf("close language catalog directory %s: %w", dir, closeErr)
		}
	}()
	path := filepath.Join(dir, locale+".json")
	var data []byte
	file, err := root.Open(locale + ".json")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read existing language catalog %s: %w", path, err)
	}
	if err == nil {
		var readErr error
		data, readErr = io.ReadAll(file)
		closeErr := file.Close()
		if readErr != nil {
			return fmt.Errorf("read existing language catalog %s: %w", path, readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close existing language catalog %s: %w", path, closeErr)
		}
	}
	if errors.Is(err, os.ErrNotExist) {
		data, err = downloadLanguageCatalog(locale)
		if err != nil {
			return err
		}
	}
	var metadata languagePackMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return fmt.Errorf("parse language catalog %s: %w", locale, err)
	}
	if metadata.Locale != locale {
		return fmt.Errorf("language catalog asset for %s declares locale %q", locale, metadata.Locale)
	}
	if metadata.Parent != "" {
		if err := fetchLanguageCatalogChain(metadata.Parent, dir, visiting); err != nil {
			return err
		}
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := writeLanguageCatalog(path, data); err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("check language catalog %s: %w", path, err)
	}
	return nil
}

func downloadLanguageCatalog(locale string) ([]byte, error) {
	if err := localize.ValidateCatalogLocale(locale); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, languagePackDownloadURL(ashVersion, locale), nil)
	if err != nil {
		return nil, fmt.Errorf("create language pack request: %w", err)
	}
	request.Header.Set("User-Agent", "ash/"+ashVersion)
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("request language pack for %s: %w", locale, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound {
		return nil, errLanguagePackUnavailable
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("language pack request for %s returned HTTP %s", locale, response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxLanguagePackBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read language pack for %s: %w", locale, err)
	}
	if len(body) > maxLanguagePackBytes {
		return nil, fmt.Errorf("language pack for %s exceeds %d bytes", locale, maxLanguagePackBytes)
	}
	return body, nil
}

func writeLanguageCatalog(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".language-*.json")
	if err != nil {
		return fmt.Errorf("create language catalog temporary file: %w", err)
	}
	tempPath := temporary.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set language catalog permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write language catalog: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close language catalog: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("install language catalog: %w", err)
	}
	return nil
}
