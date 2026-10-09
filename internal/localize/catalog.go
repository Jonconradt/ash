package localize

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/text/message/catalog"
)

//go:embed languages/en_US.json
var embeddedLanguages embed.FS

const defaultLocale = "en_US"

var formatVerbPattern = regexp.MustCompile(`%(\[[1-9][0-9]*\])?[-+# 0-9.]*[a-zA-Z]`)
var catalogLocalePattern = regexp.MustCompile(`^[A-Za-z]{2,3}_[A-Za-z]{2,4}$`)

type fileCatalog struct {
	Locale   string            `json:"locale"`
	Parent   string            `json:"parent,omitempty"`
	Messages map[string]string `json:"messages"`
}

type Translator struct {
	locale   string
	printer  *message.Printer
	messages map[string]string
}

var (
	activeMu sync.RWMutex
	active   *Translator
)

func init() {
	var err error
	active, err = Load(defaultLocale, "")
	if err != nil {
		panic(fmt.Sprintf("load embedded English language catalog: %v", err))
	}
}

// EmbeddedEnglishCatalog returns the bundled English catalog for installation
// into the user's language directory.
func EmbeddedEnglishCatalog() ([]byte, error) {
	return embeddedLanguages.ReadFile("languages/en_US.json")
}

// ResolveLocale maps POSIX LANG names and BCP 47 tags to the closest supported
// catalog. A supported language wins over a region mismatch.
func ResolveLocale(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "C" || raw == "POSIX" {
		return defaultLocale
	}
	if cut := strings.IndexAny(raw, ".@"); cut >= 0 {
		raw = raw[:cut]
	}
	tag, err := language.Parse(strings.ReplaceAll(raw, "_", "-"))
	if err != nil {
		return defaultLocale
	}
	if locale := exactLocale(tag); locale != "" {
		return locale
	}

	base, _ := tag.Base()
	script, _ := tag.Script()
	region, _ := tag.Region()
	switch base.String() {
	case "en":
		return "en_US"
	case "es":
		return "es_ES"
	case "ar":
		return "ar_AE"
	case "id":
		return "id_ID"
	case "zh":
		if script.String() == "Hant" || region.String() == "TW" || region.String() == "HK" || region.String() == "MO" {
			return "zh_TW"
		}
		return "zh_CN"
	default:
		return defaultLocale
	}
}

func exactLocale(tag language.Tag) string {
	switch tag.String() {
	case "en-US":
		return "en_US"
	case "zh-CN":
		return "zh_CN"
	case "zh-TW":
		return "zh_TW"
	case "es-ES":
		return "es_ES"
	case "ar-AE":
		return "ar_AE"
	case "id-ID":
		return "id_ID"
	default:
		return ""
	}
}

func requestedLocale(raw string) string {
	raw = strings.TrimSpace(raw)
	if cut := strings.IndexAny(raw, ".@"); cut >= 0 {
		raw = raw[:cut]
	}
	tag, err := language.Parse(strings.ReplaceAll(raw, "_", "-"))
	if err != nil {
		return ""
	}
	base, _ := tag.Base()
	region, confidence := tag.Region()
	if confidence == language.Exact && region.String() != "" {
		return base.String() + "_" + region.String()
	}
	script, confidence := tag.Script()
	if confidence == language.Exact && script.String() != "" {
		return base.String() + "_" + script.String()
	}
	return ""
}

// ExactLocale returns the locale tag explicitly requested by LANG when it has
// a region or script, without applying the supported-locale fallback.
func ExactLocale(raw string) string {
	return requestedLocale(raw)
}

// ValidateCatalogLocale rejects locale identifiers that are unsafe as catalog
// filenames or release asset path components.
func ValidateCatalogLocale(locale string) error {
	if !catalogLocalePattern.MatchString(locale) {
		return fmt.Errorf("invalid language catalog locale %q", locale)
	}
	return nil
}

