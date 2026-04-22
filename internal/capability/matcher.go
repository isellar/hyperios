package capability

import (
	"path/filepath"
	"strings"
)

func Matches(pattern, value string) bool {
	pattern = strings.TrimSpace(pattern)
	value = strings.TrimSpace(value)

	if pattern == "" || value == "" {
		return false
	}

	if pattern == "*" {
		return true
	}

	matched, err := filepath.Match(pattern, value)
	if err != nil {
		return false
	}
	return matched
}

func ExpandWorkspace(pattern, workspace string) string {
	return strings.ReplaceAll(pattern, "{workspace}", workspace)
}
