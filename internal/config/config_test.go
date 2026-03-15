package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Sixeight/kiroku/internal/config"
)

func TestDiscoverPathsPrefersXDGAndKeepsLegacy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	xdgConfigHome := filepath.Join(t.TempDir(), "xdg-config")
	t.Setenv("XDG_CONFIG_HOME", xdgConfigHome)

	xdgCacheHome := filepath.Join(t.TempDir(), "xdg-cache")
	t.Setenv("XDG_CACHE_HOME", xdgCacheHome)

	xdgClaude := filepath.Join(xdgConfigHome, "claude")
	legacyClaude := filepath.Join(os.Getenv("HOME"), ".claude")

	if err := os.MkdirAll(filepath.Join(xdgClaude, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(legacyClaude, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}

	paths, err := config.DiscoverPaths()
	if err != nil {
		t.Fatal(err)
	}

	if got, want := paths.ConfigRoot, xdgClaude; got != want {
		t.Fatalf("ConfigRoot = %q, want %q", got, want)
	}

	if got, want := paths.CacheRoot, filepath.Join(xdgCacheHome, "kiroku"); got != want {
		t.Fatalf("CacheRoot = %q, want %q", got, want)
	}

	if len(paths.ProjectRoots) != 2 {
		t.Fatalf("ProjectRoots len = %d, want 2", len(paths.ProjectRoots))
	}

	if got, want := paths.ProjectRoots[0], filepath.Join(xdgClaude, "projects"); got != want {
		t.Fatalf("ProjectRoots[0] = %q, want %q", got, want)
	}

	if got, want := paths.ProjectRoots[1], filepath.Join(legacyClaude, "projects"); got != want {
		t.Fatalf("ProjectRoots[1] = %q, want %q", got, want)
	}
}
