package config

import (
	"os"
	"path/filepath"
)

type Paths struct {
	ConfigRoot   string
	CacheRoot    string
	StatsCache   string
	ProjectRoots []string
}

func DiscoverPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}

	configRoot := filepath.Join(home, ".config")
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		configRoot = xdg
	}

	cacheRoot := filepath.Join(home, ".cache")
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		cacheRoot = xdg
	}

	xdgClaude := filepath.Join(configRoot, "claude")
	legacyClaude := filepath.Join(home, ".claude")

	projectRoots := make([]string, 0, 2)

	if hasDir(filepath.Join(xdgClaude, "projects")) {
		projectRoots = append(projectRoots, filepath.Join(xdgClaude, "projects"))
	}

	if hasDir(filepath.Join(legacyClaude, "projects")) {
		projectRoots = append(projectRoots, filepath.Join(legacyClaude, "projects"))
	}

	return Paths{
		ConfigRoot:   xdgClaude,
		CacheRoot:    filepath.Join(cacheRoot, "kiroku"),
		StatsCache:   filepath.Join(xdgClaude, "stats-cache.json"),
		ProjectRoots: projectRoots,
	}, nil
}

func hasDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}

	return info.IsDir()
}
