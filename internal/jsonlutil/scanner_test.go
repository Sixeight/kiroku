package jsonlutil_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sixeight/kiroku/internal/jsonlutil"
)

func TestForEachLineReadsAllLines(t *testing.T) {
	path := writeTempFile(t, "a\nb\nc\n")

	var lines []string
	err := jsonlutil.ForEachLine(path, func(line []byte) error {
		lines = append(lines, string(line))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if got, want := len(lines), 3; got != want {
		t.Fatalf("lines = %d, want %d", got, want)
	}

	if got, want := lines[0], "a"; got != want {
		t.Fatalf("lines[0] = %q, want %q", got, want)
	}
	if got, want := lines[2], "c"; got != want {
		t.Fatalf("lines[2] = %q, want %q", got, want)
	}
}

func TestForEachLineSkipsEmptyLines(t *testing.T) {
	path := writeTempFile(t, "a\n\n  \nb\n")

	var lines []string
	err := jsonlutil.ForEachLine(path, func(line []byte) error {
		lines = append(lines, string(line))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if got, want := len(lines), 2; got != want {
		t.Fatalf("lines = %d, want %d", got, want)
	}
}

func TestForEachLineHandlesNoTrailingNewline(t *testing.T) {
	path := writeTempFile(t, "first\nsecond")

	var lines []string
	err := jsonlutil.ForEachLine(path, func(line []byte) error {
		lines = append(lines, string(line))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if got, want := len(lines), 2; got != want {
		t.Fatalf("lines = %d, want %d", got, want)
	}
	if got, want := lines[1], "second"; got != want {
		t.Fatalf("lines[1] = %q, want %q", got, want)
	}
}

func TestForEachLineEmptyFile(t *testing.T) {
	path := writeTempFile(t, "")

	called := false
	err := jsonlutil.ForEachLine(path, func(line []byte) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("fn should not be called for an empty file")
	}
}

func TestForEachLineReturnsErrorFromCallback(t *testing.T) {
	path := writeTempFile(t, "a\nb\nc\n")

	sentinel := errors.New("stop")
	count := 0
	err := jsonlutil.ForEachLine(path, func(line []byte) error {
		count++
		if count == 2 {
			return sentinel
		}
		return nil
	})

	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
}

func TestForEachLineReturnsErrorForMissingFile(t *testing.T) {
	err := jsonlutil.ForEachLine("/nonexistent/path/file.jsonl", func(line []byte) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestForEachLineTrimsWhitespace(t *testing.T) {
	path := writeTempFile(t, "  hello  \n\tworld\t\n")

	var lines []string
	err := jsonlutil.ForEachLine(path, func(line []byte) error {
		lines = append(lines, string(line))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if got, want := lines[0], "hello"; got != want {
		t.Fatalf("lines[0] = %q, want %q", got, want)
	}
	if got, want := lines[1], "world"; got != want {
		t.Fatalf("lines[1] = %q, want %q", got, want)
	}
}

func writeTempFile(t *testing.T, content string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	return path
}
