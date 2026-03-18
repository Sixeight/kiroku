package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	httpserver "github.com/Sixeight/kiroku/internal/http"
	"github.com/Sixeight/kiroku/internal/index"
	"github.com/Sixeight/kiroku/internal/store"
)

func TestServerServesHTMLSummarySessionsAndDetail(t *testing.T) {
	handler := newTestHandler(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want %d", rec.Code, http.StatusOK)
	}

	if body := rec.Body.String(); !strings.Contains(body, "kiroku") {
		t.Fatalf("GET / body does not contain kiroku: %q", body)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/summary", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/summary status = %d, want %d", rec.Code, http.StatusOK)
	}

	var summary store.Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}

	if got, want := summary.Totals.TotalSessions, 2; got != want {
		t.Fatalf("summary total sessions = %d, want %d", got, want)
	}

	if got, want := summary.SourceInfo.ConfigRoot, "/tmp/claude-config"; got != want {
		t.Fatalf("summary config root = %q, want %q", got, want)
	}

	if got, want := len(summary.SourceInfo.ProjectRoots), 1; got != want {
		t.Fatalf("summary project roots len = %d, want %d", got, want)
	}

	if got, want := summary.SourceInfo.IndexedSessions, 2; got != want {
		t.Fatalf("summary indexed sessions = %d, want %d", got, want)
	}

	if got, want := summary.Analytics.TopTools[0].Name, "Bash"; got != want {
		t.Fatalf("summary top tool = %q, want %q", got, want)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/sessions?branch=feature/search", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/sessions status = %d, want %d", rec.Code, http.StatusOK)
	}

	var sessions struct {
		Items      []store.RecentSessionSummary `json:"items"`
		NextCursor string                       `json:"next_cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sessions); err != nil {
		t.Fatal(err)
	}

	if got, want := len(sessions.Items), 1; got != want {
		t.Fatalf("sessions len = %d, want %d", got, want)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/sessions/session-1", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/sessions/session-1 status = %d, want %d", rec.Code, http.StatusOK)
	}

	var detail store.SessionDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}

	if got, want := detail.Meta.SessionID, "session-1"; got != want {
		t.Fatalf("detail session id = %q, want %q", got, want)
	}
}

func TestServerTriggersReindexAndHealthz(t *testing.T) {
	var calls atomic.Int64

	handler := newTestHandler(t, func(ctx context.Context, full bool) (index.Report, error) {
		calls.Add(1)
		if full {
			t.Fatalf("full = true, want false")
		}
		return index.Report{FilesIndexed: 2, BrokenLines: 1}, nil
	})

	req := httptest.NewRequest(http.MethodPost, "/api/reindex", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST /api/reindex status = %d, want %d", rec.Code, http.StatusAccepted)
	}

	if got, want := calls.Load(), int64(1); got != want {
		t.Fatalf("reindex calls = %d, want %d", got, want)
	}

	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func newTestHandler(t *testing.T, reindex func(context.Context, bool) (index.Report, error)) http.Handler {
	t.Helper()

	root := t.TempDir()
	dbPath := filepath.Join(root, "index.sqlite")
	projectRoot := filepath.Join(root, "projects")
	statsPath := filepath.Join(root, "stats-cache.json")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	writeServerLog(t, filepath.Join(projectRoot, "one.jsonl"), serverSessionOneLog())
	writeServerLog(t, filepath.Join(projectRoot, "two.jsonl"), serverSessionTwoLog())

	if err := os.WriteFile(statsPath, []byte(`{"dailyActivity":[],"dailyModelTokens":[],"modelUsage":{},"totalSessions":2,"totalMessages":8}`), 0o644); err != nil {
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
	t.Cleanup(func() {
		_ = reader.Close()
	})

	handler, err := httpserver.NewHandler(httpserver.Options{
		Store:        reader,
		StatsPath:    statsPath,
		ConfigRoot:   "/tmp/claude-config",
		ProjectRoots: []string{projectRoot},
		OnReindex:    reindex,
	})
	if err != nil {
		t.Fatal(err)
	}

	return handler
}

func writeServerLog(t *testing.T, path, payload string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
}

func serverSessionOneLog() string {
	return `{"sessionId":"session-1","cwd":"/tmp/project-core","gitBranch":"main","type":"user","message":{"role":"user","content":[{"type":"text","text":"first prompt"}]},"timestamp":"2026-03-15T10:00:00Z"}
{"sessionId":"session-1","cwd":"/tmp/project-core","gitBranch":"main","type":"assistant","message":{"model":"claude-opus-4-6","role":"assistant","usage":{"input_tokens":12,"output_tokens":9,"cache_read_input_tokens":5,"cache_creation_input_tokens":2},"content":[{"type":"thinking","thinking":"..."},{"type":"tool_use","name":"Bash","id":"tool-1","input":{"command":"pwd"}},{"type":"text","text":"done"}]},"timestamp":"2026-03-15T10:00:01Z"}
{"sessionId":"session-1","cwd":"/tmp/project-core","gitBranch":"main","type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":"ok"}]},"timestamp":"2026-03-15T10:00:02Z"}
`
}

func serverSessionTwoLog() string {
	return `{"sessionId":"session-2","cwd":"/tmp/project-search","gitBranch":"feature/search","type":"user","message":{"role":"user","content":[{"type":"text","text":"search bug"}]},"timestamp":"2026-03-15T11:00:00Z"}
{"sessionId":"session-2","cwd":"/tmp/project-search","gitBranch":"feature/search","type":"assistant","message":{"model":"claude-sonnet-4-6","role":"assistant","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":3,"cache_creation_input_tokens":1},"content":[{"type":"tool_use","name":"Read","id":"tool-2","input":{"file_path":"a.go"}},{"type":"text","text":"search fixed"}]},"timestamp":"2026-03-15T11:00:01Z"}
`
}

func TestServerDailyStatsEndpoint(t *testing.T) {
	handler := newTestHandler(t, nil)

	// Missing date parameter → 400
	req := httptest.NewRequest(http.MethodGet, "/api/stats/daily", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /api/stats/daily (no date) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	// Valid date
	req = httptest.NewRequest(http.MethodGet, "/api/stats/daily?date=2026-03-15", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/stats/daily?date=2026-03-15 status = %d, want %d", rec.Code, http.StatusOK)
	}

	var stats store.DailyStats
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}

	if got, want := stats.Date, "2026-03-15"; got != want {
		t.Fatalf("date = %q, want %q", got, want)
	}

	if got, want := stats.TotalSessions, 2; got != want {
		t.Fatalf("total sessions = %d, want %d", got, want)
	}

	if len(stats.Sessions) != 2 {
		t.Fatalf("sessions len = %d, want 2", len(stats.Sessions))
	}

	// Wrong method → 405
	req = httptest.NewRequest(http.MethodPost, "/api/stats/daily?date=2026-03-15", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/stats/daily status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestServerReturns404ForUnknownPaths(t *testing.T) {
	handler := newTestHandler(t, nil)

	for _, path := range []string{"/unknown", "/api/unknown"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want %d", path, rec.Code, http.StatusNotFound)
		}
	}
}

func TestServerReturnsMethodNotAllowedForWrongMethods(t *testing.T) {
	handler := newTestHandler(t, func(ctx context.Context, full bool) (index.Report, error) {
		return index.Report{}, nil
	})

	tests := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/summary"},
		{http.MethodPost, "/api/sessions"},
		{http.MethodGet, "/api/reindex"},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(tt.method, tt.path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s status = %d, want %d", tt.method, tt.path, rec.Code, http.StatusMethodNotAllowed)
		}

		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Errorf("%s %s: failed to parse JSON body: %v", tt.method, tt.path, err)
			continue
		}
		if body["error"] != "method not allowed" {
			t.Errorf("%s %s error = %q, want %q", tt.method, tt.path, body["error"], "method not allowed")
		}
	}
}

func TestServerReturnsEmptySessionIDAs404(t *testing.T) {
	handler := newTestHandler(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/sessions/ status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestServerSessionDetailReturns404ForNonexistent(t *testing.T) {
	handler := newTestHandler(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/nonexistent-id", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/sessions/nonexistent-id status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestServerSessionMessagesEndpoint(t *testing.T) {
	handler := newTestHandler(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/session-1/messages", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/sessions/session-1/messages status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result struct {
		Messages []json.RawMessage `json:"messages"`
		HasMore  bool              `json:"has_more"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message, got 0")
	}
}