// Load builds a translator from the embedded English catalog and optional
// locale catalog files stored in dir. Files are merged parent-first.
func Load(rawLocale, dir string) (*Translator, error) {
	englishBytes, err := embeddedLanguages.ReadFile("languages/en_US.json")
	if err != nil {
		return nil, fmt.Errorf("read embedded English catalog: %w", err)
	}
	english, err := parseCatalog(englishBytes, "embedded en_US")
	if err != nil {
		return nil, err
	}
	files := map[string]fileCatalog{defaultLocale: english}
	locale := ResolveLocale(rawLocale)
	if requested := requestedLocale(rawLocale); requested != "" {
		if _, err := os.Stat(filepath.Join(dir, requested+".json")); err == nil {
			locale = requested
		}
	}
	if err := loadCatalogChain(locale, dir, files, map[string]bool{}, false); err != nil {
		return nil, err
	}
	if _, exists := files[locale]; !exists {
		locale = defaultLocale
	}
	messages := make(map[string]string, len(english.Messages))
	if err := mergeCatalog(locale, files, messages, map[string]bool{}); err != nil {
		return nil, err
	}
	for key, value := range english.Messages {
		if _, ok := messages[key]; !ok {
			messages[key] = value
		}
	}
	builder := catalog.NewBuilder()
	for key, value := range messages {
		if err := builder.SetString(language.Make(strings.ReplaceAll(locale, "_", "-")), key, value); err != nil {
			return nil, fmt.Errorf("register translation key %q for %s: %w", key, locale, err)
		}
	}
	return &Translator{
		locale:   locale,
		printer:  message.NewPrinter(language.Make(strings.ReplaceAll(locale, "_", "-")), message.Catalog(builder)),
		messages: messages,
	}, nil
}

func loadCatalogChain(locale, dir string, files map[string]fileCatalog, visiting map[string]bool, require bool) error {
	if err := ValidateCatalogLocale(locale); err != nil {
		return err
	}
	if locale == defaultLocale {
		return nil
	}
	if visiting[locale] {
		return fmt.Errorf("language catalog parent cycle at %s", locale)
	}
	path := filepath.Join(dir, locale+".json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if require {
			return fmt.Errorf("language catalog %s.json is missing", locale)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read language catalog %s: %w", path, err)
	}
	entry, err := parseCatalog(data, path)
	if err != nil {
		return err
	}
	if entry.Locale != locale {
		return fmt.Errorf("language catalog %s declares locale %s; expected %s", path, entry.Locale, locale)
	}
	visiting[locale] = true
	if entry.Parent != "" {
		if err := loadCatalogChain(entry.Parent, dir, files, visiting, require); err != nil {
			return err
		}
	}
	delete(visiting, locale)
	files[locale] = entry
	return nil
}

