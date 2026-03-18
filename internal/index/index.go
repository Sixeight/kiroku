package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Sixeight/kiroku/internal/jsonlutil"
	"github.com/Sixeight/kiroku/internal/sqliteutil"
	_ "modernc.org/sqlite"
)

type Indexer struct {
	db *sql.DB
}

type Report struct {
	FilesIndexed int `json:"files_indexed"`
	BrokenLines  int `json:"broken_lines"`
}

type fileState struct {
	Path  string
	Size  int64
	MTime int64
}

type sessionAggregate struct {
	SessionID             string
	CWD                   string
	GitBranch             string
	StartedAt             string
	EndedAt               string
	MessageCount          int
	ToolCallCount         int
	AssistantMessageCount int
	UserMessageCount      int
	SubagentCount         int
	Preview               string
	SourcePath            string
	SourceMTime           int64
	ToolCounts            map[string]int
	ModelUsage            map[string]modelAggregate
	PRLinks               []prLink
	seenSubagents         map[string]struct{}
	seenPRs               map[int]struct{}
	textParts             []string
}

type prLink struct {
	PRNumber     int
	PRUrl        string
	PRRepository string
}

type modelAggregate struct {
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
}

type transcriptRecord struct {
	Type          string          `json:"type"`
	Subtype       string          `json:"subtype"`
	SessionID     string          `json:"sessionId"`
	CWD           string          `json:"cwd"`
	GitBranch     string          `json:"gitBranch"`
	IsSidechain   bool            `json:"isSidechain"`
	AgentID       string          `json:"agentId"`
	Timestamp     string          `json:"timestamp"`
	SystemContent string          `json:"content"`
	Message       *messagePayload `json:"message"`
	PRNumber      int             `json:"prNumber"`
	PRUrl         string          `json:"prUrl"`
	PRRepository  string          `json:"prRepository"`
}

type messagePayload struct {
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   usagePayload    `json:"usage"`
}

type usagePayload struct {
	InputTokens          int `json:"input_tokens"`
	OutputTokens         int `json:"output_tokens"`
	CacheReadInputTokens int `json:"cache_read_input_tokens"`
	CacheCreationTokens  int `json:"cache_creation_input_tokens"`
}

type contentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

func Open(path string) (*Indexer, error) {
	db, err := sqliteutil.Open(path)
	if err != nil {
		return nil, err
	}

	indexer := &Indexer{db: db}
	if err := indexer.initSchema(); err != nil {
		db.Close()
		return nil, err
	}

	return indexer, nil
}

func (i *Indexer) Close() error {
	if i == nil || i.db == nil {
		return nil
	}

	return i.db.Close()
}

func (i *Indexer) Reindex(ctx context.Context, roots []string, full bool) (Report, error) {
	if full {
		if _, err := i.db.ExecContext(ctx, `DELETE FROM session_models; DELETE FROM session_tools; DELETE FROM session_prs; DELETE FROM session_content_fts; DELETE FROM sessions; DELETE FROM files;`); err != nil {
			return Report{}, err
		}
	}

	files, err := collectJSONLFiles(roots)
	if err != nil {
		return Report{}, err
	}

	var report Report

	for _, state := range files {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}

		shouldIndex, err := i.shouldIndexFile(ctx, state, full)
		if err != nil {
			return report, err
		}
		if !shouldIndex {
			continue
		}

		sessionRows, brokenLines, err := parseFile(state)
		if err != nil {
			return report, err
		}

		if err := i.replaceFileSessions(ctx, state, sessionRows); err != nil {
			return report, err
		}

		report.FilesIndexed++
		report.BrokenLines += brokenLines
	}

	return report, nil
}

func (i *Indexer) initSchema() error {
	const schema = `
CREATE TABLE IF NOT EXISTS sessions(
  session_id TEXT PRIMARY KEY,
  cwd TEXT,
  git_branch TEXT,
  started_at TEXT,
  ended_at TEXT,
  message_count INTEGER,
  tool_call_count INTEGER,
  assistant_message_count INTEGER,
  user_message_count INTEGER,
  subagent_count INTEGER,
  preview TEXT,
  source_path TEXT,
  source_mtime INTEGER
);

CREATE TABLE IF NOT EXISTS session_tools(
  session_id TEXT,
  tool_name TEXT,
  count INTEGER,
  PRIMARY KEY(session_id, tool_name)
);

CREATE TABLE IF NOT EXISTS session_models(
  session_id TEXT,
  model TEXT,
  input_tokens INTEGER,
  output_tokens INTEGER,
  cache_read_tokens INTEGER,
  cache_write_tokens INTEGER,
  PRIMARY KEY(session_id, model)
);

CREATE TABLE IF NOT EXISTS session_prs(
  session_id TEXT,
  pr_number INTEGER,
  pr_url TEXT,
  pr_repository TEXT,
  PRIMARY KEY(session_id, pr_number)
);

CREATE TABLE IF NOT EXISTS files(
  path TEXT PRIMARY KEY,
  size INTEGER,
  mtime INTEGER,
  last_indexed_at TEXT
);

CREATE VIRTUAL TABLE IF NOT EXISTS session_content_fts USING fts5(
  session_id UNINDEXED,
  content
);
`

	_, err := i.db.Exec(schema)
	return err
}

