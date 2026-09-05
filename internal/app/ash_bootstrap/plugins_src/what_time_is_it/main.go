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
			"--format":   "Output format: 'json' (default), 'human', 'rfc3339', 'unix', or 'utc'",
			"--timezone": "Optional IANA timezone location such as 'America/New_York', 'UTC', or 'local' (default: local)",
			"--ai-docs":  "Print this documentation and exit",
			"--version":  "Print version and exit",
			"--help":     "Print help and exit",
		},
		"Return format": map[string]string{
			"status":         "success or error",
			"local_time":     "Current local 12-hour time with AM/PM (e.g. '5:15:30 PM')",
			"local_time_24h": "Current local 24-hour time (e.g. '17:15:30')",
			"local_date":     "Current local date (e.g. 'Saturday, September 5, 2026')",
			"local_datetime": "Full human-readable local date, time, and timezone",
			"timezone":       "Local timezone abbreviation (e.g. 'EDT', 'PST')",
			"iso8601":        "RFC3339 timestamp in local timezone",
			"utc_iso8601":    "RFC3339 timestamp in UTC",
			"unix_timestamp": "seconds since Unix epoch",
			"human_readable": "Full formatted date and time string in local timezone",
		},
		"Usage guidance for the AI": "When asked for the current time or date, ALWAYS report the local time (from 'local_time' or 'local_datetime' and 'timezone'). Do not convert from UTC or assume a different timezone unless the user explicitly requested a specific timezone.",
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

	format := flags.String("format", "json", "Output format: json, human, rfc3339, unix, utc")
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
		_, _ = fmt.Fprintln(stdout, nowInLoc.Format("Monday, January 2, 2006 3:04:05 PM MST"))
	case "json":
		fallthrough
	default:
		payload := map[string]any{
			"status":         "success",
			"local_time":     nowInLoc.Format("3:04:05 PM"),
			"local_time_24h": nowInLoc.Format("15:04:05"),
			"local_date":     nowInLoc.Format("Monday, January 2, 2006"),
			"local_datetime": nowInLoc.Format("Monday, January 2, 2006 3:04:05 PM MST"),
			"timezone":       zoneName,
			"iso8601":        nowInLoc.Format(time.RFC3339),
			"utc_iso8601":    now.UTC().Format(time.RFC3339),
			"unix_timestamp": nowInLoc.Unix(),
			"human_readable": nowInLoc.Format("Monday, January 2, 2006 3:04:05 PM MST"),
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
