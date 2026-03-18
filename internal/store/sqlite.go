package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Sixeight/kiroku/internal/jsonlutil"
	"github.com/Sixeight/kiroku/internal/sqliteutil"
	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db     *sql.DB
	mapper *CWDMapper
}

func (s *SQLiteStore) SetCWDMapper(m *CWDMapper) {
	s.mapper = m
}

type SourceInfoParams struct {
	ConfigRoot   string
	StatsPath    string
	ProjectRoots []string
}

type ListSessionsParams struct {
	Cursor  string
	Limit   int
	Q       string
	Content string
	CWD     string
	Branch  string
	Model   string
	Tool    string
	From    string
	To      string
	Sort    string
	Dir     string
}

type SessionDetail struct {
	Meta           SessionMeta    `json:"meta"`
	Models         []SessionModel `json:"models"`
	Tools          []SessionTool  `json:"tools"`
	Hooks          []SessionHook  `json:"hooks"`
	PRs            []SessionPR    `json:"prs"`
	TimelineCounts TimelineCounts `json:"timeline_counts"`
}

type SessionHook struct {
	HookEvent string `json:"hook_event"`
	Command   string `json:"command"`
	Count     int    `json:"count"`
}

type SessionModel struct {
	Model            string `json:"model"`
	InputTokens      int    `json:"input_tokens"`
	OutputTokens     int    `json:"output_tokens"`
	CacheReadTokens  int    `json:"cache_read_tokens"`
	CacheWriteTokens int    `json:"cache_write_tokens"`
}

type SessionTool struct {
	ToolName string `json:"tool_name"`
	Count    int    `json:"count"`
}

type TimelineCounts struct {
	User       int `json:"user"`
	Assistant  int `json:"assistant"`
	ToolUse    int `json:"tool_use"`
	ToolResult int `json:"tool_result"`
	Thinking   int `json:"thinking"`
}

type transcriptDetailRecord struct {
	SessionID string `json:"sessionId"`
	Message   *struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type transcriptDetailBlock struct {
	Type string `json:"type"`
}

func OpenSQLite(path string) (*SQLiteStore, error) {
	db, err := sqliteutil.Open(path)
	if err != nil {
		return nil, err
	}

	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}

	return s.db.Close()
}

func (s *SQLiteStore) LoadSummary(ctx context.Context, statsPath string) (Summary, error) {
	summary, err := LoadSummaryFromStatsCache(statsPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Summary{}, err
	}
	if errors.Is(err, os.ErrNotExist) {
		summary, err = s.summaryFromDB(ctx)
		if err != nil {
			return Summary{}, err
		}
	}

	dbActivity, err := s.dailyActivityFromDB(ctx)
	if err != nil {
		return Summary{}, err
	}
	summary.DailyActivity = mergeDailyActivity(summary.DailyActivity, dbActivity)

	summary.TopProjects, err = s.topProjects(ctx, 100)
	if err != nil {
		return Summary{}, err
	}

	summary.TopBranches, err = s.topBranches(ctx, 5)
	if err != nil {
		return Summary{}, err
	}

	summary.RecentSessions, _, err = s.ListSessions(ctx, ListSessionsParams{
		Limit: 10,
		Sort:  "recent",
	})
	if err != nil {
		return Summary{}, err
	}

	summary.Analytics, err = s.analytics(ctx, summary.ModelUsage, summary.Totals.TotalSessions)
	if err != nil {
		return Summary{}, err
	}

	return summary, nil
}

