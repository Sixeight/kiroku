package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	stdhttp "net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sixeight/kiroku/internal/config"
	httpserver "github.com/Sixeight/kiroku/internal/http"
	"github.com/Sixeight/kiroku/internal/index"
	"github.com/Sixeight/kiroku/internal/jsonlutil"
	"github.com/Sixeight/kiroku/internal/sqliteutil"
	"github.com/Sixeight/kiroku/internal/store"
	_ "modernc.org/sqlite"
)

func main() {
	if err := run(context.Background(), os.Stdout, os.Stderr, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

const usage = `kiroku - Claude Code session viewer

Usage:
  kiroku <command> [options]

Commands:
  open      Start the web dashboard (default: http://localhost:4319)
  list      List sessions (fzf-friendly tab-separated output)
  doctor    Check configuration paths and data health
  reindex   Rebuild the session index from JSONL files
  resume    Resume a Claude Code session by ID

Run 'kiroku <command> -h' for command-specific options.
`

func run(ctx context.Context, stdout, stderr io.Writer, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(stderr, usage)
		return nil
	}

	switch args[0] {
	case "open":
		return runOpen(ctx, stdout, stderr, args[1:])
	case "doctor":
		return runDoctor(ctx, stdout, stderr, args[1:])
	case "reindex":
		return runReindex(ctx, stdout, stderr, args[1:])
	case "list":
		return runList(ctx, stdout, stderr, args[1:])
	case "resume":
		return runResume(ctx, stdout, stderr, args[1:])
	default:
		fmt.Fprint(stderr, usage)
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func runOpen(ctx context.Context, stdout, stderr io.Writer, args []string) error {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, `Start the web dashboard and open it in a browser.

Sessions are automatically re-indexed every 2 seconds while running.

Usage:
  kiroku open [options]

Options:
`)
		fs.PrintDefaults()
	}

	port := fs.Int("port", 4319, "listen port")
	noOpen := fs.Bool("no-open", false, "skip opening the browser")
	var mapFlags multiFlag
	fs.Var(&mapFlags, "map", "CWD mapping rule PATTERN=CANONICAL (repeatable)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	cwdMapper, err := store.ParseCWDMapFlags(mapFlags)
	if err != nil {
		return err
	}

	paths, err := config.DiscoverPaths()
	if err != nil {
		return err
	}

	dbPath := filepath.Join(paths.CacheRoot, "index.sqlite")
	indexer, err := index.Open(dbPath)
	if err != nil {
		return err
	}
	defer indexer.Close()

	reader, err := store.OpenSQLite(dbPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	reader.SetCWDMapper(cwdMapper)

	var mu sync.Mutex
	var indexing atomic.Bool
	reindex := func(callCtx context.Context, full bool) (index.Report, error) {
		mu.Lock()
		defer mu.Unlock()
		indexing.Store(true)
		defer indexing.Store(false)
		return indexer.Reindex(callCtx, paths.ProjectRoots, full)
	}

	handler, err := httpserver.NewHandler(httpserver.Options{
		Store:        reader,
		StatsPath:    paths.StatsCache,
		ConfigRoot:   paths.ConfigRoot,
		ProjectRoots: paths.ProjectRoots,
		OnReindex:    reindex,
		IsIndexing:   func() bool { return indexing.Load() },
	})
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(*port)))
	if err != nil {
		return err
	}
	defer listener.Close()

	server := &stdhttp.Server{Handler: handler}

	go func() {
		_, _ = reindex(context.Background(), false)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = reindex(context.Background(), false)
			}
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	address := "http://" + listener.Addr().String()
	fmt.Fprintf(stdout, "listening on %s\n", address)

	if !*noOpen {
		_ = openBrowser(ctx, address)
	}

	err = server.Serve(listener)
	if err != nil && !errors.Is(err, stdhttp.ErrServerClosed) {
		return err
	}

	return nil
}

func runDoctor(ctx context.Context, stdout, stderr io.Writer, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, `Check configuration paths and data health.

Reports config root, stats cache status, project roots, JSONL file count,
index database status, and broken line count.

Usage:
  kiroku doctor
`)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	paths, err := config.DiscoverPaths()
	if err != nil {
		return err
	}

	dbPath := filepath.Join(paths.CacheRoot, "index.sqlite")
	jsonlCount, brokenLines, err := inspectJSONL(paths.ProjectRoots)
	if err != nil {
		return err
	}

	indexStatus := "missing"
	lastIndexed := "-"
	if info, err := os.Stat(dbPath); err == nil {
		_ = info
		indexStatus = "present"
		if indexedAt, err := readLastIndexed(dbPath); err == nil {
			lastIndexed = indexedAt
		}
	}

	statsStatus := "missing"
	if _, err := os.Stat(paths.StatsCache); err == nil {
		statsStatus = "present"
	}

	fmt.Fprintf(stdout, "config root: %s\n", paths.ConfigRoot)
	fmt.Fprintf(stdout, "stats-cache.json: %s\n", statsStatus)
	fmt.Fprintf(stdout, "project roots: %s\n", strings.Join(paths.ProjectRoots, ", "))
	fmt.Fprintf(stdout, "jsonl files: %d\n", jsonlCount)
	fmt.Fprintf(stdout, "index db: %s\n", indexStatus)
	fmt.Fprintf(stdout, "last indexed: %s\n", lastIndexed)
	fmt.Fprintf(stdout, "broken lines: %d\n", brokenLines)

	_ = ctx
	return nil
}

