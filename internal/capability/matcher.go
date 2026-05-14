package capability

import (
	"path/filepath"
	"strings"
)

// Matches reports whether value matches pattern.
//
// Supports two matching modes:
//  1. Patterns containing "**" — matches any number of path segments.
//     "/etc/**" matches "/etc/nginx/nginx.conf" and "/etc/foo".
//     "{workspace}/**" matches any path under the workspace root after expansion.
//  2. All other patterns — delegated to filepath.Match, which supports
//     single-segment wildcards ("*") and character classes ("[a-z]").
//
// Both pattern and value are trimmed of whitespace before matching.
func Matches(pattern, value string) bool {
	pattern = strings.TrimSpace(pattern)
	value = strings.TrimSpace(value)

	if pattern == "" || value == "" {
		return false
	}

	// Wildcard: matches everything
	if pattern == "*" {
		return true
	}

	// ** handling: split on "**" and check that the prefix and suffix match.
	// A pattern like "/etc/**" has prefix "/etc/" and no suffix.
	// A pattern like "**/foo.go" has no prefix and suffix "/foo.go".
	if strings.Contains(pattern, "**") {
		return matchDoublestar(pattern, value)
	}

	// Standard single-segment glob via filepath.Match
	matched, err := filepath.Match(pattern, value)
	if err != nil {
		return false
	}
	return matched
}

// matchDoublestar handles patterns containing "**".
// Splits the pattern at "**" and verifies:
//   - value starts with the prefix (everything before **)
//   - value ends with the suffix (everything after **), if a suffix exists
//
// Multiple "**" segments are handled by repeated splitting.
func matchDoublestar(pattern, value string) bool {
	// Normalise separators to forward slash for consistent matching
	pattern = filepath.ToSlash(pattern)
	value = filepath.ToSlash(value)

	parts := strings.SplitN(pattern, "**", 2)
	prefix := parts[0]
	suffix := parts[1]

	// The value must start with the prefix
	if prefix != "" && !strings.HasPrefix(value, prefix) {
		return false
	}

	// If there's nothing after **, the prefix match is sufficient
	if suffix == "" {
		return true
	}

	// Strip leading separator from suffix for clean matching
	suffix = strings.TrimPrefix(suffix, "/")

	// Remove the matched prefix from value before checking suffix
	remaining := strings.TrimPrefix(value, prefix)

	// If suffix itself contains another **, recurse
	if strings.Contains(suffix, "**") {
		return matchDoublestar(suffix, remaining)
	}

	// Suffix must match the end of value (which may be nested deeper)
	// e.g. pattern "**/foo.go", value "internal/agents/foo.go" → suffix "foo.go"
	// filepath.Match the last segment(s) of remaining against suffix
	if filepath.ToSlash(remaining) == suffix {
		return true
	}
	if strings.HasSuffix("/"+filepath.ToSlash(remaining), "/"+suffix) {
		return true
	}

	return false
}

// ExpandWorkspace replaces the "{workspace}" placeholder in a pattern with
// the actual workspace root path.
func ExpandWorkspace(pattern, workspace string) string {
	return strings.ReplaceAll(pattern, "{workspace}", workspace)
}