func (s *SQLiteStore) analytics(ctx context.Context, modelUsage map[string]ModelUsage, totalSessions int) (Analytics, error) {
	var analytics Analytics

	if err := s.db.QueryRowContext(
		ctx,
		`SELECT
		   COALESCE(SUM(tool_call_count), 0),
		   COALESCE(SUM(subagent_count), 0),
		   COALESCE(AVG(message_count), 0)
		 FROM sessions`,
	).Scan(&analytics.TotalToolCalls, &analytics.TotalSubagents, &analytics.AverageMessages); err != nil {
		return Analytics{}, err
	}

	if err := s.db.QueryRowContext(
		ctx,
		`SELECT COUNT(DISTINCT tool_name) FROM session_tools`,
	).Scan(&analytics.UniqueTools); err != nil {
		return Analytics{}, err
	}

	if err := s.db.QueryRowContext(
		ctx,
		`SELECT COUNT(DISTINCT model) FROM session_models`,
	).Scan(&analytics.UniqueModels); err != nil {
		return Analytics{}, err
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT tool_name, COALESCE(SUM(count), 0), COUNT(*)
		   FROM session_tools
		  GROUP BY tool_name
		  ORDER BY SUM(count) DESC, tool_name ASC
		  LIMIT 8`,
	)
	if err != nil {
		return Analytics{}, err
	}
	defer rows.Close()

	for rows.Next() {
		var item ToolAggregate
		if err := rows.Scan(&item.Name, &item.Count, &item.SessionCount); err != nil {
			return Analytics{}, err
		}
		analytics.TopTools = append(analytics.TopTools, item)
	}
	if err := rows.Err(); err != nil {
		return Analytics{}, err
	}

	modelRows, err := s.db.QueryContext(
		ctx,
		`SELECT model, COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0),
		        COALESCE(SUM(cache_read_tokens), 0), COALESCE(SUM(cache_write_tokens), 0)
		   FROM session_models
		  GROUP BY model
		  ORDER BY SUM(output_tokens) DESC, model ASC
		  LIMIT 8`,
	)
	if err != nil {
		return Analytics{}, err
	}
	defer modelRows.Close()

	for modelRows.Next() {
		var item ModelAggregate
		if err := modelRows.Scan(
			&item.Name,
			&item.SessionCount,
			&item.InputTokens,
			&item.OutputTokens,
			&item.CacheReadTokens,
			&item.CacheWriteTokens,
		); err != nil {
			return Analytics{}, err
		}
		analytics.TopModels = append(analytics.TopModels, item)
	}
	if err := modelRows.Err(); err != nil {
		return Analytics{}, err
	}

	hookRows, err := s.db.QueryContext(
		ctx,
		`SELECT hook_event, command, COALESCE(SUM(count), 0), COUNT(*)
		   FROM session_hooks
		  GROUP BY hook_event, command
		  ORDER BY SUM(count) DESC, hook_event ASC`,
	)
	if err != nil {
		return Analytics{}, err
	}
	defer hookRows.Close()

	for hookRows.Next() {
		var item HookAggregate
		if err := hookRows.Scan(&item.Event, &item.Command, &item.Count, &item.SessionCount); err != nil {
			return Analytics{}, err
		}
		analytics.TopHooks = append(analytics.TopHooks, item)
	}
	if err := hookRows.Err(); err != nil {
		return Analytics{}, err
	}

	analytics.LongestSessions, err = s.sessionInsights(ctx, "(julianday(ended_at) - julianday(started_at)) DESC, session_id ASC", 6)
	if err != nil {
		return Analytics{}, err
	}

	analytics.BusiestSessions, err = s.sessionInsights(ctx, "tool_call_count DESC, message_count DESC, session_id ASC", 6)
	if err != nil {
		return Analytics{}, err
	}

	if len(modelUsage) > 0 {
		for name, usage := range modelUsage {
			analytics.TotalInputTokens += usage.InputTokens
			analytics.TotalOutputTokens += usage.OutputTokens
			analytics.TotalCacheReadTokens += usage.CacheReadInputTokens
			analytics.TotalCacheWriteTokens += usage.CacheCreationTokens

			if !containsModel(analytics.TopModels, name) {
				analytics.TopModels = append(analytics.TopModels, ModelAggregate{
					Name:             name,
					InputTokens:      usage.InputTokens,
					OutputTokens:     usage.OutputTokens,
					CacheReadTokens:  usage.CacheReadInputTokens,
					CacheWriteTokens: usage.CacheCreationTokens,
				})
			}
		}
	} else {
		if err := s.db.QueryRowContext(
			ctx,
			`SELECT
			   COALESCE(SUM(input_tokens), 0),
			   COALESCE(SUM(output_tokens), 0),
			   COALESCE(SUM(cache_read_tokens), 0),
			   COALESCE(SUM(cache_write_tokens), 0)
			 FROM session_models`,
		).Scan(
			&analytics.TotalInputTokens,
			&analytics.TotalOutputTokens,
			&analytics.TotalCacheReadTokens,
			&analytics.TotalCacheWriteTokens,
		); err != nil {
			return Analytics{}, err
		}
	}

	if totalSessions > 0 && analytics.AverageMessages == 0 {
		analytics.AverageMessages = float64(analytics.TotalToolCalls) / float64(totalSessions)
	}

	return analytics, nil
}

func containsModel(items []ModelAggregate, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return true
		}
	}

	return false
}

func (s *SQLiteStore) sessionInsights(ctx context.Context, orderBy string, limit int) ([]SessionInsight, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT session_id, cwd, git_branch, started_at, ended_at, message_count, tool_call_count, preview,
		        CAST(COALESCE((julianday(ended_at) - julianday(started_at)) * 86400, 0) AS INTEGER)
		   FROM sessions
		  ORDER BY `+orderBy+`
		  LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []SessionInsight
	for rows.Next() {
		var item SessionInsight
		if err := rows.Scan(
			&item.SessionID,
			&item.CWD,
			&item.GitBranch,
			&item.StartedAt,
			&item.EndedAt,
			&item.MessageCount,
			&item.ToolCallCount,
			&item.Preview,
			&item.DurationSeconds,
		); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

func (s *SQLiteStore) SourceInfo(ctx context.Context, params SourceInfoParams) (SourceInfo, error) {
	info := SourceInfo{
		ConfigRoot:    params.ConfigRoot,
		StatsPath:     params.StatsPath,
		ProjectRoots:  append([]string(nil), params.ProjectRoots...),
		StatsStatus:   "missing",
		LastIndexedAt: "-",
	}

	if params.StatsPath != "" {
		if _, err := os.Stat(params.StatsPath); err == nil {
			info.StatsStatus = "present"
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return SourceInfo{}, err
		}
	}

	if err := s.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*), COALESCE(MAX(last_indexed_at), '-') FROM files`,
	).Scan(&info.IndexedFiles, &info.LastIndexedAt); err != nil {
		return SourceInfo{}, err
	}

	if err := s.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM sessions`,
	).Scan(&info.IndexedSessions); err != nil {
		return SourceInfo{}, err
	}

	return info, nil
}

func (s *SQLiteStore) ListSessions(ctx context.Context, params ListSessionsParams) ([]RecentSessionSummary, string, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	offset := decodeCursor(params.Cursor)
	dir := "DESC"
	if params.Dir == "asc" {
		dir = "ASC"
	}
	sortExpr := "sessions.ended_at " + dir + ", sessions.session_id " + dir
	switch params.Sort {
	case "longest":
		sortExpr = "(julianday(sessions.ended_at) - julianday(sessions.started_at)) " + dir + ", sessions.session_id " + dir
	case "tools":
		sortExpr = "sessions.tool_call_count " + dir + ", sessions.started_at " + dir + ", sessions.session_id " + dir
	}

	args := []any{}
	var filters []string

	if params.Q != "" {
		filters = append(filters, "(preview LIKE ? OR cwd LIKE ? OR git_branch LIKE ?)")
		like := "%" + params.Q + "%"
		args = append(args, like, like, like)
	}
	if params.CWD != "" {
		clause, cwdArgs := s.cwdFilterClause(ctx, params.CWD)
		filters = append(filters, clause)
		args = append(args, cwdArgs...)
	}
	if params.Branch != "" {
		filters = append(filters, "git_branch = ?")
		args = append(args, params.Branch)
	}
	if params.From != "" {
		filters = append(filters, "started_at >= ?")
		args = append(args, params.From)
	}
	if params.To != "" {
		filters = append(filters, "started_at <= ?")
		args = append(args, params.To)
	}
	if params.Model != "" {
		filters = append(filters, "EXISTS (SELECT 1 FROM session_models sm WHERE sm.session_id = sessions.session_id AND sm.model = ?)")
		args = append(args, params.Model)
	}
	if params.Tool != "" {
		filters = append(filters, "EXISTS (SELECT 1 FROM session_tools st WHERE st.session_id = sessions.session_id AND st.tool_name = ?)")
		args = append(args, params.Tool)
	}
	if params.Content != "" {
		ftsQuery := sanitizeFTS5Query(params.Content)
		if ftsQuery != "" {
			filters = append(filters, "sessions.session_id IN (SELECT session_id FROM session_content_fts WHERE session_content_fts MATCH ?)")
			args = append(args, ftsQuery)
		}
	}

	query := `SELECT
	  sessions.session_id, sessions.cwd, sessions.git_branch, sessions.started_at, sessions.ended_at,
	  sessions.message_count, sessions.tool_call_count, sessions.preview,
	  COALESCE(tm.total_input, 0), COALESCE(tm.total_output, 0),
	  COALESCE(tm.total_cache_read, 0), COALESCE(tm.total_cache_write, 0),
	  COALESCE((SELECT model FROM session_models WHERE session_id = sessions.session_id ORDER BY output_tokens DESC LIMIT 1), ''),
	  sessions.source_path
	FROM sessions
	LEFT JOIN (
	  SELECT session_id, SUM(input_tokens) AS total_input, SUM(output_tokens) AS total_output,
	    SUM(cache_read_tokens) AS total_cache_read, SUM(cache_write_tokens) AS total_cache_write
	  FROM session_models GROUP BY session_id
	) tm ON sessions.session_id = tm.session_id`
	if len(filters) > 0 {
		query += " WHERE " + strings.Join(filters, " AND ")
	}
	query += " ORDER BY " + sortExpr + " LIMIT ? OFFSET ?"
	args = append(args, limit+1, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	sessions := make([]RecentSessionSummary, 0, limit)
	for rows.Next() {
		var session RecentSessionSummary
		if err := rows.Scan(
			&session.SessionID,
			&session.CWD,
			&session.GitBranch,
			&session.StartedAt,
			&session.EndedAt,
			&session.MessageCount,
			&session.ToolCallCount,
			&session.Preview,
			&session.InputTokens,
			&session.OutputTokens,
			&session.CacheReadTokens,
			&session.CacheWriteTokens,
			&session.PrimaryModel,
			&session.SourcePath,
		); err != nil {
			return nil, "", err
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	nextCursor := ""
	if len(sessions) > limit {
		sessions = sessions[:limit]
		nextCursor = encodeCursor(offset + limit)
	}

	if err := s.attachPRsToSessions(ctx, sessions); err != nil {
		return nil, "", err
	}

	if s.mapper != nil {
		for i := range sessions {
			sessions[i].CWD = s.mapper.Map(sessions[i].CWD)
		}
	}

	return sessions, nextCursor, nil
}

func (s *SQLiteStore) sessionPRs(ctx context.Context, sessionID string) ([]SessionPR, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT pr_number, pr_url, pr_repository
		 FROM session_prs
		 WHERE session_id = ?
		 ORDER BY pr_number ASC`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var prs []SessionPR
	for rows.Next() {
		var pr SessionPR
		if err := rows.Scan(&pr.PRNumber, &pr.PRUrl, &pr.PRRepository); err != nil {
			return nil, err
		}
		prs = append(prs, pr)
	}
	return prs, rows.Err()
}

