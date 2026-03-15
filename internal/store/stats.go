package store

import (
	"encoding/json"
	"os"
)

type Summary struct {
	Totals           Totals                 `json:"totals"`
	DailyActivity    []DailyActivity        `json:"daily_activity"`
	DailyModelTokens []DailyModelTokens     `json:"daily_model_tokens"`
	ModelUsage       map[string]ModelUsage  `json:"model_usage"`
	Analytics        Analytics              `json:"analytics"`
	TopProjects      []ProjectSummary       `json:"top_projects,omitempty"`
	TopBranches      []BranchSummary        `json:"top_branches,omitempty"`
	RecentSessions   []RecentSessionSummary `json:"recent_sessions,omitempty"`
	SourceInfo       SourceInfo             `json:"source_info"`
}

type Totals struct {
	TotalSessions int `json:"total_sessions"`
	TotalMessages int `json:"total_messages"`
}

type DailyActivity struct {
	Date          string `json:"date"`
	MessageCount  int    `json:"messageCount"`
	SessionCount  int    `json:"sessionCount"`
	ToolCallCount int    `json:"toolCallCount"`
}

type DailyModelTokens struct {
	Date          string         `json:"date"`
	TokensByModel map[string]int `json:"tokensByModel"`
}

type ModelUsage struct {
	InputTokens          int `json:"inputTokens"`
	OutputTokens         int `json:"outputTokens"`
	CacheReadInputTokens int `json:"cacheReadInputTokens"`
	CacheCreationTokens  int `json:"cacheCreationInputTokens"`
}

type ProjectSummary struct {
	Name         string `json:"name"`
	SessionCount int    `json:"session_count"`
}

type BranchSummary struct {
	Name         string `json:"name"`
	SessionCount int    `json:"session_count"`
}

type RecentSessionSummary struct {
	SessionID        string      `json:"session_id"`
	CWD              string      `json:"cwd"`
	GitBranch        string      `json:"git_branch"`
	StartedAt        string      `json:"started_at"`
	EndedAt          string      `json:"ended_at"`
	MessageCount     int         `json:"message_count"`
	ToolCallCount    int         `json:"tool_call_count"`
	Preview          string      `json:"preview"`
	InputTokens      int         `json:"input_tokens"`
	OutputTokens     int         `json:"output_tokens"`
	CacheReadTokens  int         `json:"cache_read_tokens"`
	CacheWriteTokens int         `json:"cache_write_tokens"`
	PrimaryModel     string      `json:"primary_model"`
	PRs              []SessionPR `json:"prs,omitempty"`
}

type SessionMeta struct {
	SessionID             string `json:"session_id"`
	CWD                   string `json:"cwd"`
	GitBranch             string `json:"git_branch"`
	StartedAt             string `json:"started_at"`
	EndedAt               string `json:"ended_at"`
	MessageCount          int    `json:"message_count"`
	ToolCallCount         int    `json:"tool_call_count"`
	AssistantMessageCount int    `json:"assistant_message_count"`
	UserMessageCount      int    `json:"user_message_count"`
	SubagentCount         int    `json:"subagent_count"`
	DurationSeconds       int    `json:"duration_seconds"`
	Preview               string `json:"preview"`
	SourcePath            string `json:"source_path"`
}

type SourceInfo struct {
	ConfigRoot      string   `json:"config_root"`
	StatsPath       string   `json:"stats_path"`
	StatsStatus     string   `json:"stats_status"`
	ProjectRoots    []string `json:"project_roots"`
	IndexedFiles    int      `json:"indexed_files"`
	IndexedSessions int      `json:"indexed_sessions"`
	LastIndexedAt   string   `json:"last_indexed_at"`
}

