package store

import (
	"fmt"
	"path/filepath"
	"strings"
)

type CWDMapRule struct {
	Pattern   string
	Canonical string
}

type CWDMapper struct {
	rules []CWDMapRule
}

func NewCWDMapper(rules []CWDMapRule) *CWDMapper {
	if len(rules) == 0 {
		return nil
	}
	return &CWDMapper{rules: rules}
}

func (m *CWDMapper) Map(cwd string) string {
	if m == nil || cwd == "" {
		return cwd
	}
	for _, rule := range m.rules {
		if matched, _ := filepath.Match(rule.Pattern, cwd); matched {
			return rule.Canonical
		}
	}
	return cwd
}

func ParseCWDMapFlags(flags []string) (*CWDMapper, error) {
	if len(flags) == 0 {
		return nil, nil
	}
	rules := make([]CWDMapRule, 0, len(flags))
	for _, f := range flags {
		idx := strings.LastIndex(f, "=")
		if idx < 0 {
			return nil, fmt.Errorf("invalid --map format %q: expected PATTERN=CANONICAL", f)
		}
		rules = append(rules, CWDMapRule{
			Pattern:   f[:idx],
			Canonical: f[idx+1:],
		})
	}
	return NewCWDMapper(rules), nil
}