func TestServerSessionMessagesNonexistent(t *testing.T) {
	handler := newTestHandler(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/nonexistent/messages", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/sessions/nonexistent/messages status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestServerProjectSummaryRequiresCWD(t *testing.T) {
	handler := newTestHandler(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/summary", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /api/projects/summary (no cwd) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if body["error"] != "cwd is required" {
		t.Fatalf("error = %q, want %q", body["error"], "cwd is required")
	}
}

func TestServerReindexWithoutOnReindex(t *testing.T) {
	handler := newTestHandler(t, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/reindex", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("POST /api/reindex (no handler) status = %d, want %d", rec.Code, http.StatusNotImplemented)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if body["error"] != "reindex is not configured" {
		t.Fatalf("error = %q, want %q", body["error"], "reindex is not configured")
	}
}

func TestServerReindexFullParam(t *testing.T) {
	var receivedFull atomic.Bool

	handler := newTestHandler(t, func(ctx context.Context, full bool) (index.Report, error) {
		receivedFull.Store(full)
		return index.Report{FilesIndexed: 1}, nil
	})

	req := httptest.NewRequest(http.MethodPost, "/api/reindex?full=true", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST /api/reindex?full=true status = %d, want %d", rec.Code, http.StatusAccepted)
	}

	if !receivedFull.Load() {
		t.Fatal("full parameter was not passed through as true")
	}
}

func TestServerNoCacheHeaderOnStaticFiles(t *testing.T) {
	handler := newTestHandler(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/static/app.css", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/app.css status = %d, want %d", rec.Code, http.StatusOK)
	}

	cc := rec.Header().Get("Cache-Control")
	if !strings.Contains(cc, "no-cache") {
		t.Fatalf("Cache-Control = %q, want it to contain %q", cc, "no-cache")
	}
}
