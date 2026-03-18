package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	stdhttp "net/http"
	"strconv"
	"strings"
	"text/template"

	"github.com/Sixeight/kiroku/internal/index"
	"github.com/Sixeight/kiroku/internal/store"
	"github.com/Sixeight/kiroku/internal/ui"
)

type Options struct {
	Store        *store.SQLiteStore
	StatsPath    string
	ConfigRoot   string
	ProjectRoots []string
	OnReindex    func(context.Context, bool) (index.Report, error)
	IsIndexing   func() bool
}

type handler struct {
	store        *store.SQLiteStore
	statsPath    string
	configRoot   string
	projectRoots []string
	onReindex    func(context.Context, bool) (index.Report, error)
	isIndexing   func() bool
	indexTmpl    *template.Template
}

type rootView struct {
	Summary store.Summary
}

func NewHandler(opts Options) (stdhttp.Handler, error) {
	if opts.Store == nil {
		return nil, errors.New("store is required")
	}

	indexTmpl, err := template.ParseFS(ui.Files, "templates/index.html")
	if err != nil {
		return nil, err
	}

	h := &handler{
		store:        opts.Store,
		statsPath:    opts.StatsPath,
		configRoot:   opts.ConfigRoot,
		projectRoots: append([]string(nil), opts.ProjectRoots...),
		onReindex:    opts.OnReindex,
		isIndexing:   opts.IsIndexing,
		indexTmpl:    indexTmpl,
	}

	mux := stdhttp.NewServeMux()
	mux.Handle("/static/", noCacheMiddleware(stdhttp.StripPrefix("/static/", stdhttp.FileServer(stdhttp.FS(ui.StaticFiles())))))
	mux.HandleFunc("/", h.handleIndex)
	mux.HandleFunc("/api/summary", h.handleSummary)
	mux.HandleFunc("/api/sessions", h.handleSessions)
	mux.HandleFunc("/api/sessions/", h.handleSessionDetail)
	mux.HandleFunc("/api/projects/summary", h.handleProjectSummary)
	mux.HandleFunc("/api/stats/daily", h.handleDailyStats)
	mux.HandleFunc("/api/reindex", h.handleReindex)
	mux.HandleFunc("/healthz", h.handleHealthz)

	return mux, nil
}