func runReindex(ctx context.Context, stdout, stderr io.Writer, args []string) error {
	fs := flag.NewFlagSet("reindex", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, `Rebuild the session index from JSONL transcript files.

By default, only re-indexes files that have changed since the last run.
Use -full to drop and rebuild the entire index (required after schema changes).

Usage:
  kiroku reindex [options]

Options:
`)
		fs.PrintDefaults()
	}

	full := fs.Bool("full", false, "drop all data and rebuild from scratch")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	paths, err := config.DiscoverPaths()
	if err != nil {
		return err
	}

	dbPath := filepath.Join(paths.CacheRoot, "index.sqlite")
	indexer, err := index.Open(dbPath)
	if err != nil {
		return err
	}
	defer indexer.Close()

	report, err := indexer.Reindex(ctx, paths.ProjectRoots, *full)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "files_indexed=%d broken_lines=%d\n", report.FilesIndexed, report.BrokenLines)
	return nil
}

func inspectJSONL(roots []string) (int, int, error) {
	jsonlCount := 0
	brokenLines := 0

	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if d.IsDir() || filepath.Ext(path) != ".jsonl" {
				return nil
			}

			jsonlCount++
			count, err := countBrokenLines(path)
			if err != nil {
				return err
			}
			brokenLines += count

			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return 0, 0, err
		}
	}

	return jsonlCount, brokenLines, nil
}