func (s *SQLiteStore) attachPRsToSessions(ctx context.Context, sessions []RecentSessionSummary) error {
	if len(sessions) == 0 {
		return nil
	}

	placeholders := make([]string, len(sessions))
	args := make([]any, len(sessions))
	idxMap := map[string]int{}
	for i, sess := range sessions {
		placeholders[i] = "?"
		args[i] = sess.SessionID
		idxMap[sess.SessionID] = i
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT session_id, pr_number, pr_url, pr_repository
		 FROM session_prs
		 WHERE session_id IN (`+strings.Join(placeholders, ",")+`)
		 ORDER BY session_id, pr_number`,
		args...,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var sessionID string
		var pr SessionPR
		if err := rows.Scan(&sessionID, &pr.PRNumber, &pr.PRUrl, &pr.PRRepository); err != nil {
			return err
		}
		if idx, ok := idxMap[sessionID]; ok {
			sessions[idx].PRs = append(sessions[idx].PRs, pr)
		}
	}
	return rows.Err()
}

func (s *SQLiteStore) GetSessionCWD(ctx context.Context, sessionID string) (string, error) {
	var cwd string
	err := s.db.QueryRowContext(ctx, `SELECT cwd FROM sessions WHERE session_id = ?`, sessionID).Scan(&cwd)
	if err != nil {
		return "", err
	}

	return cwd, nil
}

func (s *SQLiteStore) GetSession(ctx context.Context, sessionID string) (SessionDetail, error) {
	var detail SessionDetail
	var sourcePath string

	err := s.db.QueryRowContext(
		ctx,
		`SELECT session_id, cwd, git_branch, started_at, ended_at, message_count, tool_call_count,
		        assistant_message_count, user_message_count, subagent_count,
		        CAST(COALESCE((julianday(ended_at) - julianday(started_at)) * 86400, 0) AS INTEGER),
		        preview, source_path
		 FROM sessions
		 WHERE session_id = ?`,
		sessionID,
	).Scan(
		&detail.Meta.SessionID,
		&detail.Meta.CWD,
		&detail.Meta.GitBranch,
		&detail.Meta.StartedAt,
		&detail.Meta.EndedAt,
		&detail.Meta.MessageCount,
		&detail.Meta.ToolCallCount,
		&detail.Meta.AssistantMessageCount,
		&detail.Meta.UserMessageCount,
		&detail.Meta.SubagentCount,
		&detail.Meta.DurationSeconds,
		&detail.Meta.Preview,
		&detail.Meta.SourcePath,
	)
	if err != nil {
		return SessionDetail{}, err
	}

	sourcePath = detail.Meta.SourcePath

	modelRows, err := s.db.QueryContext(
		ctx,
		`SELECT model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens
		 FROM session_models
		 WHERE session_id = ?
		 ORDER BY output_tokens DESC, model ASC`,
		sessionID,
	)
	if err != nil {
		return SessionDetail{}, err
	}
	defer modelRows.Close()

	for modelRows.Next() {
		var model SessionModel
		if err := modelRows.Scan(
			&model.Model,
			&model.InputTokens,
			&model.OutputTokens,
			&model.CacheReadTokens,
			&model.CacheWriteTokens,
		); err != nil {
			return SessionDetail{}, err
		}
		detail.Models = append(detail.Models, model)
	}
	if err := modelRows.Err(); err != nil {
		return SessionDetail{}, err
	}

	toolRows, err := s.db.QueryContext(
		ctx,
		`SELECT tool_name, count
		 FROM session_tools
		 WHERE session_id = ?
		 ORDER BY count DESC, tool_name ASC`,
		sessionID,
	)
	if err != nil {
		return SessionDetail{}, err
	}
	defer toolRows.Close()

	for toolRows.Next() {
		var tool SessionTool
		if err := toolRows.Scan(&tool.ToolName, &tool.Count); err != nil {
			return SessionDetail{}, err
		}
		detail.Tools = append(detail.Tools, tool)
	}
	if err := toolRows.Err(); err != nil {
		return SessionDetail{}, err
	}

	hookRows, err := s.db.QueryContext(
		ctx,
		`SELECT hook_event, command, count
		 FROM session_hooks
		 WHERE session_id = ?
		 ORDER BY count DESC, hook_event ASC`,
		sessionID,
	)
	if err != nil {
		return SessionDetail{}, err
	}
	defer hookRows.Close()

	for hookRows.Next() {
		var hook SessionHook
		if err := hookRows.Scan(&hook.HookEvent, &hook.Command, &hook.Count); err != nil {
			return SessionDetail{}, err
		}
		detail.Hooks = append(detail.Hooks, hook)
	}
	if err := hookRows.Err(); err != nil {
		return SessionDetail{}, err
	}

	detail.PRs, err = s.sessionPRs(ctx, sessionID)
	if err != nil {
		return SessionDetail{}, err
	}

	detail.TimelineCounts, err = readTimelineCounts(sourcePath, sessionID)
	if err != nil {
		return SessionDetail{}, err
	}

	return detail, nil
}

func (s *SQLiteStore) summaryFromDB(ctx context.Context) (Summary, error) {
	var summary Summary

	if err := s.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*), COALESCE(SUM(message_count), 0) FROM sessions`,
	).Scan(&summary.Totals.TotalSessions, &summary.Totals.TotalMessages); err != nil {
		return Summary{}, err
	}

	var err error
	summary.DailyActivity, err = s.dailyActivityFromDB(ctx)
	if err != nil {
		return Summary{}, err
	}

	return summary, nil
}