func collectJSONLFiles(roots []string) ([]fileState, error) {
	seen := map[string]struct{}{}
	files := make([]fileState, 0, 64)

	for _, root := range roots {
		if root == "" {
			continue
		}

		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}

		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if d.IsDir() {
				return nil
			}

			if filepath.Ext(path) != ".jsonl" {
				return nil
			}

			info, err := d.Info()
			if err != nil {
				return err
			}

			files = append(files, fileState{
				Path:  path,
				Size:  info.Size(),
				MTime: info.ModTime().UnixNano(),
			})

			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}

	sort.Slice(files, func(a, b int) bool {
		return files[a].Path < files[b].Path
	})

	return files, nil
}

func (i *Indexer) shouldIndexFile(ctx context.Context, state fileState, full bool) (bool, error) {
	if full {
		return true, nil
	}

	var size int64
	var mtime int64
	err := i.db.QueryRowContext(
		ctx,
		`SELECT size, mtime FROM files WHERE path = ?`,
		state.Path,
	).Scan(&size, &mtime)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}

	return size != state.Size || mtime != state.MTime, nil
}

func parseFile(state fileState) ([]sessionAggregate, int, error) {
	sessions := map[string]*sessionAggregate{}
	brokenLines := 0

	err := jsonlutil.ForEachLine(state.Path, func(line []byte) error {
		var record transcriptRecord
		if unmarshalErr := json.Unmarshal(line, &record); unmarshalErr != nil {
			brokenLines++
		} else {
			aggregateRecord(sessions, state, record)
		}
		return nil
	})
	if err != nil {
		return nil, brokenLines, err
	}

	rows := make([]sessionAggregate, 0, len(sessions))
	for _, session := range sessions {
		rows = append(rows, *session)
	}

	sort.Slice(rows, func(a, b int) bool {
		return rows[a].SessionID < rows[b].SessionID
	})

	return rows, brokenLines, nil
}

func getOrCreateSession(sessions map[string]*sessionAggregate, state fileState, record transcriptRecord) *sessionAggregate {
	session := sessions[record.SessionID]
	if session == nil {
		session = &sessionAggregate{
			SessionID:     record.SessionID,
			CWD:           record.CWD,
			GitBranch:     record.GitBranch,
			SourcePath:    state.Path,
			SourceMTime:   state.MTime,
			ToolCounts:    map[string]int{},
			ModelUsage:    map[string]modelAggregate{},
			seenSubagents: map[string]struct{}{},
			seenPRs:       map[int]struct{}{},
		}
		sessions[record.SessionID] = session
	}
	return session
}

