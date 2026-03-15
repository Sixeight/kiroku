package store_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sixeight/kiroku/internal/index"
	"github.com/Sixeight/kiroku/internal/store"
)

func TestSQLiteStoreListsSessionsWithFilters(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	writeLog(t, filepath.Join(projectRoot, "one.jsonl"), sessionOneLog())
	writeLog(t, filepath.Join(projectRoot, "two.jsonl"), sessionTwoLog())

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if _, err := idx.Reindex(context.Background(), []string{projectRoot}, false); err != nil {
		t.Fatal(err)
	}

	reader, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	sessions, nextCursor, err := reader.ListSessions(context.Background(), store.ListSessionsParams{
		Limit:  10,
		Branch: "feature/search",
		Model:  "claude-sonnet-4-6",
		Q:      "search",
		Sort:   "recent",
	})
	if err != nil {
		t.Fatal(err)
	}

	if nextCursor != "" {
		t.Fatalf("nextCursor = %q, want empty", nextCursor)
	}

	if got, want := len(sessions), 1; got != want {
		t.Fatalf("sessions len = %d, want %d", got, want)
	}

	if got, want := sessions[0].SessionID, "session-2"; got != want {
		t.Fatalf("session id = %q, want %q", got, want)
	}

	if got := sessions[0].PrimaryModel; got != "claude-sonnet-4-6" {
		t.Fatalf("primary model = %q, want %q", got, "claude-sonnet-4-6")
	}

	if got := sessions[0].OutputTokens; got != 5 {
		t.Fatalf("output tokens = %d, want %d", got, 5)
	}
}

func TestSQLiteStoreReadsSessionMessages(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	writeLog(t, filepath.Join(projectRoot, "one.jsonl"), sessionOneLog())

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if _, err := idx.Reindex(context.Background(), []string{projectRoot}, false); err != nil {
		t.Fatal(err)
	}

	reader, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	messages, hasMore, err := reader.ReadSessionMessages(context.Background(), "session-1", 100, 0)
	if err != nil {
		t.Fatal(err)
	}

	if hasMore {
		t.Fatal("hasMore = true, want false")
	}

	if got, want := len(messages), 3; got != want {
		t.Fatalf("messages len = %d, want %d", got, want)
	}

	if got, want := messages[0].Role, "user"; got != want {
		t.Fatalf("messages[0].role = %q, want %q", got, want)
	}

	if got, want := messages[1].Role, "assistant"; got != want {
		t.Fatalf("messages[1].role = %q, want %q", got, want)
	}

	hasThinking := false
	hasToolUse := false
	for _, b := range messages[1].Blocks {
		if b.Type == "thinking" {
			hasThinking = true
		}
		if b.Type == "tool_use" && b.Tool == "Bash" {
			hasToolUse = true
		}
	}
	if !hasThinking {
		t.Fatal("expected thinking block in assistant message")
	}
	if !hasToolUse {
		t.Fatal("expected tool_use block with Bash in assistant message")
	}
}

func TestSQLiteStoreReturnsProjectStats(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	writeLog(t, filepath.Join(projectRoot, "two.jsonl"), sessionTwoLog())
	writeLog(t, filepath.Join(projectRoot, "three.jsonl"), sessionThreeLog())

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if _, err := idx.Reindex(context.Background(), []string{projectRoot}, false); err != nil {
		t.Fatal(err)
	}

	reader, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	stats, err := reader.GetProjectStats(context.Background(), "/tmp/project-search")
	if err != nil {
		t.Fatal(err)
	}

	if got, want := stats.TotalSessions, 2; got != want {
		t.Fatalf("total sessions = %d, want %d", got, want)
	}

	if got, want := stats.OutputTokens, 9; got != want {
		t.Fatalf("output tokens = %d, want %d", got, want)
	}

	if got, want := len(stats.Branches), 1; got != want {
		t.Fatalf("branches len = %d, want %d", got, want)
	}
}

