package index_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sixeight/kiroku/internal/index"
	_ "modernc.org/sqlite"
)

func TestReindexBuildsSessionAndSkipsUnchangedFiles(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(projectRoot, "session.jsonl")
	if err := os.WriteFile(logPath, []byte(validSessionLog()), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	report, err := idx.Reindex(context.Background(), []string{projectRoot}, false)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := report.FilesIndexed, 1; got != want {
		t.Fatalf("FilesIndexed = %d, want %d", got, want)
	}

	if got, want := sessionCount(t, dbPath), 1; got != want {
		t.Fatalf("session count = %d, want %d", got, want)
	}

	if got, want := toolCount(t, dbPath, "session-1", "Bash"), 1; got != want {
		t.Fatalf("Bash count = %d, want %d", got, want)
	}

	if got, want := modelOutputTokens(t, dbPath, "session-1", "claude-opus-4-6"), 9; got != want {
		t.Fatalf("output tokens = %d, want %d", got, want)
	}

	report, err = idx.Reindex(context.Background(), []string{projectRoot}, false)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := report.FilesIndexed, 0; got != want {
		t.Fatalf("FilesIndexed after unchanged run = %d, want %d", got, want)
	}

	if err := os.WriteFile(logPath, []byte(validSessionLogWithExtraToolCall()), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err = idx.Reindex(context.Background(), []string{projectRoot}, false)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := report.FilesIndexed, 1; got != want {
		t.Fatalf("FilesIndexed after mutation = %d, want %d", got, want)
	}

	if got, want := toolCount(t, dbPath, "session-1", "Bash"), 2; got != want {
		t.Fatalf("Bash count after mutation = %d, want %d", got, want)
	}
}

func TestReindexCountsBrokenLinesAndContinues(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(projectRoot, "broken.jsonl")
	payload := validSessionLog() + "this-is-not-json\n"
	if err := os.WriteFile(logPath, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	report, err := idx.Reindex(context.Background(), []string{projectRoot}, false)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := report.BrokenLines, 1; got != want {
		t.Fatalf("BrokenLines = %d, want %d", got, want)
	}

	if got, want := sessionCount(t, dbPath), 1; got != want {
		t.Fatalf("session count = %d, want %d", got, want)
	}
}

func sessionCount(t *testing.T, dbPath string) int {
	t.Helper()

	db := openDB(t, dbPath)
	defer db.Close()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatal(err)
	}

	return count
}

func toolCount(t *testing.T, dbPath, sessionID, toolName string) int {
	t.Helper()

	db := openDB(t, dbPath)
	defer db.Close()

	var count int
	if err := db.QueryRow(
		`SELECT count FROM session_tools WHERE session_id = ? AND tool_name = ?`,
		sessionID,
		toolName,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}

	return count
}

func modelOutputTokens(t *testing.T, dbPath, sessionID, model string) int {
	t.Helper()

	db := openDB(t, dbPath)
	defer db.Close()

	var count int
	if err := db.QueryRow(
		`SELECT output_tokens FROM session_models WHERE session_id = ? AND model = ?`,
		sessionID,
		model,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}

	return count
}

func openDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}

	return db
}

