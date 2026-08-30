package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestRunToolPipelineClosesParentPipeFDs is a regression test for a deadlock where the
// parent process kept its own copy of each pipe's fds open, so a downstream stage that
// reads until EOF (most Unix tools) never saw EOF and hung until the timeout killed it.
func TestRunToolPipelineClosesParentPipeFDs(t *testing.T) {
	t.Run("two stage pipeline reads to EOF promptly", func(t *testing.T) {
		commands := [][]string{
			{"printf", "a\\nb\\nc\\n"},
			{"wc", "-l"},
		}
		started := time.Now()
		result := runToolPipeline(context.Background(), commands, "printf | wc -l", 2*time.Second, 8192)
		elapsed := time.Since(started)

		if !result.OK {
			t.Fatalf("expected pipeline success, got %#v", result)
		}
		if elapsed >= 2*time.Second {
			t.Fatalf("pipeline took %s, expected it to finish well before the timeout", elapsed)
		}
		if got := strings.TrimSpace(result.Stdout); got != "3" {
			t.Fatalf("expected wc -l output %q, got %q", "3", got)
		}
	})

	t.Run("multi stage pipeline propagates EOF through every intermediate pipe", func(t *testing.T) {
		commands := [][]string{
			{"printf", "a\\nb\\nc\\nd\\n"},
			{"cat"},
			{"cat"},
			{"wc", "-l"},
		}
		started := time.Now()
		result := runToolPipeline(context.Background(), commands, "printf | cat | cat | wc -l", 2*time.Second, 8192)
		elapsed := time.Since(started)

		if !result.OK {
			t.Fatalf("expected pipeline success, got %#v", result)
		}
		if elapsed >= 2*time.Second {
			t.Fatalf("pipeline took %s, expected it to finish well before the timeout", elapsed)
		}
		if got := strings.TrimSpace(result.Stdout); got != "4" {
			t.Fatalf("expected wc -l output %q, got %q", "4", got)
		}
	})
}