func (s *SQLiteStore) dailyActivityFromDB(ctx context.Context) ([]DailyActivity, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT substr(started_at, 1, 10) AS day,
		        COALESCE(SUM(message_count), 0),
		        COUNT(*),
		        COALESCE(SUM(tool_call_count), 0)
		   FROM sessions
		  WHERE started_at <> ''
		  GROUP BY day
		  ORDER BY day ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []DailyActivity
	for rows.Next() {
		var item DailyActivity
		if err := rows.Scan(&item.Date, &item.MessageCount, &item.SessionCount, &item.ToolCallCount); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func mergeDailyActivity(cached, db []DailyActivity) []DailyActivity {
	byDate := make(map[string]DailyActivity, len(cached)+len(db))
	for _, d := range cached {
		byDate[d.Date] = d
	}
	for _, d := range db {
		if existing, ok := byDate[d.Date]; ok {
			if d.SessionCount > existing.SessionCount {
				byDate[d.Date] = d
			}
		} else {
			byDate[d.Date] = d
		}
	}

	result := make([]DailyActivity, 0, len(byDate))
	for _, d := range byDate {
		result = append(result, d)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Date < result[j].Date
	})
	return result
}

func (s *SQLiteStore) topProjects(ctx context.Context, limit int) ([]ProjectSummary, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT cwd, COUNT(*) AS session_count
		   FROM sessions
		  WHERE cwd <> ''
		  GROUP BY cwd
		  ORDER BY session_count DESC, cwd ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	merged := map[string]int{}
	for rows.Next() {
		var cwd string
		var count int
		if err := rows.Scan(&cwd, &count); err != nil {
			return nil, err
		}
		canonical := s.mapper.Map(cwd)
		merged[canonical] += count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	result := make([]ProjectSummary, 0, len(merged))
	for name, count := range merged {
		result = append(result, ProjectSummary{Name: name, SessionCount: count})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].SessionCount != result[j].SessionCount {
			return result[i].SessionCount > result[j].SessionCount
		}
		return result[i].Name < result[j].Name
	})
	if len(result) > limit {
		result = result[:limit]
	}

	return result, nil
}

