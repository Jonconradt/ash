package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"ash/internal/localize"
)

func initLanguage(stderr io.Writer) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		if initErr := localize.Init("en_US", ""); initErr != nil {
			_, _ = fmt.Fprintf(stderr, "language initialization failed: %v\n", initErr)
			return false
		}
		_, _ = fmt.Fprintf(stderr, "language packs unavailable: %v; using en_US\n", err)
		return true
	}
	if err := localize.Init(localize.EnvironmentLocale(), filepath.Join(home, ".ash", "languages")); err != nil {
		_, _ = fmt.Fprintf(stderr, "language catalog error: %v\n", err)
		if fallbackErr := localize.Init("en_US", ""); fallbackErr != nil {
			_, _ = fmt.Fprintf(stderr, "English language catalog error: %v\n", fallbackErr)
			return false
		}
		_, _ = fmt.Fprintln(stderr, "using en_US; run 'ash install' to repair or install language catalogs")
	}
	return true
}

func responseLanguageName() string {
	locale := localize.Locale()
	switch locale {
	case "zh_CN":
		return "Simplified Chinese"
	case "zh_TW":
		return "Traditional Chinese"
	case "es_ES":
		return "Spanish"
	case "ar_AE":
		return "Arabic"
	case "id_ID":
		return "Indonesian"
	default:
		if strings.HasPrefix(locale, "zh_") {
			return "Chinese"
		}
		if strings.HasPrefix(locale, "es_") {
			return "Spanish"
		}
		if strings.HasPrefix(locale, "ar_") {
			return "Arabic"
		}
		if strings.HasPrefix(locale, "id_") {
			return "Indonesian"
		}
		return "English"
	}
}
