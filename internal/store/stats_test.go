package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Sixeight/kiroku/internal/store"
)

func TestLoadSummaryUsesStatsCache(t *testing.T) {
	root := t.TempDir()
	statsPath := filepath.Join(root, "stats-cache.json")

	const payload = `{
  "dailyActivity": [
    {"date":"2026-03-14","messageCount":12,"sessionCount":3,"toolCallCount":8},
    {"date":"2026-03-15","messageCount":20,"sessionCount":5,"toolCallCount":11}
  ],
  "dailyModelTokens": [
    {"date":"2026-03-15","tokensByModel":{"claude-opus-4-6":2200,"claude-sonnet-4-6":400}}
  ],
  "modelUsage": {
    "claude-opus-4-6":{"inputTokens":100,"outputTokens":200,"cacheReadInputTokens":300,"cacheCreationInputTokens":25},
    "claude-sonnet-4-6":{"inputTokens":50,"outputTokens":75,"cacheReadInputTokens":80,"cacheCreationInputTokens":10}
  },
  "totalSessions": 9,
  "totalMessages": 120
}`

	if err := os.WriteFile(statsPath, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, err := store.LoadSummaryFromStatsCache(statsPath)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := summary.Totals.TotalSessions, 9; got != want {
		t.Fatalf("TotalSessions = %d, want %d", got, want)
	}

	if got, want := summary.Totals.TotalMessages, 120; got != want {
		t.Fatalf("TotalMessages = %d, want %d", got, want)
	}

	if got, want := len(summary.DailyActivity), 2; got != want {
		t.Fatalf("DailyActivity len = %d, want %d", got, want)
	}

	if got, want := len(summary.DailyModelTokens), 1; got != want {
		t.Fatalf("DailyModelTokens len = %d, want %d", got, want)
	}

	if got, want := len(summary.ModelUsage), 2; got != want {
		t.Fatalf("ModelUsage len = %d, want %d", got, want)
	}
}