func (s *SQLiteStore) topBranches(ctx context.Context, limit int) ([]BranchSummary, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT git_branch, COUNT(*) AS session_count
		   FROM sessions
		  WHERE git_branch <> ''
		  GROUP BY git_branch
		  ORDER BY session_count DESC, git_branch ASC
		  LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []BranchSummary
	for rows.Next() {
		var item BranchSummary
		if err := rows.Scan(&item.Name, &item.SessionCount); err != nil {
			return nil, err
		}
		result = append(result, item)
	}

	return result, rows.Err()
}

func readTimelineCounts(path, sessionID string) (TimelineCounts, error) {
	var counts TimelineCounts

	err := jsonlutil.ForEachLine(path, func(line []byte) error {
		var record transcriptDetailRecord
		if json.Unmarshal(line, &record) == nil && record.SessionID == sessionID && record.Message != nil {
			switch record.Message.Role {
			case "user":
				counts.User++
			case "assistant":
				counts.Assistant++
			}

			var blocks []transcriptDetailBlock
			if json.Unmarshal(record.Message.Content, &blocks) == nil {
				for _, block := range blocks {
					switch block.Type {
					case "tool_use":
						counts.ToolUse++
					case "tool_result":
						counts.ToolResult++
					case "thinking":
						counts.Thinking++
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return TimelineCounts{}, err
	}

	return counts, nil
}

type conversationRecord struct {
	Type        string `json:"type"`
	SessionID   string `json:"sessionId"`
	IsSidechain bool   `json:"isSidechain"`
	IsMeta      bool   `json:"isMeta"`
	Timestamp   string `json:"timestamp"`
	Subtype     string `json:"subtype"`
	HookInfos   []struct {
		Command    string `json:"command"`
		DurationMs int    `json:"durationMs"`
	} `json:"hookInfos"`
	SystemContent string `json:"content"`
	Message       *struct {
		Role    string          `json:"role"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

var skillCommandNameRe = regexp.MustCompile(`<command-name>([^<]+)</command-name>`)
var skillCommandArgsRe = regexp.MustCompile(`<command-args>([^<]+)</command-args>`)

type conversationRawBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Name     string          `json:"name"`
	Thinking string          `json:"thinking"`
	Content  json.RawMessage `json:"content"`
	Input    json.RawMessage `json:"input"`
	Source   *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	} `json:"source"`
}

func (s *SQLiteStore) ReadSessionMessages(ctx context.Context, sessionID string, limit, offset int, q ...string) ([]ConversationMessage, bool, error) {
	searchQuery := ""
	if len(q) > 0 {
		searchQuery = strings.ToLower(q[0])
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	var sourcePath string
	err := s.db.QueryRowContext(ctx, `SELECT source_path FROM sessions WHERE session_id = ?`, sessionID).Scan(&sourcePath)
	if err != nil {
		return nil, false, err
	}

	var messages []ConversationMessage
	idx := 0
	hasMore := false

	scanErr := jsonlutil.ForEachLine(sourcePath, func(line []byte) error {
		var record conversationRecord
		if json.Unmarshal(line, &record) != nil {
			return nil
		}

		if record.Type == "system" && record.Subtype == "stop_hook_summary" && len(record.HookInfos) > 0 {
			if idx >= offset {
				var cmds []string
				for _, h := range record.HookInfos {
					cmds = append(cmds, h.Command)
				}
				msg := ConversationMessage{
					Role:      "system",
					Timestamp: record.Timestamp,
					Blocks: []ConversationBlock{{
						Type: "hook",
						Text: strings.Join(cmds, "\n"),
					}},
				}
				if searchQuery == "" || messageMatchesQuery(msg, searchQuery) {
					messages = append(messages, msg)
				}
			}
		}

		if record.Type == "system" && record.Subtype == "local_command" && record.SystemContent != "" {
			if m := skillCommandNameRe.FindStringSubmatch(record.SystemContent); len(m) > 1 {
				if idx >= offset {
					msg := ConversationMessage{
						Role:      "system",
						Timestamp: record.Timestamp,
						Blocks: []ConversationBlock{{
							Type: "skill",
							Text: m[1],
						}},
					}
					if searchQuery == "" || messageMatchesQuery(msg, searchQuery) {
						messages = append(messages, msg)
					}
				}
			}
		}

		if record.SessionID == sessionID && !record.IsSidechain && record.Message != nil {
			// Skip isMeta user messages (expanded skill prompts).
			if record.IsMeta {
				return nil
			}

			if idx >= offset {
				msg := ConversationMessage{
					Role:             record.Message.Role,
					Timestamp:        record.Timestamp,
					Model:            record.Message.Model,
					InputTokens:      record.Message.Usage.InputTokens,
					OutputTokens:     record.Message.Usage.OutputTokens,
					CacheReadTokens:  record.Message.Usage.CacheReadInputTokens,
					CacheWriteTokens: record.Message.Usage.CacheCreationInputTokens,
				}

				var rawBlocks []conversationRawBlock
				if json.Unmarshal(record.Message.Content, &rawBlocks) == nil {
					for _, rb := range rawBlocks {
						block := ConversationBlock{Type: rb.Type}
						switch rb.Type {
						case "text":
							block.Text = rb.Text
						case "tool_use":
							block.Tool = rb.Name
							if len(rb.Input) > 0 {
								var input map[string]any
								if json.Unmarshal(rb.Input, &input) == nil {
									block.Input = input
								}
							}
						case "tool_result":
							block.Content = extractToolResultContent(rb.Content)
						case "thinking":
							block.Text = rb.Thinking
						case "image":
							if rb.Source != nil && rb.Source.Data != "" {
								block.MediaType = rb.Source.MediaType
								block.ImageData = rb.Source.Data
							}
						}
						msg.Blocks = append(msg.Blocks, block)
					}
				} else {
					var plainText string
					if json.Unmarshal(record.Message.Content, &plainText) == nil && plainText != "" {
						if record.Message.Role == "user" {
							if m := skillCommandNameRe.FindStringSubmatch(plainText); len(m) > 1 {
								text := m[1]
								if a := skillCommandArgsRe.FindStringSubmatch(plainText); len(a) > 1 && a[1] != "" {
									text += " " + a[1]
								}
								msg.Blocks = append(msg.Blocks, ConversationBlock{Type: "text", Text: text})
							} else {
								msg.Blocks = append(msg.Blocks, ConversationBlock{Type: "text", Text: plainText})
							}
						} else {
							msg.Blocks = append(msg.Blocks, ConversationBlock{Type: "text", Text: plainText})
						}
					}
				}

				if searchQuery == "" || messageMatchesQuery(msg, searchQuery) {
					messages = append(messages, msg)
					if len(messages) > limit {
						hasMore = true
						messages = messages[:limit]
						return errStopIteration
					}
				}
			}
			idx++
		}

		return nil
	})
	if scanErr != nil && scanErr != errStopIteration {
		return nil, false, scanErr
	}

	return messages, hasMore, nil
}

// errStopIteration is a sentinel error used to break out of ForEachLine early.
var errStopIteration = errors.New("stop iteration")

func sanitizeFTS5Query(raw string) string {
	words := strings.Fields(raw)
	var parts []string
	for _, w := range words {
		cleaned := strings.Map(func(r rune) rune {
			if r == '"' {
				return -1
			}
			return r
		}, w)
		cleaned = strings.TrimSpace(cleaned)
		if cleaned != "" {
			parts = append(parts, `"`+cleaned+`"`)
		}
	}
	return strings.Join(parts, " ")
}

func messageMatchesQuery(msg ConversationMessage, query string) bool {
	for _, b := range msg.Blocks {
		switch b.Type {
		case "text":
			if strings.Contains(strings.ToLower(b.Text), query) {
				return true
			}
		case "tool_result":
			if strings.Contains(strings.ToLower(b.Content), query) {
				return true
			}
		}
	}
	return false
}

func extractToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}

	return string(raw)
}

func (s *SQLiteStore) GetProjectStats(ctx context.Context, cwd string) (ProjectStats, error) {
	stats := ProjectStats{CWD: cwd}

	cwdFilter, cwdArgs := s.cwdFilterClause(ctx, cwd)

	args := append([]any{}, cwdArgs...)
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(message_count), 0), COALESCE(SUM(tool_call_count), 0)
		 FROM sessions WHERE `+cwdFilter, args...,
	).Scan(&stats.TotalSessions, &stats.TotalMessages, &stats.TotalToolCalls)
	if err != nil {
		return ProjectStats{}, err
	}

	args = append([]any{}, cwdArgs...)
	err = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(sm.input_tokens), 0), COALESCE(SUM(sm.output_tokens), 0),
		        COALESCE(SUM(sm.cache_read_tokens), 0), COALESCE(SUM(sm.cache_write_tokens), 0)
		 FROM session_models sm JOIN sessions s ON sm.session_id = s.session_id
		 WHERE s.`+cwdFilter, args...,
	).Scan(&stats.InputTokens, &stats.OutputTokens, &stats.CacheReadTokens, &stats.CacheWriteTokens)
	if err != nil {
		return ProjectStats{}, err
	}

	args = append([]any{}, cwdArgs...)
	toolRows, err := s.db.QueryContext(ctx,
		`SELECT st.tool_name, SUM(st.count) AS total
		 FROM session_tools st JOIN sessions s ON st.session_id = s.session_id
		 WHERE s.`+cwdFilter+` GROUP BY st.tool_name ORDER BY total DESC LIMIT 8`, args...)
	if err != nil {
		return ProjectStats{}, err
	}
	defer toolRows.Close()
	for toolRows.Next() {
		var t ToolAggregate
		if err := toolRows.Scan(&t.Name, &t.Count); err != nil {
			return ProjectStats{}, err
		}
		stats.TopTools = append(stats.TopTools, t)
	}
	if err := toolRows.Err(); err != nil {
		return ProjectStats{}, err
	}

	args = append([]any{}, cwdArgs...)
	modelRows, err := s.db.QueryContext(ctx,
		`SELECT sm.model, SUM(sm.input_tokens), SUM(sm.output_tokens),
		        SUM(sm.cache_read_tokens), SUM(sm.cache_write_tokens), COUNT(DISTINCT sm.session_id)
		 FROM session_models sm JOIN sessions s ON sm.session_id = s.session_id
		 WHERE s.`+cwdFilter+` GROUP BY sm.model ORDER BY SUM(sm.output_tokens) DESC LIMIT 5`, args...)
	if err != nil {
		return ProjectStats{}, err
	}
	defer modelRows.Close()
	for modelRows.Next() {
		var m ModelAggregate
		if err := modelRows.Scan(&m.Name, &m.InputTokens, &m.OutputTokens, &m.CacheReadTokens, &m.CacheWriteTokens, &m.SessionCount); err != nil {
			return ProjectStats{}, err
		}
		stats.TopModels = append(stats.TopModels, m)
	}
	if err := modelRows.Err(); err != nil {
		return ProjectStats{}, err
	}

	args = append([]any{}, cwdArgs...)
	branchRows, err := s.db.QueryContext(ctx,
		`SELECT git_branch, COUNT(*) FROM sessions
		 WHERE `+cwdFilter+` AND git_branch <> '' GROUP BY git_branch ORDER BY COUNT(*) DESC`, args...)
	if err != nil {
		return ProjectStats{}, err
	}
	defer branchRows.Close()
	for branchRows.Next() {
		var b BranchSummary
		if err := branchRows.Scan(&b.Name, &b.SessionCount); err != nil {
			return ProjectStats{}, err
		}
		stats.Branches = append(stats.Branches, b)
	}
	if err := branchRows.Err(); err != nil {
		return ProjectStats{}, err
	}

	return stats, nil
}

func (s *SQLiteStore) GetDailyStats(ctx context.Context, date string) (DailyStats, error) {
	stats := DailyStats{Date: date}

	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(message_count), 0), COALESCE(SUM(tool_call_count), 0)
		 FROM sessions
		 WHERE substr(started_at, 1, 10) = ?`, date,
	).Scan(&stats.TotalSessions, &stats.TotalMessages, &stats.TotalToolCalls)
	if err != nil {
		return DailyStats{}, err
	}

	err = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(sm.input_tokens), 0), COALESCE(SUM(sm.output_tokens), 0),
		        COALESCE(SUM(sm.cache_read_tokens), 0), COALESCE(SUM(sm.cache_write_tokens), 0)
		 FROM session_models sm JOIN sessions s ON sm.session_id = s.session_id
		 WHERE substr(s.started_at, 1, 10) = ?`, date,
	).Scan(&stats.InputTokens, &stats.OutputTokens, &stats.CacheReadTokens, &stats.CacheWriteTokens)
	if err != nil {
		return DailyStats{}, err
	}

	toolRows, err := s.db.QueryContext(ctx,
		`SELECT st.tool_name, SUM(st.count) AS total, COUNT(DISTINCT st.session_id)
		 FROM session_tools st JOIN sessions s ON st.session_id = s.session_id
		 WHERE substr(s.started_at, 1, 10) = ?
		 GROUP BY st.tool_name
		 ORDER BY total DESC, st.tool_name ASC`, date)
	if err != nil {
		return DailyStats{}, err
	}
	defer toolRows.Close()
	for toolRows.Next() {
		var t ToolAggregate
		if err := toolRows.Scan(&t.Name, &t.Count, &t.SessionCount); err != nil {
			return DailyStats{}, err
		}
		stats.TopTools = append(stats.TopTools, t)
	}
	if err := toolRows.Err(); err != nil {
		return DailyStats{}, err
	}

	modelRows, err := s.db.QueryContext(ctx,
		`SELECT sm.model, COUNT(DISTINCT sm.session_id),
		        SUM(sm.input_tokens), SUM(sm.output_tokens),
		        SUM(sm.cache_read_tokens), SUM(sm.cache_write_tokens)
		 FROM session_models sm JOIN sessions s ON sm.session_id = s.session_id
		 WHERE substr(s.started_at, 1, 10) = ?
		 GROUP BY sm.model
		 ORDER BY SUM(sm.output_tokens) DESC, sm.model ASC`, date)
	if err != nil {
		return DailyStats{}, err
	}
	defer modelRows.Close()
	for modelRows.Next() {
		var m ModelAggregate
		if err := modelRows.Scan(&m.Name, &m.SessionCount, &m.InputTokens, &m.OutputTokens, &m.CacheReadTokens, &m.CacheWriteTokens); err != nil {
			return DailyStats{}, err
		}
		stats.TopModels = append(stats.TopModels, m)
	}
	if err := modelRows.Err(); err != nil {
		return DailyStats{}, err
	}

	hookRows, err := s.db.QueryContext(ctx,
		`SELECT sh.hook_event, sh.command, SUM(sh.count) AS total, COUNT(DISTINCT sh.session_id)
		 FROM session_hooks sh JOIN sessions s ON sh.session_id = s.session_id
		 WHERE substr(s.started_at, 1, 10) = ?
		 GROUP BY sh.hook_event, sh.command
		 ORDER BY total DESC, sh.hook_event ASC`, date)
	if err != nil {
		return DailyStats{}, err
	}
	defer hookRows.Close()
	for hookRows.Next() {
		var h HookAggregate
		if err := hookRows.Scan(&h.Event, &h.Command, &h.Count, &h.SessionCount); err != nil {
			return DailyStats{}, err
		}
		stats.TopHooks = append(stats.TopHooks, h)
	}
	if err := hookRows.Err(); err != nil {
		return DailyStats{}, err
	}

	stats.Sessions, _, err = s.ListSessions(ctx, ListSessionsParams{
		Limit: 100,
		From:  date + "T00:00:00Z",
		To:    date + "T23:59:59Z",
		Sort:  "recent",
	})
	if err != nil {
		return DailyStats{}, err
	}

	return stats, nil
}

// cwdFilterClause returns a SQL WHERE clause and args that match sessions
// belonging to the given canonical CWD. When a CWDMapper is set, this
// expands the canonical CWD to include all raw CWDs that map to it.
func (s *SQLiteStore) cwdFilterClause(ctx context.Context, canonical string) (string, []any) {
	if s.mapper == nil {
		return "cwd = ?", []any{canonical}
	}

	cwds := s.expandCWD(ctx, canonical)
	if len(cwds) == 0 {
		return "cwd = ?", []any{canonical}
	}

	placeholders := make([]string, len(cwds))
	args := make([]any, len(cwds))
	for i, c := range cwds {
		placeholders[i] = "?"
		args[i] = c
	}
	return "cwd IN (" + strings.Join(placeholders, ",") + ")", args
}

// expandCWD returns all distinct CWDs in the DB that map to the given canonical CWD.
func (s *SQLiteStore) expandCWD(ctx context.Context, canonical string) []string {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT cwd FROM sessions WHERE cwd <> ''`)
	if err != nil {
		return []string{canonical}
	}
	defer rows.Close()

	var result []string
	for rows.Next() {
		var cwd string
		if err := rows.Scan(&cwd); err != nil {
			continue
		}
		if s.mapper.Map(cwd) == canonical {
			result = append(result, cwd)
		}
	}
	if len(result) == 0 {
		return []string{canonical}
	}
	return result
}

func encodeCursor(offset int) string {
	return base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodeCursor(cursor string) int {
	if cursor == "" {
		return 0
	}

	payload, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return 0
	}

	offset, err := strconv.Atoi(string(payload))
	if err != nil || offset < 0 {
		return 0
	}

	return offset
}

func (p ListSessionsParams) String() string {
	return fmt.Sprintf("%s:%s:%s", p.Branch, p.Model, p.Sort)
}