func (h *handler) handleIndex(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if r.URL.Path != "/" {
		stdhttp.NotFound(w, r)
		return
	}

	summary, err := h.store.LoadSummary(r.Context(), h.statsPath)
	if err != nil {
		writeError(w, err, stdhttp.StatusInternalServerError)
		return
	}

	summary.SourceInfo, err = h.store.SourceInfo(r.Context(), store.SourceInfoParams{
		ConfigRoot:   h.configRoot,
		StatsPath:    h.statsPath,
		ProjectRoots: h.projectRoots,
	})
	if err != nil {
		writeError(w, err, stdhttp.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.indexTmpl.Execute(w, rootView{Summary: summary}); err != nil {
		writeError(w, err, stdhttp.StatusInternalServerError)
	}
}

func (h *handler) handleSummary(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if r.Method != stdhttp.MethodGet {
		writeJSON(w, stdhttp.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	summary, err := h.store.LoadSummary(r.Context(), h.statsPath)
	if err != nil {
		writeError(w, err, stdhttp.StatusInternalServerError)
		return
	}

	summary.SourceInfo, err = h.store.SourceInfo(r.Context(), store.SourceInfoParams{
		ConfigRoot:   h.configRoot,
		StatsPath:    h.statsPath,
		ProjectRoots: h.projectRoots,
	})
	if err != nil {
		writeError(w, err, stdhttp.StatusInternalServerError)
		return
	}

	indexing := false
	if h.isIndexing != nil {
		indexing = h.isIndexing()
	}

	writeJSON(w, stdhttp.StatusOK, map[string]any{
		"totals":             summary.Totals,
		"daily_activity":     summary.DailyActivity,
		"daily_model_tokens": summary.DailyModelTokens,
		"model_usage":        summary.ModelUsage,
		"analytics":          summary.Analytics,
		"top_projects":       summary.TopProjects,
		"top_branches":       summary.TopBranches,
		"recent_sessions":    summary.RecentSessions,
		"source_info":        summary.SourceInfo,
		"indexing":           indexing,
	})
}

func (h *handler) handleSessions(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if r.Method != stdhttp.MethodGet {
		writeJSON(w, stdhttp.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, nextCursor, err := h.store.ListSessions(r.Context(), store.ListSessionsParams{
		Cursor:  r.URL.Query().Get("cursor"),
		Limit:   limit,
		Q:       r.URL.Query().Get("q"),
		Content: r.URL.Query().Get("content"),
		CWD:     r.URL.Query().Get("cwd"),
		Branch:  r.URL.Query().Get("branch"),
		Model:   r.URL.Query().Get("model"),
		Tool:    r.URL.Query().Get("tool"),
		From:    r.URL.Query().Get("from"),
		To:      r.URL.Query().Get("to"),
		Sort:    r.URL.Query().Get("sort"),
		Dir:     r.URL.Query().Get("dir"),
	})
	if err != nil {
		writeError(w, err, stdhttp.StatusInternalServerError)
		return
	}

	writeJSON(w, stdhttp.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": nextCursor,
	})
}

func (h *handler) handleSessionDetail(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if r.Method != stdhttp.MethodGet {
		writeJSON(w, stdhttp.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	remainder := strings.TrimPrefix(r.URL.Path, "/api/sessions/")

	if strings.HasSuffix(remainder, "/messages") {
		sessionID := strings.TrimSuffix(remainder, "/messages")
		if sessionID == "" {
			stdhttp.NotFound(w, r)
			return
		}
		h.handleSessionMessages(w, r, sessionID)
		return
	}

	sessionID := remainder
	if sessionID == "" || strings.Contains(sessionID, "/") {
		stdhttp.NotFound(w, r)
		return
	}

	detail, err := h.store.GetSession(r.Context(), sessionID)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			writeError(w, err, stdhttp.StatusRequestTimeout)
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			stdhttp.NotFound(w, r)
			return
		}
		writeError(w, err, stdhttp.StatusInternalServerError)
		return
	}

	writeJSON(w, stdhttp.StatusOK, detail)
}

func (h *handler) handleSessionMessages(w stdhttp.ResponseWriter, r *stdhttp.Request, sessionID string) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	q := r.URL.Query().Get("q")
	var qArgs []string
	if q != "" {
		qArgs = append(qArgs, q)
	}

	messages, hasMore, err := h.store.ReadSessionMessages(r.Context(), sessionID, limit, offset, qArgs...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			stdhttp.NotFound(w, r)
			return
		}
		writeError(w, err, stdhttp.StatusInternalServerError)
		return
	}

	writeJSON(w, stdhttp.StatusOK, map[string]any{
		"messages": messages,
		"has_more": hasMore,
	})
}

func (h *handler) handleProjectSummary(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if r.Method != stdhttp.MethodGet {
		writeJSON(w, stdhttp.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	cwd := r.URL.Query().Get("cwd")
	if cwd == "" {
		writeJSON(w, stdhttp.StatusBadRequest, map[string]string{"error": "cwd is required"})
		return
	}

	stats, err := h.store.GetProjectStats(r.Context(), cwd)
	if err != nil {
		writeError(w, err, stdhttp.StatusInternalServerError)
		return
	}

	writeJSON(w, stdhttp.StatusOK, stats)
}

func (h *handler) handleDailyStats(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if r.Method != stdhttp.MethodGet {
		writeJSON(w, stdhttp.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	date := r.URL.Query().Get("date")
	if date == "" {
		writeJSON(w, stdhttp.StatusBadRequest, map[string]string{"error": "date is required"})
		return
	}

	stats, err := h.store.GetDailyStats(r.Context(), date)
	if err != nil {
		writeError(w, err, stdhttp.StatusInternalServerError)
		return
	}

	writeJSON(w, stdhttp.StatusOK, stats)
}

func (h *handler) handleReindex(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if r.Method != stdhttp.MethodPost {
		writeJSON(w, stdhttp.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	if h.onReindex == nil {
		writeJSON(w, stdhttp.StatusNotImplemented, map[string]string{"error": "reindex is not configured"})
		return
	}

	report, err := h.onReindex(r.Context(), r.URL.Query().Get("full") == "true")
	if err != nil {
		writeError(w, err, stdhttp.StatusInternalServerError)
		return
	}

	writeJSON(w, stdhttp.StatusAccepted, report)
}

func (h *handler) handleHealthz(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	writeJSON(w, stdhttp.StatusOK, map[string]string{"status": "ok"})
}

func writeError(w stdhttp.ResponseWriter, err error, status int) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeJSON(w stdhttp.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func noCacheMiddleware(next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		next.ServeHTTP(w, r)
	})
}