func TestSQLiteStoreReturnsSessionDetailWithTimeline(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	writeLog(t, filepath.Join(projectRoot, "one.jsonl"), sessionOneLog())

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if _, err := idx.Reindex(context.Background(), []string{projectRoot}, false); err != nil {
		t.Fatal(err)
	}

	reader, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	detail, err := reader.GetSession(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}

	if got, want := detail.Meta.SessionID, "session-1"; got != want {
		t.Fatalf("SessionID = %q, want %q", got, want)
	}

	if got, want := detail.TimelineCounts.ToolUse, 2; got != want {
		t.Fatalf("ToolUse = %d, want %d", got, want)
	}

	if got, want := detail.TimelineCounts.ToolResult, 1; got != want {
		t.Fatalf("ToolResult = %d, want %d", got, want)
	}

	if got, want := detail.TimelineCounts.Thinking, 1; got != want {
		t.Fatalf("Thinking = %d, want %d", got, want)
	}

	if got, want := len(detail.Tools), 2; got != want {
		t.Fatalf("tools len = %d, want %d", got, want)
	}

	if got, want := detail.Meta.SubagentCount, 1; got != want {
		t.Fatalf("SubagentCount = %d, want %d", got, want)
	}

	if got, want := detail.Meta.SourcePath, "one.jsonl"; !strings.HasSuffix(got, want) {
		t.Fatalf("SourcePath = %q, want suffix %q", got, want)
	}

	if got, want := detail.Meta.DurationSeconds, 3; got != want {
		t.Fatalf("DurationSeconds = %d, want %d", got, want)
	}
}