func aggregateRecord(sessions map[string]*sessionAggregate, state fileState, record transcriptRecord) {
	if record.SessionID == "" {
		return
	}

	if record.Type == "pr-link" && record.PRNumber > 0 {
		session := getOrCreateSession(sessions, state, record)
		if _, ok := session.seenPRs[record.PRNumber]; !ok {
			session.seenPRs[record.PRNumber] = struct{}{}
			session.PRLinks = append(session.PRLinks, prLink{
				PRNumber:     record.PRNumber,
				PRUrl:        record.PRUrl,
				PRRepository: record.PRRepository,
			})
		}
		return
	}

	if record.Type == "system" && record.Subtype == "local_command" && record.SystemContent != "" {
		session := getOrCreateSession(sessions, state, record)
		updateBounds(session, record.Timestamp)
		if m := commandNameRe.FindStringSubmatch(record.SystemContent); len(m) > 1 {
			session.ToolCounts[m[1]]++
		}
		return
	}

	if record.Message == nil {
		return
	}

	session := getOrCreateSession(sessions, state, record)

	if session.CWD == "" {
		session.CWD = record.CWD
	}
	if session.GitBranch == "" {
		session.GitBranch = record.GitBranch
	}

	session.MessageCount++
	updateBounds(session, record.Timestamp)

	if record.IsSidechain {
		key := record.AgentID
		if key == "" {
			key = record.Timestamp
		}
		if _, ok := session.seenSubagents[key]; !ok {
			session.seenSubagents[key] = struct{}{}
			session.SubagentCount++
		}
	}

	switch record.Message.Role {
	case "assistant":
		session.AssistantMessageCount++
	case "user":
		session.UserMessageCount++
	}

	blocks := decodeBlocks(record.Message.Content)
	for _, block := range blocks {
		switch block.Type {
		case "tool_use":
			session.ToolCallCount++
			if block.Name != "" {
				name := block.Name
				if name == "Skill" && len(block.Input) > 0 {
					var skillInput struct {
						Skill string `json:"skill"`
					}
					if json.Unmarshal(block.Input, &skillInput) == nil && skillInput.Skill != "" {
						name = "auto:/" + skillInput.Skill
					}
				}
				if name == "Agent" && len(block.Input) > 0 {
					var agentInput struct {
						SubagentType string `json:"subagent_type"`
						Description  string `json:"description"`
					}
					if json.Unmarshal(block.Input, &agentInput) == nil {
						if agentInput.SubagentType != "" {
							name = "agent:" + agentInput.SubagentType
						} else if agentInput.Description != "" {
							name = "agent:" + agentInput.Description
						}
					}
				}
				session.ToolCounts[name]++
			}
		case "text":
			if block.Text != "" {
				session.textParts = append(session.textParts, block.Text)
			}
			if record.Message.Role == "user" && block.Text != "" {
				if m := commandNameRe.FindStringSubmatch(block.Text); len(m) > 1 {
					session.ToolCounts[m[1]]++
				}
				if session.Preview == "" {
					if p := cleanPreview(block.Text); p != "" {
						session.Preview = p
					}
				}
			}
		}
	}

	if record.Message.Role == "assistant" && record.Message.Model != "" {
		usage := session.ModelUsage[record.Message.Model]
		usage.InputTokens += record.Message.Usage.InputTokens
		usage.OutputTokens += record.Message.Usage.OutputTokens
		usage.CacheReadTokens += record.Message.Usage.CacheReadInputTokens
		usage.CacheWriteTokens += record.Message.Usage.CacheCreationTokens
		session.ModelUsage[record.Message.Model] = usage
	}
}

var commandNameRe = regexp.MustCompile(`<command-name>([^<]+)</command-name>`)
var commandArgsRe = regexp.MustCompile(`<command-args>([^<]*)</command-args>`)

var systemTextMarkers = []string{
	"<system-reminder>",
	"<local-command-caveat>",
	"<bash-input>",
	"<new-diagnostics>",
	"Base directory for this skill:",
	"Caveat: The messages below were generated",
	"[Request interrupted by user]",
	"This skill guides",
	"ARGUMENTS:",
	"Available skills",
	"Plan mode",
	"DO NOT respond to these messages",
}

func cleanPreview(text string) string {
	if m := commandNameRe.FindStringSubmatch(text); len(m) > 1 {
		cmd := m[1]
		if a := commandArgsRe.FindStringSubmatch(text); len(a) > 1 && strings.TrimSpace(a[1]) != "" {
			return cmd + " " + strings.TrimSpace(a[1])
		}
		return cmd
	}

	for _, marker := range systemTextMarkers {
		if strings.Contains(text, marker) {
			return ""
		}
	}

	return text
}

func updateBounds(session *sessionAggregate, timestamp string) {
	if timestamp == "" {
		return
	}

	if session.StartedAt == "" || timestamp < session.StartedAt {
		session.StartedAt = timestamp
	}

	if session.EndedAt == "" || timestamp > session.EndedAt {
		session.EndedAt = timestamp
	}
}

func decodeBlocks(raw json.RawMessage) []contentBlock {
	if len(raw) == 0 {
		return nil
	}

	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		return blocks
	}

	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []contentBlock{{Type: "text", Text: single}}
	}

	return nil
}