type Analytics struct {
	TotalInputTokens      int              `json:"total_input_tokens"`
	TotalOutputTokens     int              `json:"total_output_tokens"`
	TotalCacheReadTokens  int              `json:"total_cache_read_tokens"`
	TotalCacheWriteTokens int              `json:"total_cache_write_tokens"`
	TotalToolCalls        int              `json:"total_tool_calls"`
	TotalSubagents        int              `json:"total_subagents"`
	UniqueTools           int              `json:"unique_tools"`
	UniqueModels          int              `json:"unique_models"`
	AverageMessages       float64          `json:"average_messages"`
	TopTools              []ToolAggregate  `json:"top_tools,omitempty"`
	TopModels             []ModelAggregate `json:"top_models,omitempty"`
	LongestSessions       []SessionInsight `json:"longest_sessions,omitempty"`
	BusiestSessions       []SessionInsight `json:"busiest_sessions,omitempty"`
}

type ToolAggregate struct {
	Name         string `json:"name"`
	Count        int    `json:"count"`
	SessionCount int    `json:"session_count"`
}

type ModelAggregate struct {
	Name             string `json:"name"`
	SessionCount     int    `json:"session_count"`
	InputTokens      int    `json:"input_tokens"`
	OutputTokens     int    `json:"output_tokens"`
	CacheReadTokens  int    `json:"cache_read_tokens"`
	CacheWriteTokens int    `json:"cache_write_tokens"`
}

type SessionInsight struct {
	SessionID       string `json:"session_id"`
	CWD             string `json:"cwd"`
	GitBranch       string `json:"git_branch"`
	StartedAt       string `json:"started_at"`
	EndedAt         string `json:"ended_at"`
	MessageCount    int    `json:"message_count"`
	ToolCallCount   int    `json:"tool_call_count"`
	DurationSeconds int    `json:"duration_seconds"`
	Preview         string `json:"preview"`
}

type ProjectStats struct {
	CWD              string           `json:"cwd"`
	TotalSessions    int              `json:"total_sessions"`
	TotalMessages    int              `json:"total_messages"`
	TotalToolCalls   int              `json:"total_tool_calls"`
	InputTokens      int              `json:"input_tokens"`
	OutputTokens     int              `json:"output_tokens"`
	CacheReadTokens  int              `json:"cache_read_tokens"`
	CacheWriteTokens int              `json:"cache_write_tokens"`
	TopTools         []ToolAggregate  `json:"top_tools"`
	TopModels        []ModelAggregate `json:"top_models"`
	Branches         []BranchSummary  `json:"branches"`
}

type ConversationMessage struct {
	Role             string              `json:"role"`
	Timestamp        string              `json:"timestamp"`
	Model            string              `json:"model,omitempty"`
	InputTokens      int                 `json:"input_tokens,omitempty"`
	OutputTokens     int                 `json:"output_tokens,omitempty"`
	CacheReadTokens  int                 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int                 `json:"cache_write_tokens,omitempty"`
	Blocks           []ConversationBlock `json:"blocks"`
}

type ConversationBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	Tool      string         `json:"tool,omitempty"`
	Content   string         `json:"content,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	MediaType string         `json:"media_type,omitempty"`
	ImageData string         `json:"image_data,omitempty"`
}

type statsCache struct {
	DailyActivity    []DailyActivity       `json:"dailyActivity"`
	DailyModelTokens []DailyModelTokens    `json:"dailyModelTokens"`
	ModelUsage       map[string]ModelUsage `json:"modelUsage"`
	TotalSessions    int                   `json:"totalSessions"`
	TotalMessages    int                   `json:"totalMessages"`
}

func LoadSummaryFromStatsCache(path string) (Summary, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return Summary{}, err
	}

	var cache statsCache
	if err := json.Unmarshal(payload, &cache); err != nil {
		return Summary{}, err
	}

	return Summary{
		Totals: Totals{
			TotalSessions: cache.TotalSessions,
			TotalMessages: cache.TotalMessages,
		},
		DailyActivity:    cache.DailyActivity,
		DailyModelTokens: cache.DailyModelTokens,
		ModelUsage:       cache.ModelUsage,
	}, nil
}
