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

	if strings.Contains(pattern, "**") {
		return matchDoublestar(pattern, value)
	}

	matched, err := filepath.Match(pattern, value)
	if err != nil {
		return false
	}
	return matched
}

func matchDoublestar(pattern, value string) bool {
	pattern = filepath.ToSlash(pattern)
	value = filepath.ToSlash(value)

	parts := strings.SplitN(pattern, "**", 2)
	prefix := parts[0]
	suffix := parts[1]

	if prefix != "" && !strings.HasPrefix(value, prefix) {
		return false
	}

	if suffix == "" {
		return true
	}

	suffix = strings.TrimPrefix(suffix, "/")

	remaining := strings.TrimPrefix(value, prefix)

	if strings.Contains(suffix, "**") {
		return matchDoublestar(suffix, remaining)
	}

	if filepath.ToSlash(remaining) == suffix {
		return true
	}
	if strings.HasSuffix("/"+filepath.ToSlash(remaining), "/"+suffix) {
		return true
	}

	return false
}

func ExpandWorkspace(pattern, workspace string) string {
	return strings.ReplaceAll(pattern, "{workspace}", workspace)
}