func TestReindexPopulatesFTSContent(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(projectRoot, "session.jsonl")
	if err := os.WriteFile(logPath, []byte(validSessionLog()), 0o644); err != nil {
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

	db := openDB(t, dbPath)
	defer db.Close()

	var content string
	if err := db.QueryRow(
		`SELECT content FROM session_content_fts WHERE session_id = ?`,
		"session-1",
	).Scan(&content); err != nil {
		t.Fatalf("FTS content query failed: %v", err)
	}

	if !strings.Contains(content, "first prompt") {
		t.Fatalf("FTS content missing user text: %q", content)
	}

	if !strings.Contains(content, "done") {
		t.Fatalf("FTS content missing assistant text: %q", content)
	}

	if !strings.Contains(content, "subagent") {
		t.Fatalf("FTS content missing subagent text: %q", content)
	}
}

func TestReindexParsesPRLinks(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(projectRoot, "session.jsonl")
	payload := validSessionLog() +
		`{"type":"pr-link","sessionId":"session-1","prNumber":123,"prUrl":"https://github.com/org/repo/pull/123","prRepository":"org/repo","timestamp":"2026-03-15T10:01:00Z"}` + "\n" +
		`{"type":"pr-link","sessionId":"session-1","prNumber":123,"prUrl":"https://github.com/org/repo/pull/123","prRepository":"org/repo","timestamp":"2026-03-15T10:02:00Z"}` + "\n" +
		`{"type":"pr-link","sessionId":"session-1","prNumber":456,"prUrl":"https://github.com/org/repo/pull/456","prRepository":"org/repo","timestamp":"2026-03-15T10:03:00Z"}` + "\n"
	if err := os.WriteFile(logPath, []byte(payload), 0o644); err != nil {
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

	db := openDB(t, dbPath)
	defer db.Close()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM session_prs WHERE session_id = ?`, "session-1").Scan(&count); err != nil {
		t.Fatalf("query session_prs: %v", err)
	}

	if got, want := count, 2; got != want {
		t.Fatalf("PR count = %d, want %d (should deduplicate)", got, want)
	}

	var prURL string
	if err := db.QueryRow(`SELECT pr_url FROM session_prs WHERE session_id = ? AND pr_number = ?`, "session-1", 123).Scan(&prURL); err != nil {
		t.Fatalf("query PR 123: %v", err)
	}
	if got, want := prURL, "https://github.com/org/repo/pull/123"; got != want {
		t.Fatalf("pr_url = %q, want %q", got, want)
	}
}

func validSessionLog() string {
	return `{"sessionId":"session-1","cwd":"/tmp/project","gitBranch":"main","type":"user","message":{"role":"user","content":[{"type":"text","text":"first prompt"}]},"timestamp":"2026-03-15T10:00:00Z"}
{"sessionId":"session-1","cwd":"/tmp/project","gitBranch":"main","type":"assistant","message":{"model":"claude-opus-4-6","role":"assistant","usage":{"input_tokens":12,"output_tokens":9,"cache_read_input_tokens":5,"cache_creation_input_tokens":2},"content":[{"type":"thinking","thinking":"..."},{"type":"tool_use","name":"Bash","id":"tool-1","input":{"command":"pwd"}},{"type":"text","text":"done"}]},"timestamp":"2026-03-15T10:00:01Z"}
{"sessionId":"session-1","cwd":"/tmp/project","gitBranch":"main","type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":"ok"}]},"timestamp":"2026-03-15T10:00:02Z"}
{"sessionId":"session-1","cwd":"/tmp/project","gitBranch":"main","isSidechain":true,"type":"assistant","message":{"model":"claude-haiku-4-5-20251001","role":"assistant","usage":{"input_tokens":2,"output_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},"content":[{"type":"text","text":"subagent"}]},"timestamp":"2026-03-15T10:00:03Z"}
`
}

func validSessionLogWithExtraToolCall() string {
	return validSessionLog() + `{"sessionId":"session-1","cwd":"/tmp/project","gitBranch":"main","type":"assistant","message":{"model":"claude-opus-4-6","role":"assistant","usage":{"input_tokens":1,"output_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},"content":[{"type":"tool_use","name":"Bash","id":"tool-2","input":{"command":"ls"}}]},"timestamp":"2026-03-15T10:00:04Z"}
`
}

func sessionPreview(t *testing.T, dbPath, sessionID string) string {
	t.Helper()

	db := openDB(t, dbPath)
	defer db.Close()

	var preview string
	if err := db.QueryRow(
		`SELECT preview FROM sessions WHERE session_id = ?`,
		sessionID,
	).Scan(&preview); err != nil {
		t.Fatal(err)
	}

	return preview
}

func sessionMessageCount(t *testing.T, dbPath, sessionID string) int {
	t.Helper()

	db := openDB(t, dbPath)
	defer db.Close()

	var count int
	if err := db.QueryRow(
		`SELECT message_count FROM sessions WHERE session_id = ?`,
		sessionID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}

	return count
}

func sessionSourcePath(t *testing.T, dbPath, sessionID string) string {
	t.Helper()

	db := openDB(t, dbPath)
	defer db.Close()

	var sourcePath string
	if err := db.QueryRow(
		`SELECT source_path FROM sessions WHERE session_id = ?`,
		sessionID,
	).Scan(&sourcePath); err != nil {
		t.Fatal(err)
	}

	return sourcePath
}

// TestReindexEmptyProjectRoot verifies that reindexing a directory with
// no JSONL files produces zero results and no errors.
func TestReindexEmptyProjectRoot(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	report, err := idx.Reindex(context.Background(), []string{projectRoot}, false)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := report.FilesIndexed, 0; got != want {
		t.Fatalf("FilesIndexed = %d, want %d", got, want)
	}

	if got, want := report.BrokenLines, 0; got != want {
		t.Fatalf("BrokenLines = %d, want %d", got, want)
	}

	if got, want := sessionCount(t, dbPath), 0; got != want {
		t.Fatalf("session count = %d, want %d", got, want)
	}
}

// TestReindexEmptyJSONLFile verifies that a 0-byte JSONL file does not crash
// and indexes no sessions.
func TestReindexEmptyJSONLFile(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(projectRoot, "empty.jsonl")
	if err := os.WriteFile(logPath, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	report, err := idx.Reindex(context.Background(), []string{projectRoot}, false)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := report.FilesIndexed, 1; got != want {
		t.Fatalf("FilesIndexed = %d, want %d", got, want)
	}

	if got, want := sessionCount(t, dbPath), 0; got != want {
		t.Fatalf("session count = %d, want %d", got, want)
	}
}

// TestReindexSessionWithEmptySessionID verifies that a JSONL line with
// sessionId:"" is silently skipped and does not create a session with an
// empty ID.
func TestReindexSessionWithEmptySessionID(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	payload := `{"sessionId":"","cwd":"/tmp","type":"user","message":{"role":"user","content":[{"type":"text","text":"hello"}]},"timestamp":"2026-03-15T10:00:00Z"}
{"sessionId":"valid-session","cwd":"/tmp","type":"user","message":{"role":"user","content":[{"type":"text","text":"hello"}]},"timestamp":"2026-03-15T10:00:01Z"}
`
	logPath := filepath.Join(projectRoot, "empty_id.jsonl")
	if err := os.WriteFile(logPath, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	report, err := idx.Reindex(context.Background(), []string{projectRoot}, false)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := report.BrokenLines, 0; got != want {
		t.Fatalf("BrokenLines = %d, want %d", got, want)
	}

	if got, want := sessionCount(t, dbPath), 1; got != want {
		t.Fatalf("session count = %d, want %d (empty sessionId should be skipped)", got, want)
	}

	db := openDB(t, dbPath)
	defer db.Close()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE session_id = ''`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("found %d sessions with empty session_id, want 0", count)
	}
}

// TestReindexSessionWithNullMessage verifies that lines where message is
// null or missing do not crash and do not increment message counts.
func TestReindexSessionWithNullMessage(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	// First line has a valid message to create the session, then lines with null/missing message.
	payload := `{"sessionId":"sess-null","cwd":"/tmp","type":"user","message":{"role":"user","content":[{"type":"text","text":"hi"}]},"timestamp":"2026-03-15T10:00:00Z"}
{"sessionId":"sess-null","cwd":"/tmp","type":"user","message":null,"timestamp":"2026-03-15T10:00:01Z"}
{"sessionId":"sess-null","cwd":"/tmp","type":"user","timestamp":"2026-03-15T10:00:02Z"}
`
	logPath := filepath.Join(projectRoot, "null_msg.jsonl")
	if err := os.WriteFile(logPath, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	report, err := idx.Reindex(context.Background(), []string{projectRoot}, false)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := report.BrokenLines, 0; got != want {
		t.Fatalf("BrokenLines = %d, want %d", got, want)
	}

	// Only the first line has a valid message; the other two should not count.
	if got, want := sessionMessageCount(t, dbPath, "sess-null"), 1; got != want {
		t.Fatalf("message_count = %d, want %d", got, want)
	}
}

// TestReindexDuplicateSessionAcrossFiles verifies that when the same sessionId
// appears in two JSONL files with different mtimes, the file with the newer mtime
// wins via shouldWriteSession.
func TestReindexDuplicateSessionAcrossFiles(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	sessionLine := func(text string) string {
		return `{"sessionId":"dup-sess","cwd":"/tmp","type":"user","message":{"role":"user","content":[{"type":"text","text":"` + text + `"}]},"timestamp":"2026-03-15T10:00:00Z"}` + "\n"
	}

	// Write file A first (older mtime).
	fileA := filepath.Join(projectRoot, "a_old.jsonl")
	if err := os.WriteFile(fileA, []byte(sessionLine("from-a")), 0o644); err != nil {
		t.Fatal(err)
	}

	// Set an old mtime on file A.
	oldTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(fileA, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	// Write file B with a newer mtime (current time).
	fileB := filepath.Join(projectRoot, "b_new.jsonl")
	if err := os.WriteFile(fileB, []byte(sessionLine("from-b")), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	report, err := idx.Reindex(context.Background(), []string{projectRoot}, false)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := report.FilesIndexed, 2; got != want {
		t.Fatalf("FilesIndexed = %d, want %d", got, want)
	}

	// There should be exactly 1 session (deduplicated).
	if got, want := sessionCount(t, dbPath), 1; got != want {
		t.Fatalf("session count = %d, want %d", got, want)
	}

	// The session should be sourced from b_new.jsonl (the newer file).
	gotPath := sessionSourcePath(t, dbPath, "dup-sess")
	if !strings.HasSuffix(gotPath, "b_new.jsonl") {
		t.Fatalf("source_path = %q, want suffix b_new.jsonl (newer file should win)", gotPath)
	}
}

// TestReindexFullDropsAndRebuilds verifies that a full reindex drops all data
// and rebuilds it correctly with consistent counts.
func TestReindexFullDropsAndRebuilds(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(projectRoot, "session.jsonl")
	if err := os.WriteFile(logPath, []byte(validSessionLog()), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	// Initial index.
	if _, err := idx.Reindex(context.Background(), []string{projectRoot}, false); err != nil {
		t.Fatal(err)
	}

	countBefore := sessionCount(t, dbPath)
	toolsBefore := toolCount(t, dbPath, "session-1", "Bash")
	tokensBefore := modelOutputTokens(t, dbPath, "session-1", "claude-opus-4-6")

	// Full reindex.
	report, err := idx.Reindex(context.Background(), []string{projectRoot}, true)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := report.FilesIndexed, 1; got != want {
		t.Fatalf("FilesIndexed on full reindex = %d, want %d", got, want)
	}

	countAfter := sessionCount(t, dbPath)
	toolsAfter := toolCount(t, dbPath, "session-1", "Bash")
	tokensAfter := modelOutputTokens(t, dbPath, "session-1", "claude-opus-4-6")

	if countBefore != countAfter {
		t.Fatalf("session count changed after full reindex: %d -> %d", countBefore, countAfter)
	}

	if toolsBefore != toolsAfter {
		t.Fatalf("tool count changed after full reindex: %d -> %d", toolsBefore, toolsAfter)
	}

	if tokensBefore != tokensAfter {
		t.Fatalf("output tokens changed after full reindex: %d -> %d", tokensBefore, tokensAfter)
	}
}

// TestReindexWithContentOnlyJSONLLines verifies that decodeBlocks handles
// both string content (plain text) and array content (blocks) formats.
func TestReindexWithContentOnlyJSONLLines(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	// Content is a plain string, not an array of blocks.
	payload := `{"sessionId":"string-content","cwd":"/tmp","type":"user","message":{"role":"user","content":"plain text prompt"},"timestamp":"2026-03-15T10:00:00Z"}
{"sessionId":"string-content","cwd":"/tmp","type":"assistant","message":{"model":"claude-opus-4-6","role":"assistant","usage":{"input_tokens":5,"output_tokens":3,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},"content":[{"type":"text","text":"array response"}]},"timestamp":"2026-03-15T10:00:01Z"}
`
	logPath := filepath.Join(projectRoot, "string_content.jsonl")
	if err := os.WriteFile(logPath, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	report, err := idx.Reindex(context.Background(), []string{projectRoot}, false)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := report.BrokenLines, 0; got != want {
		t.Fatalf("BrokenLines = %d, want %d", got, want)
	}

	if got, want := sessionCount(t, dbPath), 1; got != want {
		t.Fatalf("session count = %d, want %d", got, want)
	}

	// The preview should be the string content from the user message.
	if got, want := sessionPreview(t, dbPath, "string-content"), "plain text prompt"; got != want {
		t.Fatalf("preview = %q, want %q", got, want)
	}

	// FTS should contain both the string content and the array response.
	db := openDB(t, dbPath)
	defer db.Close()

	var content string
	if err := db.QueryRow(
		`SELECT content FROM session_content_fts WHERE session_id = ?`,
		"string-content",
	).Scan(&content); err != nil {
		t.Fatalf("FTS content query failed: %v", err)
	}

	if !strings.Contains(content, "plain text prompt") {
		t.Fatalf("FTS content missing string content: %q", content)
	}

	if !strings.Contains(content, "array response") {
		t.Fatalf("FTS content missing array response: %q", content)
	}
}

// TestReindexCleanPreviewSkipsSystemMessages verifies that system text markers
// (like <system-reminder>) are skipped when extracting preview, and the first
// real user text becomes the preview.
func TestReindexCleanPreviewSkipsSystemMessages(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	// First user message contains system text; second user message has real text.
	payload := `{"sessionId":"preview-test","cwd":"/tmp","type":"user","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>some system instructions</system-reminder>"}]},"timestamp":"2026-03-15T10:00:00Z"}
{"sessionId":"preview-test","cwd":"/tmp","type":"user","message":{"role":"user","content":[{"type":"text","text":"actual user question"}]},"timestamp":"2026-03-15T10:00:01Z"}
`
	logPath := filepath.Join(projectRoot, "preview.jsonl")
	if err := os.WriteFile(logPath, []byte(payload), 0o644); err != nil {
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

	got := sessionPreview(t, dbPath, "preview-test")
	if got != "actual user question" {
		t.Fatalf("preview = %q, want %q (system text should be skipped)", got, "actual user question")
	}
}

// TestReindexCountsLocalCommandSkills verifies that local_command system
// records (manually triggered skills like /commit) are counted in session_tools.
func TestReindexCountsLocalCommandSkills(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	payload := validSessionLog() +
		`{"type":"system","subtype":"local_command","sessionId":"session-1","content":"<command-name>/commit</command-name>\n<command-message>commit</command-message>\n<command-args></command-args>Expanded skill content here...","timestamp":"2026-03-15T10:00:05Z"}` + "\n"

	logPath := filepath.Join(projectRoot, "session.jsonl")
	if err := os.WriteFile(logPath, []byte(payload), 0o644); err != nil {
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

	if got, want := toolCount(t, dbPath, "session-1", "/commit"), 1; got != want {
		t.Fatalf("/commit tool count = %d, want %d", got, want)
	}

	// ToolCallCount should NOT be incremented by local_command records.
	db := openDB(t, dbPath)
	defer db.Close()

	var toolCallCount int
	if err := db.QueryRow(
		`SELECT tool_call_count FROM sessions WHERE session_id = ?`,
		"session-1",
	).Scan(&toolCallCount); err != nil {
		t.Fatal(err)
	}

	// validSessionLog has 1 Bash tool_use; local_command should not add to tool_call_count.
	if got, want := toolCallCount, 1; got != want {
		t.Fatalf("tool_call_count = %d, want %d (local_command should not increment)", got, want)
	}
}

// TestReindexContextCancellation verifies that passing a cancelled context
// to Reindex returns ctx.Err() promptly without processing all files.
func TestReindexContextCancellation(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create multiple JSONL files so there's work to skip.
	for i := 0; i < 10; i++ {
		logPath := filepath.Join(projectRoot, fmt.Sprintf("session_%d.jsonl", i))
		payload := fmt.Sprintf(`{"sessionId":"sess-%d","cwd":"/tmp","type":"user","message":{"role":"user","content":[{"type":"text","text":"hello"}]},"timestamp":"2026-03-15T10:00:00Z"}`+"\n", i)
		if err := os.WriteFile(logPath, []byte(payload), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err = idx.Reindex(ctx, []string{projectRoot}, false)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