// ValidateLocale validates the effective catalog for locale without allowing
// untranslated keys to fall back to English.
func ValidateLocale(locale, dir string) error {
	englishBytes, err := embeddedLanguages.ReadFile("languages/en_US.json")
	if err != nil {
		return fmt.Errorf("read embedded English catalog: %w", err)
	}
	english, err := parseCatalog(englishBytes, "embedded en_US")
	if err != nil {
		return err
	}
	files := map[string]fileCatalog{defaultLocale: english}
	if err := loadCatalogChain(locale, dir, files, map[string]bool{}, true); err != nil {
		return err
	}
	merged := make(map[string]string, len(english.Messages))
	if err := mergeCatalog(locale, files, merged, map[string]bool{}); err != nil {
		return err
	}
	var missing []string
	for key := range english.Messages {
		if _, ok := merged[key]; !ok {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("language catalog %s missing keys: %s", locale, strings.Join(missing, ", "))
	}
	return nil
}

func parseCatalog(data []byte, source string) (fileCatalog, error) {
	var result fileCatalog
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return fileCatalog{}, fmt.Errorf("parse language catalog %s: %w", source, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fileCatalog{}, fmt.Errorf("parse language catalog %s: expected a single JSON object", source)
	}
	if result.Locale == "" {
		return fileCatalog{}, fmt.Errorf("parse language catalog %s: locale is required", source)
	}
	if err := ValidateCatalogLocale(result.Locale); err != nil {
		return fileCatalog{}, fmt.Errorf("parse language catalog %s: %w", source, err)
	}
	if result.Parent != "" {
		if err := ValidateCatalogLocale(result.Parent); err != nil {
			return fileCatalog{}, fmt.Errorf("parse language catalog %s: invalid parent: %w", source, err)
		}
	}
	if result.Messages == nil {
		return fileCatalog{}, fmt.Errorf("parse language catalog %s: messages must be an object", source)
	}
	for key, value := range result.Messages {
		if strings.TrimSpace(key) == "" || value == "" {
			return fileCatalog{}, fmt.Errorf("parse language catalog %s: empty key or message", source)
		}
	}
	return result, nil
}

// ValidateDir parses the catalogs in dir and ensures every catalog resolves
// all keys from the English base through its declared parent chain.
func ValidateDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read language catalog directory %s: %w", dir, err)
	}
	englishBytes, err := embeddedLanguages.ReadFile("languages/en_US.json")
	if err != nil {
		return fmt.Errorf("read embedded English catalog: %w", err)
	}
	english, err := parseCatalog(englishBytes, "embedded en_US")
	if err != nil {
		return err
	}
	files := make(map[string]fileCatalog)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read language catalog %s: %w", path, readErr)
		}
		parsed, parseErr := parseCatalog(data, path)
		if parseErr != nil {
			return parseErr
		}
		if entry.Name() != parsed.Locale+".json" {
			return fmt.Errorf("language catalog %s declares locale %s; filename must be %s.json", path, parsed.Locale, parsed.Locale)
		}
		if _, duplicate := files[parsed.Locale]; duplicate {
			return fmt.Errorf("duplicate language catalog for locale %s", parsed.Locale)
		}
		files[parsed.Locale] = parsed
	}
	if _, ok := files[defaultLocale]; !ok {
		return fmt.Errorf("language catalog %s.json is missing", defaultLocale)
	}
	for _, locale := range []string{"zh_CN", "zh_TW", "es_ES", "ar_AE", "id_ID"} {
		if _, ok := files[locale]; !ok {
			return fmt.Errorf("language catalog %s.json is missing", locale)
		}
	}
	for locale, item := range files {
		if item.Parent != "" {
			if _, exists := files[item.Parent]; !exists {
				return fmt.Errorf("language catalog %s declares missing parent %s", locale, item.Parent)
			}
		}
		merged := make(map[string]string, len(english.Messages))
		if err := mergeCatalog(locale, files, merged, map[string]bool{}); err != nil {
			return err
		}
		var missing []string
		for key := range english.Messages {
			if _, ok := merged[key]; !ok {
				missing = append(missing, key)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			return fmt.Errorf("language catalog %s missing keys: %s", locale, strings.Join(missing, ", "))
		}
		for key, source := range english.Messages {
			if got := strings.Join(formatVerbPattern.FindAllString(merged[key], -1), ","); got != strings.Join(formatVerbPattern.FindAllString(source, -1), ",") {
				return fmt.Errorf("language catalog %s key %s has mismatched formatting placeholders: got %q, want %q", locale, key, got, strings.Join(formatVerbPattern.FindAllString(source, -1), ","))
			}
		}
	}
	return nil
}

func mergeCatalog(locale string, files map[string]fileCatalog, merged map[string]string, visited map[string]bool) error {
	if visited[locale] {
		return fmt.Errorf("language catalog parent cycle at %s", locale)
	}
	visited[locale] = true
	entry, exists := files[locale]
	if !exists {
		return nil
	}
	if entry.Parent != "" {
		if err := mergeCatalog(entry.Parent, files, merged, visited); err != nil {
			return err
		}
	}
	for key, value := range entry.Messages {
		merged[key] = value
	}
	delete(visited, locale)
	return nil
}

// Init loads and installs the process-wide translator.
func Init(rawLocale, dir string) error {
	translator, err := Load(rawLocale, dir)
	if err != nil {
		return err
	}
	activeMu.Lock()
	active = translator
	activeMu.Unlock()
	return nil
}

// Text returns a translated message without formatting arguments.
func Text(key string) string {
	return Format(key, nil)
}

// Format returns the translation for key, formatting args with Go's fmt verbs.
// Unknown keys are returned unchanged so English remains usable.
func Format(key string, args []any) string {
	activeMu.RLock()
	translator := active
	activeMu.RUnlock()
	if translator == nil {
		return fmt.Sprintf(key, args...)
	}
	if _, ok := translator.messages[key]; !ok {
		return fmt.Sprintf(key, args...)
	}
	return translator.printer.Sprintf(key, args...)
}

// Locale returns the active locale or the default locale before initialization.
func Locale() string {
	activeMu.RLock()
	defer activeMu.RUnlock()
	if active == nil {
		return defaultLocale
	}
	return active.locale
}
