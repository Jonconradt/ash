package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"time"

	"ash/internal/plugin"
)

type timePlugin struct{}

func (t *timePlugin) Name() string {
	return "what_time_is_it"
}

func (t *timePlugin) Version() string {
	return "1.0.0"
}

func (t *timePlugin) AIDocs() string {
	docs := map[string]any{
		"Capabilities": []string{
			"Retrieve the current date, time, timezone, and epoch timestamp.",
			"Support formatted output in RFC3339/ISO8601, Unix epoch seconds, UTC, or human-readable format.",
			"Support specific IANA timezone conversions.",
		},
		"Arguments": map[string]string{
			"--format":   "Output format: 'rfc3339' (default), 'unix', 'utc', 'human', or 'json'",
			"--timezone": "Optional IANA timezone location such as 'America/New_York', 'UTC', or 'local' (default: local)",
			"--ai-docs":  "Print this documentation and exit",
			"--version":  "Print version and exit",
			"--help":     "Print help and exit",
		},
		"Return format": map[string]string{
			"status":         "success or error",
			"iso8601":        "RFC3339 timestamp in target timezone",
			"utc_iso8601":    "RFC3339 timestamp in UTC",
			"unix_timestamp": "seconds since Unix epoch",
			"timezone":       "active timezone name",
			"human_readable": "formatted date and time string",
		},
		"Usage guidance for the AI": "Use what_time_is_it when you need the current date, time, or day of the week instead of guessing.",
	}
	bytes, err := json.MarshalIndent(docs, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(bytes)
}

func (t *timePlugin) Run(ctx context.Context, args []string, stdout, stderr io.Writer, logger *slog.Logger) int {
	logger.Debug("running what_time_is_it plugin", "args", args, "EID", "wTimE01a")

	flags := flag.NewFlagSet("what_time_is_it", flag.ContinueOnError)
	flags.SetOutput(stderr)

	format := flags.String("format", "json", "Output format: json, rfc3339, unix, utc, human")
	tzName := flags.String("timezone", "local", "Timezone location name")

	if err := flags.Parse(args); err != nil {
		logger.Error("failed to parse arguments", "error", err, "EID", "wTimE02b")
		return 1
	}

	select {
	case <-ctx.Done():
		logger.Warn("execution cancelled", "EID", "wTimE03c")
		return 130
	default:
	}

	now := time.Now()
	loc := time.Local
	if *tzName != "" && *tzName != "local" {
		var err error
		loc, err = time.LoadLocation(*tzName)
		if err != nil {
			logger.Error("invalid timezone location", "timezone", *tzName, "error", err, "EID", "wTimE04d")
			res := map[string]any{
				"status":  "error",
				"message": fmt.Sprintf("invalid timezone %q: %v", *tzName, err),
			}
			out, _ := json.Marshal(res)
			_, _ = fmt.Fprintln(stdout, string(out))
			return 1
		}
	}

	nowInLoc := now.In(loc)
	zoneName, _ := nowInLoc.Zone()

	switch *format {
	case "rfc3339", "iso8601":
		_, _ = fmt.Fprintln(stdout, nowInLoc.Format(time.RFC3339))
	case "unix":
		_, _ = fmt.Fprintf(stdout, "%d\n", nowInLoc.Unix())
	case "utc":
		_, _ = fmt.Fprintln(stdout, now.UTC().Format(time.RFC3339))
	case "human":
		_, _ = fmt.Fprintln(stdout, nowInLoc.Format("Monday, January 2, 2006 15:04:05 MST"))
	case "json":
		fallthrough
	default:
		payload := map[string]any{
			"status":         "success",
			"iso8601":        nowInLoc.Format(time.RFC3339),
			"utc_iso8601":    now.UTC().Format(time.RFC3339),
			"unix_timestamp": nowInLoc.Unix(),
			"timezone":       zoneName,
			"human_readable": nowInLoc.Format("Monday, January 2, 2006 15:04:05 MST"),
		}
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			logger.Error("failed to marshal JSON response", "error", err, "EID", "wTimE05e")
			return 1
		}
		_, _ = fmt.Fprintln(stdout, string(data))
	}

	logger.Debug("completed what_time_is_it execution", "EID", "wTimE06f")
	return 0
}

func main() {
	plugin.Main(&timePlugin{})
}
