package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunDoctorReportsPathsAndCounts(t *testing.T) {
	setupCLIEnv(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := run(context.Background(), &stdout, &stderr, []string{"doctor"}); err != nil {
		t.Fatal(err)
	}

	output := stdout.String()
	if !strings.Contains(output, "stats-cache.json: present") {
		t.Fatalf("doctor output missing stats-cache.json status: %q", output)
	}

	if !strings.Contains(output, "jsonl files: 1") {
		t.Fatalf("doctor output missing jsonl count: %q", output)
	}
}

func TestRunReindexCreatesIndexDB(t *testing.T) {
	paths := setupCLIEnv(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := run(context.Background(), &stdout, &stderr, []string{"reindex", "--full"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(paths.cacheHome, "kiroku", "index.sqlite")); err != nil {
		t.Fatalf("index sqlite stat error: %v", err)
	}

	if !strings.Contains(stdout.String(), "files_indexed=1") {
		t.Fatalf("reindex output missing files_indexed: %q", stdout.String())
	}
}

func TestRunOpenStartsServerWithoutBrowser(t *testing.T) {
	setupCLIEnv(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	done := make(chan error, 1)
	go func() {
		done <- run(ctx, &stdout, &stderr, []string{"open", "--no-open", "--port", "0"})
	}()

	address := waitForAddress(t, &stdout)

	response, err := http.Get(address + "/api/summary")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/summary status = %d, want %d", response.StatusCode, http.StatusOK)
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run(open) did not stop after cancel")
	}
}

func TestCountBrokenLinesHandlesLargeJSONLLine(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "large.jsonl")

	payload := map[string]any{
		"sessionId": "session-1",
		"message": map[string]any{
			"role": "user",
			"content": []map[string]any{
				{
					"type": "text",
					"text": strings.Repeat("a", 200_000),
				},
			},
		},
	}

	line, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	brokenLines, err := countBrokenLines(path)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := brokenLines, 0; got != want {
		t.Fatalf("brokenLines = %d, want %d", got, want)
	}
}

func TestRunResumeRequiresSessionID(t *testing.T) {
	setupCLIEnv(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := run(context.Background(), &stdout, &stderr, []string{"resume"})
	if err == nil {
		t.Fatal("expected error for missing session id")
	}

	if !strings.Contains(err.Error(), "session-id") {
		t.Fatalf("error = %q, want mention of session-id", err.Error())
	}
}

func TestRunResumeReportsUnknownSession(t *testing.T) {
	paths := setupCLIEnv(t)

	// Reindex first so the DB exists
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(context.Background(), &stdout, &stderr, []string{"reindex"}); err != nil {
		t.Fatal(err)
	}

	_ = paths

	stdout.Reset()
	stderr.Reset()

	err := run(context.Background(), &stdout, &stderr, []string{"resume", "nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown session")
	}

	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %q, want mention of not found", err.Error())
	}
}

type cliPaths struct {
	home      string
	cacheHome string
}

func setupCLIEnv(t *testing.T) cliPaths {
	t.Helper()

	home := t.TempDir()
	configHome := filepath.Join(home, "config")
	cacheHome := filepath.Join(home, "cache")
	claudeRoot := filepath.Join(configHome, "claude")
	projectRoot := filepath.Join(claudeRoot, "projects")

	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(claudeRoot, "stats-cache.json"), []byte(`{"dailyActivity":[],"dailyModelTokens":[],"modelUsage":{},"totalSessions":1,"totalMessages":3}`), 0o644); err != nil {
		t.Fatal(err)
	}

	payload := `{"sessionId":"session-1","cwd":"/tmp/project-core","gitBranch":"main","type":"user","message":{"role":"user","content":[{"type":"text","text":"first prompt"}]},"timestamp":"2026-03-15T10:00:00Z"}
{"sessionId":"session-1","cwd":"/tmp/project-core","gitBranch":"main","type":"assistant","message":{"model":"claude-opus-4-6","role":"assistant","usage":{"input_tokens":12,"output_tokens":9,"cache_read_input_tokens":5,"cache_creation_input_tokens":2},"content":[{"type":"tool_use","name":"Bash","id":"tool-1","input":{"command":"pwd"}},{"type":"text","text":"done"}]},"timestamp":"2026-03-15T10:00:01Z"}
`

	if err := os.WriteFile(filepath.Join(projectRoot, "session.jsonl"), []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	return cliPaths{
		home:      home,
		cacheHome: cacheHome,
	}
}

func waitForAddress(t *testing.T, stdout *bytes.Buffer) string {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		output := stdout.String()
		for _, line := range strings.Split(output, "\n") {
			if strings.HasPrefix(line, "listening on ") {
				return strings.TrimSpace(strings.TrimPrefix(line, "listening on "))
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("server address not found in stdout: %q", stdout.String())
	return ""
}
