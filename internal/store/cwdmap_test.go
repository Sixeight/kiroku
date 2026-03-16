package store

import (
	"testing"
)

func TestCWDMapper_Map(t *testing.T) {
	mapper := NewCWDMapper([]CWDMapRule{
		{Pattern: "/home/user/repo-worktrees/*", Canonical: "/home/user/repo"},
		{Pattern: "/home/user/repo/.claude/worktrees/*", Canonical: "/home/user/repo"},
	})

	tests := []struct {
		cwd  string
		want string
	}{
		{"/home/user/repo", "/home/user/repo"},
		{"/home/user/repo-worktrees/feature-a", "/home/user/repo"},
		{"/home/user/repo-worktrees/fix-bug-123", "/home/user/repo"},
		{"/home/user/repo/.claude/worktrees/inspiring-ptolemy", "/home/user/repo"},
		{"/home/user/other-project", "/home/user/other-project"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.cwd, func(t *testing.T) {
			got := mapper.Map(tt.cwd)
			if got != tt.want {
				t.Errorf("Map(%q) = %q, want %q", tt.cwd, got, tt.want)
			}
		})
	}
}

func TestCWDMapper_Nil(t *testing.T) {
	var mapper *CWDMapper
	if got := mapper.Map("/some/path"); got != "/some/path" {
		t.Errorf("nil mapper: Map(%q) = %q, want %q", "/some/path", got, "/some/path")
	}
}

func TestParseCWDMapFlags(t *testing.T) {
	flags := []string{
		"/home/user/repo-worktrees/*=/home/user/repo",
		"/home/user/repo/.claude/worktrees/*=/home/user/repo",
	}

	mapper, err := ParseCWDMapFlags(flags)
	if err != nil {
		t.Fatalf("ParseCWDMapFlags: %v", err)
	}

	if got := mapper.Map("/home/user/repo-worktrees/feature-a"); got != "/home/user/repo" {
		t.Errorf("got %q, want /home/user/repo", got)
	}
}

func TestParseCWDMapFlags_InvalidFormat(t *testing.T) {
	_, err := ParseCWDMapFlags([]string{"no-equals-sign"})
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
}

func TestParseCWDMapFlags_Empty(t *testing.T) {
	mapper, err := ParseCWDMapFlags(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mapper != nil {
		t.Fatal("expected nil mapper for empty flags")
	}
}
