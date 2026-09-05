package plugin

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

// Plugin represents the standard lifecycle and documentation interface for an Ash native Go plugin.
type Plugin interface {
	Name() string
	Version() string
	AIDocs() string
	Run(ctx context.Context, args []string, stdout, stderr io.Writer, logger *slog.Logger) int
}

// SetupLogger initializes a structured slog.Logger connected to ASH_LOG_FILE if configured.
func SetupLogger(pluginName string) (*slog.Logger, func()) {
	logFile := strings.TrimSpace(os.Getenv("ASH_LOG_FILE"))
	verbose := strings.TrimSpace(os.Getenv("ASH_VERBOSE"))
	format := strings.ToLower(strings.TrimSpace(os.Getenv("ASH_LOG_FORMAT")))

	var level slog.Level
	if verbose == "1" || strings.EqualFold(verbose, "true") || strings.EqualFold(verbose, "yes") || strings.EqualFold(verbose, "debug") {
		level = slog.LevelDebug
	} else {
		level = slog.LevelInfo
	}

	var w io.Writer = os.Stderr
	closer := func() {}

	if logFile != "" {
		// #nosec G703 -- log directory is created based on configured log file path.
		if err := os.MkdirAll(filepath.Dir(logFile), 0o700); err == nil {
			// #nosec G703 G304 G302 -- log file is opened using owner-only permissions.
			if f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
				w = f
				closer = func() { _ = f.Close() }
			}
		}
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}

	var handler slog.Handler
	if format == "json" {
		handler = slog.NewJSONHandler(w, opts)
	} else {
		handler = slog.NewTextHandler(w, opts)
	}

	logger := slog.New(handler).With("plugin", pluginName)
	return logger, closer
}

// Main runs the standard plugin lifecycle: flag parsing, signal trapping, logger setup, and clean shutdown.
func Main(p Plugin) {
	os.Exit(Run(p, os.Args[1:], os.Stdout, os.Stderr))
}

// Run executes the plugin lifecycle with customizable arguments and streams.
func Run(p Plugin, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "--ai-docs":
			_, _ = fmt.Fprintln(stdout, p.AIDocs())
			return 0
		case "--version", "-v":
			_, _ = fmt.Fprintf(stdout, "%s %s\n", p.Name(), p.Version())
			return 0
		case "--help", "-h":
			_, _ = fmt.Fprintf(stdout, "%s - %s plugin for Ash\n\nUsage:\n  %s [flags/arguments]\n\nFlags:\n  --ai-docs     Show AI capability documentation and exit\n  -v, --version Show version and exit\n  -h, --help    Show help and exit\n", p.Name(), p.Version(), p.Name())
			return 0
		}
	}

	logger, cleanup := SetupLogger(p.Name())
	defer cleanup()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return p.Run(ctx, args, stdout, stderr, logger)
}
