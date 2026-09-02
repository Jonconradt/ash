// Command ash is the ash CLI entry point; see internal/app for implementation.
package main

import (
	"os"

	"ash/internal/app"
)

func main() {
	os.Exit(app.Run(os.Args[1:], os.Stdout, os.Stderr))
}