func (i *Indexer) replaceFileSessions(ctx context.Context, state fileState, sessions []sessionAggregate) error {
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	existingIDs, err := fileSessionIDs(ctx, tx, state.Path)
	if err != nil {
		return err
	}

	for _, sessionID := range existingIDs {
		if _, err = tx.ExecContext(ctx, `DELETE FROM session_models WHERE session_id = ?`, sessionID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM session_tools WHERE session_id = ?`, sessionID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM session_prs WHERE session_id = ?`, sessionID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM session_content_fts WHERE session_id = ?`, sessionID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE session_id = ?`, sessionID); err != nil {
			return err
		}
	}

	for _, session := range sessions {
		shouldWrite, err := shouldWriteSession(ctx, tx, session)
		if err != nil {
			return err
		}
		if !shouldWrite {
			continue
		}

		if _, err = tx.ExecContext(
			ctx,
			`INSERT INTO sessions(
			  session_id, cwd, git_branch, started_at, ended_at, message_count, tool_call_count,
			  assistant_message_count, user_message_count, subagent_count, preview, source_path, source_mtime
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(session_id) DO UPDATE SET
			  cwd = excluded.cwd,
			  git_branch = excluded.git_branch,
			  started_at = excluded.started_at,
			  ended_at = excluded.ended_at,
			  message_count = excluded.message_count,
			  tool_call_count = excluded.tool_call_count,
			  assistant_message_count = excluded.assistant_message_count,
			  user_message_count = excluded.user_message_count,
			  subagent_count = excluded.subagent_count,
			  preview = excluded.preview,
			  source_path = excluded.source_path,
			  source_mtime = excluded.source_mtime`,
			session.SessionID,
			session.CWD,
			session.GitBranch,
			session.StartedAt,
			session.EndedAt,
			session.MessageCount,
			session.ToolCallCount,
			session.AssistantMessageCount,
			session.UserMessageCount,
			session.SubagentCount,
			session.Preview,
			session.SourcePath,
			session.SourceMTime,
		); err != nil {
			return err
		}

		for toolName, count := range session.ToolCounts {
			if _, err = tx.ExecContext(
				ctx,
				`INSERT INTO session_tools(session_id, tool_name, count) VALUES (?, ?, ?)
				 ON CONFLICT(session_id, tool_name) DO UPDATE SET count = excluded.count`,
				session.SessionID,
				toolName,
				count,
			); err != nil {
				return err
			}
		}

		for model, usage := range session.ModelUsage {
			if _, err = tx.ExecContext(
				ctx,
				`INSERT INTO session_models(
				  session_id, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens
				) VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT(session_id, model) DO UPDATE SET
				  input_tokens = excluded.input_tokens,
				  output_tokens = excluded.output_tokens,
				  cache_read_tokens = excluded.cache_read_tokens,
				  cache_write_tokens = excluded.cache_write_tokens`,
				session.SessionID,
				model,
				usage.InputTokens,
				usage.OutputTokens,
				usage.CacheReadTokens,
				usage.CacheWriteTokens,
			); err != nil {
				return err
			}
		}

		if len(session.textParts) > 0 {
			content := strings.Join(session.textParts, "\n")
			if _, err = tx.ExecContext(
				ctx,
				`INSERT INTO session_content_fts(session_id, content) VALUES (?, ?)`,
				session.SessionID,
				content,
			); err != nil {
				return err
			}
		}

		for _, pr := range session.PRLinks {
			if _, err = tx.ExecContext(
				ctx,
				`INSERT INTO session_prs(session_id, pr_number, pr_url, pr_repository) VALUES (?, ?, ?, ?)
				 ON CONFLICT(session_id, pr_number) DO UPDATE SET
				   pr_url = excluded.pr_url,
				   pr_repository = excluded.pr_repository`,
				session.SessionID,
				pr.PRNumber,
				pr.PRUrl,
				pr.PRRepository,
			); err != nil {
				return err
			}
		}
	}

	if _, err = tx.ExecContext(
		ctx,
		`INSERT INTO files(path, size, mtime, last_indexed_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET
		   size = excluded.size,
		   mtime = excluded.mtime,
		   last_indexed_at = excluded.last_indexed_at`,
		state.Path,
		state.Size,
		state.MTime,
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return err
	}

	return tx.Commit()
}

func fileSessionIDs(ctx context.Context, tx *sql.Tx, path string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT session_id FROM sessions WHERE source_path = ?`, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}

	return ids, rows.Err()
}

func shouldWriteSession(ctx context.Context, tx *sql.Tx, session sessionAggregate) (bool, error) {
	var sourcePath string
	var sourceMTime int64
	err := tx.QueryRowContext(
		ctx,
		`SELECT source_path, source_mtime FROM sessions WHERE session_id = ?`,
		session.SessionID,
	).Scan(&sourcePath, &sourceMTime)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}

	if sourcePath == session.SourcePath {
		return true, nil
	}

	return session.SourceMTime >= sourceMTime, nil
}

func (f fileState) String() string {
	return fmt.Sprintf("%s:%d:%d", f.Path, f.Size, f.MTime)
}