func countBrokenLines(path string) (int, error) {
	broken := 0

	err := jsonlutil.ForEachLine(path, func(line []byte) error {
		if !json.Valid(line) {
			broken++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	return broken, nil
}

func runList(ctx context.Context, stdout, stderr io.Writer, args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, `List sessions in fzf-friendly tab-separated format.

Output columns: SESSION_ID, STARTED, PROJECT, BRANCH, MSGS, TOOLS, PREVIEW

Usage:
  kiroku list [options]

Examples:
  kiroku list
  kiroku list -all
  kiroku list -branch main -sort longest
  kiroku resume $(kiroku list -all | fzf | awk '{print $1}')

Options:
`)
		fs.PrintDefaults()
	}

	limit := fs.Int("limit", 50, "maximum number of sessions (max 100)")
	project := fs.String("project", "", "filter by project path (default: current directory)")
	all := fs.Bool("all", false, "show sessions from all projects")
	branch := fs.String("branch", "", "filter by git branch")
	from := fs.String("from", "", "filter by start date (YYYY-MM-DD)")
	to := fs.String("to", "", "filter by end date (YYYY-MM-DD)")
	q := fs.String("q", "", "search preview, cwd, and branch")
	sortBy := fs.String("sort", "recent", "sort order: recent, longest, tools")
	jsonOut := fs.Bool("json", false, "output as JSON lines")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	cwd := *project
	if !*all && cwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		cwd = wd
	}

	paths, err := config.DiscoverPaths()
	if err != nil {
		return err
	}

	dbPath := filepath.Join(paths.CacheRoot, "index.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		return errors.New("no index found; run 'kiroku reindex' first")
	}

	reader, err := store.OpenSQLite(dbPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	sessions, _, err := reader.ListSessions(ctx, store.ListSessionsParams{
		Limit:  *limit,
		CWD:    cwd,
		Branch: *branch,
		From:   *from,
		To:     *to,
		Q:      *q,
		Sort:   *sortBy,
	})
	if err != nil {
		return err
	}

	if *jsonOut {
		enc := json.NewEncoder(stdout)
		for _, s := range sessions {
			if err := enc.Encode(s); err != nil {
				return err
			}
		}
		return nil
	}

	for _, s := range sessions {
		fmt.Fprintln(stdout, formatListLine(s))
	}
	return nil
}

func formatListLine(s store.RecentSessionSummary) string {
	started := formatShortTime(s.StartedAt)
	project := filepath.Base(s.CWD)
	preview := sanitizePreview(s.Preview, 60)
	return fmt.Sprintf("%s\t%s\t%s\t%s\t%d\t%d\t%s",
		s.SessionID, started, project, s.GitBranch,
		s.MessageCount, s.ToolCallCount, preview)
}

func formatShortTime(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	return t.Local().Format("2006-01-02 15:04")
}

func sanitizePreview(s string, maxRunes int) string {
	s = strings.NewReplacer("\n", " ", "\r", "", "\t", " ").Replace(s)
	runes := []rune(s)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "..."
	}
	return s
}

func runResume(ctx context.Context, stdout, stderr io.Writer, args []string) error {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, `Resume a Claude Code session.

Launches 'claude --resume <session-id>' in the session's original working
directory. The session ID can be found in the web dashboard.

Usage:
  kiroku resume <session-id>
`)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if fs.NArg() < 1 {
		fs.Usage()
		return errors.New("missing required argument: session-id")
	}
	sessionID := fs.Arg(0)

	paths, err := config.DiscoverPaths()
	if err != nil {
		return err
	}

	dbPath := filepath.Join(paths.CacheRoot, "index.sqlite")
	reader, err := store.OpenSQLite(dbPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	cwd, err := reader.GetSessionCWD(ctx, sessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("session not found: %s", sessionID)
		}
		return err
	}

	claudePath, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude not found in PATH: %w", err)
	}

	cmd := exec.CommandContext(ctx, claudePath, "--resume", sessionID)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if cwd != "" {
		if _, statErr := os.Stat(cwd); statErr != nil {
			fmt.Fprintf(stderr, "warning: directory %s does not exist, using current directory\n", cwd)
		} else {
			cmd.Dir = cwd
		}
	}

	fmt.Fprintf(stdout, "resuming session %s in %s\n", sessionID, cwd)
	return cmd.Run()
}

type multiFlag []string

func (f *multiFlag) String() string { return strings.Join(*f, ", ") }
func (f *multiFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func openBrowser(ctx context.Context, address string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", address)
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", address)
	}

	return cmd.Start()
}

func readLastIndexed(dbPath string) (string, error) {
	db, err := sqliteutil.Open(dbPath)
	if err != nil {
		return "", err
	}
	defer db.Close()

	var lastIndexed sql.NullString
	err = db.QueryRow(`SELECT MAX(last_indexed_at) FROM files`).Scan(&lastIndexed)
	if err != nil {
		return "", err
	}
	if !lastIndexed.Valid {
		return "-", nil
	}

	return lastIndexed.String, nil
}
