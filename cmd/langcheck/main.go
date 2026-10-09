package main

import (
	"fmt"
	"os"
	"path/filepath"

	"ash/internal/localize"
)

func main() {
	if err := localize.ValidateDir("internal/localize/languages"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, locale := range []string{"zh_CN", "zh_TW", "es_ES", "ar_AE", "id_ID"} {
		if _, err := os.Stat(filepath.Join("docs", "man", locale, "ash.1")); err != nil {
			fmt.Fprintf(os.Stderr, "localized man page missing for %s: %v\n", locale, err)
			os.Exit(1)
		}
	}
	fmt.Println("language catalogs: ok")
}