func TestSQLiteStoreBuildsSummaryWithRecentAndTopGroups(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")
	statsPath := filepath.Join(root, "stats-cache.json")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	writeLog(t, filepath.Join(projectRoot, "one.jsonl"), sessionOneLog())
	writeLog(t, filepath.Join(projectRoot, "two.jsonl"), sessionTwoLog())
	writeLog(t, filepath.Join(projectRoot, "three.jsonl"), sessionThreeLog())

	if err := os.WriteFile(statsPath, []byte(`{"dailyActivity":[],"dailyModelTokens":[],"modelUsage":{},"totalSessions":3,"totalMessages":14}`), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if _, err := idx.Reindex(context.Background(), []string{projectRoot}, false); err != nil {
		t.Fatal(err)
	}

	reader, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	summary, err := reader.LoadSummary(context.Background(), statsPath)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := len(summary.RecentSessions), 3; got != want {
		t.Fatalf("recent sessions len = %d, want %d", got, want)
	}

	if got, want := summary.TopBranches[0].Name, "feature/search"; got != want {
		t.Fatalf("top branch = %q, want %q", got, want)
	}

	if got, want := summary.TopProjects[0].Name, "/tmp/project-search"; got != want {
		t.Fatalf("top project = %q, want %q", got, want)
	}

	if got, want := summary.Analytics.TotalToolCalls, 3; got != want {
		t.Fatalf("total tool calls = %d, want %d", got, want)
	}

	if got, want := summary.Analytics.UniqueTools, 2; got != want {
		t.Fatalf("unique tools = %d, want %d", got, want)
	}

	if got, want := summary.Analytics.UniqueModels, 3; got != want {
		t.Fatalf("unique models = %d, want %d", got, want)
	}

	if got, want := summary.Analytics.TotalInputTokens, 31; got != want {
		t.Fatalf("total input tokens = %d, want %d", got, want)
	}

	if got, want := summary.Analytics.TopTools[0].Name, "Read"; got != want {
		t.Fatalf("top tool = %q, want %q", got, want)
	}

	if got, want := summary.Analytics.TopModels[0].Name, "claude-opus-4-6"; got != want {
		t.Fatalf("top model = %q, want %q", got, want)
	}

	if got, want := summary.Analytics.LongestSessions[0].SessionID, "session-1"; got != want {
		t.Fatalf("longest session = %q, want %q", got, want)
	}

	if got, want := summary.Analytics.BusiestSessions[0].SessionID, "session-1"; got != want {
		t.Fatalf("busiest session = %q, want %q", got, want)
	}

	sourceInfo, err := reader.SourceInfo(context.Background(), store.SourceInfoParams{
		ConfigRoot:   "/tmp/claude-config",
		StatsPath:    statsPath,
		ProjectRoots: []string{projectRoot},
	})
	if err != nil {
		t.Fatal(err)
	}

	if got, want := sourceInfo.ConfigRoot, "/tmp/claude-config"; got != want {
		t.Fatalf("config root = %q, want %q", got, want)
	}

	if got, want := sourceInfo.IndexedFiles, 3; got != want {
		t.Fatalf("indexed files = %d, want %d", got, want)
	}

	if got, want := sourceInfo.StatsStatus, "present"; got != want {
		t.Fatalf("stats status = %q, want %q", got, want)
	}
}

func TestSQLiteStoreSearchesMessageContent(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	writeLog(t, filepath.Join(projectRoot, "one.jsonl"), sessionOneLog())
	writeLog(t, filepath.Join(projectRoot, "two.jsonl"), sessionTwoLog())

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if _, err := idx.Reindex(context.Background(), []string{projectRoot}, false); err != nil {
		t.Fatal(err)
	}

	reader, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	// "subagent" only appears in session-1's message content, not in preview/cwd/branch
	sessions, _, err := reader.ListSessions(context.Background(), store.ListSessionsParams{
		Limit:   10,
		Content: "subagent",
	})
	if err != nil {
		t.Fatal(err)
	}

	if got, want := len(sessions), 1; got != want {
		t.Fatalf("sessions len = %d, want %d", got, want)
	}

	if got, want := sessions[0].SessionID, "session-1"; got != want {
		t.Fatalf("session id = %q, want %q", got, want)
	}
}

func TestSQLiteStoreReturnsSessionCWD(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	writeLog(t, filepath.Join(projectRoot, "one.jsonl"), sessionOneLog())

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if _, err := idx.Reindex(context.Background(), []string{projectRoot}, false); err != nil {
		t.Fatal(err)
	}

	reader, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	cwd, err := reader.GetSessionCWD(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}

	if got, want := cwd, "/tmp/project-core"; got != want {
		t.Fatalf("cwd = %q, want %q", got, want)
	}

	_, err = reader.GetSessionCWD(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestSQLiteStoreSearchesMessagesWithQuery(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	writeLog(t, filepath.Join(projectRoot, "one.jsonl"), sessionOneLog())

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if _, err := idx.Reindex(context.Background(), []string{projectRoot}, false); err != nil {
		t.Fatal(err)
	}

	reader, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	// Search within session messages using q parameter
	messages, _, err := reader.ReadSessionMessages(context.Background(), "session-1", 100, 0, "done")
	if err != nil {
		t.Fatal(err)
	}

	// Should return only the assistant message containing "done"
	if got := len(messages); got != 1 {
		t.Fatalf("messages len = %d, want 1", got)
	}

	if got, want := messages[0].Role, "assistant"; got != want {
		t.Fatalf("messages[0].role = %q, want %q", got, want)
	}
}

func TestSQLiteStoreReturnsSessionDetailWithPRLinks(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	writeLog(t, filepath.Join(projectRoot, "one.jsonl"), sessionOneLogWithPR())

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if _, err := idx.Reindex(context.Background(), []string{projectRoot}, false); err != nil {
		t.Fatal(err)
	}

	reader, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	detail, err := reader.GetSession(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}

	if got, want := len(detail.PRs), 2; got != want {
		t.Fatalf("PRs len = %d, want %d", got, want)
	}

	if got, want := detail.PRs[0].PRNumber, 123; got != want {
		t.Fatalf("PRs[0].PRNumber = %d, want %d", got, want)
	}

	if got, want := detail.PRs[0].PRUrl, "https://github.com/org/repo/pull/123"; got != want {
		t.Fatalf("PRs[0].PRUrl = %q, want %q", got, want)
	}

	if got, want := detail.PRs[0].PRRepository, "org/repo"; got != want {
		t.Fatalf("PRs[0].PRRepository = %q, want %q", got, want)
	}
}

func TestSQLiteStoreListSessionsIncludesPRLinks(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	writeLog(t, filepath.Join(projectRoot, "one.jsonl"), sessionOneLogWithPR())

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if _, err := idx.Reindex(context.Background(), []string{projectRoot}, false); err != nil {
		t.Fatal(err)
	}

	reader, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	sessions, _, err := reader.ListSessions(context.Background(), store.ListSessionsParams{
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	if got, want := len(sessions), 1; got != want {
		t.Fatalf("sessions len = %d, want %d", got, want)
	}

	if got, want := len(sessions[0].PRs), 2; got != want {
		t.Fatalf("PRs len = %d, want %d", got, want)
	}
}

// setupStore creates a temp DB with the given log files indexed and returns the store.
func setupStore(t *testing.T, logs map[string]string) *store.SQLiteStore {
	t.Helper()

	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	for name, content := range logs {
		writeLog(t, filepath.Join(projectRoot, name), content)
	}

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if _, err := idx.Reindex(context.Background(), []string{projectRoot}, false); err != nil {
		t.Fatal(err)
	}

	reader, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close() })

	return reader
}

func TestSQLiteStoreListSessionsWithPagination(t *testing.T) {
	reader := setupStore(t, map[string]string{
		"one.jsonl":   sessionOneLog(),
		"two.jsonl":   sessionTwoLog(),
		"three.jsonl": sessionThreeLog(),
	})

	// Page 1: limit=1
	page1, cursor1, err := reader.ListSessions(context.Background(), store.ListSessionsParams{
		Limit: 1,
		Sort:  "recent",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(page1), 1; got != want {
		t.Fatalf("page1 len = %d, want %d", got, want)
	}
	if cursor1 == "" {
		t.Fatal("expected non-empty cursor after page 1")
	}

	// Page 2: use cursor from page 1
	page2, cursor2, err := reader.ListSessions(context.Background(), store.ListSessionsParams{
		Limit:  1,
		Sort:   "recent",
		Cursor: cursor1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(page2), 1; got != want {
		t.Fatalf("page2 len = %d, want %d", got, want)
	}
	if cursor2 == "" {
		t.Fatal("expected non-empty cursor after page 2")
	}
	// Page 2 session must differ from page 1
	if page2[0].SessionID == page1[0].SessionID {
		t.Fatalf("page2 returned same session as page1: %s", page2[0].SessionID)
	}

	// Page 3: use cursor from page 2
	page3, cursor3, err := reader.ListSessions(context.Background(), store.ListSessionsParams{
		Limit:  1,
		Sort:   "recent",
		Cursor: cursor2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(page3), 1; got != want {
		t.Fatalf("page3 len = %d, want %d", got, want)
	}
	// No more pages after 3 sessions
	if cursor3 != "" {
		t.Fatalf("expected empty cursor after last page, got %q", cursor3)
	}

	// All 3 sessions should be distinct
	ids := map[string]bool{
		page1[0].SessionID: true,
		page2[0].SessionID: true,
		page3[0].SessionID: true,
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 distinct session IDs, got %d", len(ids))
	}
}

func TestSQLiteStoreListSessionsEmptyResult(t *testing.T) {
	reader := setupStore(t, map[string]string{
		"one.jsonl": sessionOneLog(),
	})

	sessions, cursor, err := reader.ListSessions(context.Background(), store.ListSessionsParams{
		Limit: 10,
		CWD:   "/nonexistent/path/that/does/not/exist",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cursor != "" {
		t.Fatalf("expected empty cursor, got %q", cursor)
	}
	if sessions == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(sessions))
	}

	// Also test with nonexistent branch
	sessions2, cursor2, err := reader.ListSessions(context.Background(), store.ListSessionsParams{
		Limit:  10,
		Branch: "nonexistent/branch",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cursor2 != "" {
		t.Fatalf("expected empty cursor for branch filter, got %q", cursor2)
	}
	if sessions2 == nil {
		t.Fatal("expected non-nil empty slice for branch filter, got nil")
	}
	if len(sessions2) != 0 {
		t.Fatalf("expected 0 sessions for branch filter, got %d", len(sessions2))
	}
}

func TestSQLiteStoreListSessionsWithDateRange(t *testing.T) {
	// session-1: started 2026-03-15T10:00:00Z
	// session-2: started 2026-03-15T11:00:00Z
	// session-3: started 2026-03-15T09:00:00Z
	reader := setupStore(t, map[string]string{
		"one.jsonl":   sessionOneLog(),
		"two.jsonl":   sessionTwoLog(),
		"three.jsonl": sessionThreeLog(),
	})

	// Filter: from 09:30 to 10:30 should only include session-1 (10:00)
	sessions, _, err := reader.ListSessions(context.Background(), store.ListSessionsParams{
		Limit: 10,
		From:  "2026-03-15T09:30:00Z",
		To:    "2026-03-15T10:30:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(sessions), 1; got != want {
		t.Fatalf("date range sessions len = %d, want %d", got, want)
	}
	if got, want := sessions[0].SessionID, "session-1"; got != want {
		t.Fatalf("session id = %q, want %q", got, want)
	}

	// Filter: from 08:00 to 09:30 should only include session-3 (09:00)
	sessions2, _, err := reader.ListSessions(context.Background(), store.ListSessionsParams{
		Limit: 10,
		From:  "2026-03-15T08:00:00Z",
		To:    "2026-03-15T09:30:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(sessions2), 1; got != want {
		t.Fatalf("date range 2 sessions len = %d, want %d", got, want)
	}
	if got, want := sessions2[0].SessionID, "session-3"; got != want {
		t.Fatalf("session id = %q, want %q", got, want)
	}

	// Filter: future date range should return empty
	sessions3, _, err := reader.ListSessions(context.Background(), store.ListSessionsParams{
		Limit: 10,
		From:  "2099-01-01T00:00:00Z",
		To:    "2099-12-31T23:59:59Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions3) != 0 {
		t.Fatalf("expected 0 sessions for future date range, got %d", len(sessions3))
	}
}

func TestSQLiteStoreListSessionsSortByLongest(t *testing.T) {
	// session-1: 10:00:00Z to 10:00:03Z = 3 seconds (via sidechain timestamp)
	// session-2: 11:00:00Z to 11:00:01Z = 1 second
	// session-3: 09:00:00Z to 09:00:01Z = 1 second
	reader := setupStore(t, map[string]string{
		"one.jsonl":   sessionOneLog(),
		"two.jsonl":   sessionTwoLog(),
		"three.jsonl": sessionThreeLog(),
	})

	sessions, _, err := reader.ListSessions(context.Background(), store.ListSessionsParams{
		Limit: 10,
		Sort:  "longest",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(sessions), 3; got != want {
		t.Fatalf("sessions len = %d, want %d", got, want)
	}

	// Default direction is DESC, so longest first
	if got, want := sessions[0].SessionID, "session-1"; got != want {
		t.Fatalf("first (longest) session = %q, want %q", got, want)
	}
}

func TestSQLiteStoreGetSessionNotFound(t *testing.T) {
	reader := setupStore(t, map[string]string{
		"one.jsonl": sessionOneLog(),
	})

	_, err := reader.GetSession(context.Background(), "nonexistent-session-id")
	if err == nil {
		t.Fatal("expected error for nonexistent session, got nil")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestSQLiteStoreReadSessionMessagesNonexistentSession(t *testing.T) {
	reader := setupStore(t, map[string]string{
		"one.jsonl": sessionOneLog(),
	})

	_, _, err := reader.ReadSessionMessages(context.Background(), "nonexistent-session-id", 100, 0)
	if err == nil {
		t.Fatal("expected error for nonexistent session, got nil")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestSQLiteStoreReadSessionMessagesWithPagination(t *testing.T) {
	reader := setupStore(t, map[string]string{
		"one.jsonl": sessionOneLog(),
	})

	// session-1 has 3 messages (user, assistant, user with tool_result)
	// The sidechain message is skipped by ReadSessionMessages.
	// Read with limit=1, offset=0
	msgs1, hasMore1, err := reader.ReadSessionMessages(context.Background(), "session-1", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasMore1 {
		t.Fatal("expected hasMore=true with limit=1 and 3 messages")
	}
	if got, want := len(msgs1), 1; got != want {
		t.Fatalf("msgs1 len = %d, want %d", got, want)
	}

	// Read with limit=1, offset=1
	msgs2, hasMore2, err := reader.ReadSessionMessages(context.Background(), "session-1", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	// After offset=1 with limit=1, there should be 1 more message left
	if got, want := len(msgs2), 1; got != want {
		t.Fatalf("msgs2 len = %d, want %d", got, want)
	}
	// msgs2 should be the second message (assistant)
	if got, want := msgs2[0].Role, "assistant"; got != want {
		t.Fatalf("msgs2[0].role = %q, want %q", got, want)
	}
	_ = hasMore2
}

func TestSQLiteStoreSanitizeFTS5SpecialCharacters(t *testing.T) {
	reader := setupStore(t, map[string]string{
		"one.jsonl": sessionOneLog(),
		"two.jsonl": sessionTwoLog(),
	})

	// These are all FTS5 special characters that could cause SQL errors
	specials := []string{
		`"`,
		`"unterminated`,
		`""`,
		`col:value`,
		`*`,
		`term*`,
		`NEAR(a b)`,
		`a OR b`,
		`a AND b`,
		`a NOT b`,
		`(`,
		`)`,
		`(group)`,
		`^start`,
		`{`,
		`}`,
		`a + b`,
		`"""`,
	}

	for _, special := range specials {
		t.Run(special, func(t *testing.T) {
			sessions, _, err := reader.ListSessions(context.Background(), store.ListSessionsParams{
				Limit:   10,
				Content: special,
			})
			if err != nil {
				t.Fatalf("FTS5 search with %q caused error: %v", special, err)
			}
			// We don't assert on result count -- just that it doesn't crash
			_ = sessions
		})
	}
}

func TestSQLiteStoreGetProjectStatsEmptyCWD(t *testing.T) {
	reader := setupStore(t, map[string]string{
		"one.jsonl": sessionOneLog(),
	})

	stats, err := reader.GetProjectStats(context.Background(), "/nonexistent/project/path")
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalSessions != 0 {
		t.Fatalf("expected 0 sessions, got %d", stats.TotalSessions)
	}
	if stats.TotalMessages != 0 {
		t.Fatalf("expected 0 messages, got %d", stats.TotalMessages)
	}
	if stats.TotalToolCalls != 0 {
		t.Fatalf("expected 0 tool calls, got %d", stats.TotalToolCalls)
	}
	if stats.InputTokens != 0 {
		t.Fatalf("expected 0 input tokens, got %d", stats.InputTokens)
	}
	if stats.OutputTokens != 0 {
		t.Fatalf("expected 0 output tokens, got %d", stats.OutputTokens)
	}
	if stats.CWD != "/nonexistent/project/path" {
		t.Fatalf("expected CWD to be set, got %q", stats.CWD)
	}
	if len(stats.TopTools) != 0 {
		t.Fatalf("expected empty TopTools, got %d", len(stats.TopTools))
	}
	if len(stats.TopModels) != 0 {
		t.Fatalf("expected empty TopModels, got %d", len(stats.TopModels))
	}
	if len(stats.Branches) != 0 {
		t.Fatalf("expected empty Branches, got %d", len(stats.Branches))
	}
}

func writeLog(t *testing.T, path, payload string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sessionOneLogWithPR() string {
	return sessionOneLog() +
		`{"type":"pr-link","sessionId":"session-1","prNumber":123,"prUrl":"https://github.com/org/repo/pull/123","prRepository":"org/repo","timestamp":"2026-03-15T10:01:00Z"}` + "\n" +
		`{"type":"pr-link","sessionId":"session-1","prNumber":123,"prUrl":"https://github.com/org/repo/pull/123","prRepository":"org/repo","timestamp":"2026-03-15T10:02:00Z"}` + "\n" +
		`{"type":"pr-link","sessionId":"session-1","prNumber":456,"prUrl":"https://github.com/org/repo/pull/456","prRepository":"org/repo","timestamp":"2026-03-15T10:03:00Z"}` + "\n"
}

func sessionOneLog() string {
	return `{"sessionId":"session-1","cwd":"/tmp/project-core","gitBranch":"main","type":"user","message":{"role":"user","content":[{"type":"text","text":"first prompt"}]},"timestamp":"2026-03-15T10:00:00Z"}
{"sessionId":"session-1","cwd":"/tmp/project-core","gitBranch":"main","type":"assistant","message":{"model":"claude-opus-4-6","role":"assistant","usage":{"input_tokens":12,"output_tokens":9,"cache_read_input_tokens":5,"cache_creation_input_tokens":2},"content":[{"type":"thinking","thinking":"..."},{"type":"tool_use","name":"Bash","id":"tool-1","input":{"command":"pwd"}},{"type":"tool_use","name":"Read","id":"tool-2","input":{"file_path":"a.go"}},{"type":"text","text":"done"}]},"timestamp":"2026-03-15T10:00:01Z"}
{"sessionId":"session-1","cwd":"/tmp/project-core","gitBranch":"main","type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":"ok"}]},"timestamp":"2026-03-15T10:00:02Z"}
{"sessionId":"session-1","cwd":"/tmp/project-core","gitBranch":"main","isSidechain":true,"agentId":"sub-1","type":"assistant","message":{"model":"claude-haiku-4-5-20251001","role":"assistant","usage":{"input_tokens":2,"output_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},"content":[{"type":"text","text":"subagent"}]},"timestamp":"2026-03-15T10:00:03Z"}
`
}

func sessionTwoLog() string {
	return `{"sessionId":"session-2","cwd":"/tmp/project-search","gitBranch":"feature/search","type":"user","message":{"role":"user","content":[{"type":"text","text":"search bug"}]},"timestamp":"2026-03-15T11:00:00Z"}
{"sessionId":"session-2","cwd":"/tmp/project-search","gitBranch":"feature/search","type":"assistant","message":{"model":"claude-sonnet-4-6","role":"assistant","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":3,"cache_creation_input_tokens":1},"content":[{"type":"tool_use","name":"Read","id":"tool-2","input":{"file_path":"a.go"}},{"type":"text","text":"search fixed"}]},"timestamp":"2026-03-15T11:00:01Z"}
`
}

func sessionThreeLog() string {
	return `{"sessionId":"session-3","cwd":"/tmp/project-search","gitBranch":"feature/search","type":"user","message":{"role":"user","content":[{"type":"text","text":"other work"}]},"timestamp":"2026-03-15T09:00:00Z"}
{"sessionId":"session-3","cwd":"/tmp/project-search","gitBranch":"feature/search","type":"assistant","message":{"model":"claude-opus-4-6","role":"assistant","usage":{"input_tokens":7,"output_tokens":4,"cache_read_input_tokens":2,"cache_creation_input_tokens":1},"content":[{"type":"text","text":"done"}]},"timestamp":"2026-03-15T09:00:01Z"}
`
}
