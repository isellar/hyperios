package capability

import (
	"testing"
)

func TestMatches(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		value    string
		expected bool
	}{
		{"exact match", "grep", "grep", true},
		{"glob star matches all", "*", "anything", true},
		{"glob star at end", "*.go", "main.go", true},
		{"glob star no match", "*.go", "main.txt", false},
		{"glob question", "file?.txt", "file1.txt", true},
		{"glob question no match", "file?.txt", "file10.txt", false},
		{"empty pattern", "", "value", false},
		{"empty value", "pattern", "", false},
		{"double star matches path", "/repo/**", "/repo/main.go", true},
		{"double star no match", "/repo/**", "/other/main.go", false},
		{"pattern with slash", "src/*.go", "src/main.go", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Matches(tt.pattern, tt.value)
			if result != tt.expected {
				t.Errorf("Matches(%q, %q) = %v, want %v", tt.pattern, tt.value, result, tt.expected)
			}
		})
	}
}

func TestExpandWorkspace(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		ws       string
		expected string
	}{
		{"replaces placeholder", "{workspace}/**", "/my/project", "/my/project/**"},
		{"no placeholder", "/absolute/path", "/anything", "/absolute/path"},
		{"empty workspace", "{workspace}/src", "", "/src"},
		{"multiple placeholders", "{workspace}/{workspace}", "/repo", "/repo//repo"},
		{"no braces", "/repo/src", "/ws", "/repo/src"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExpandWorkspace(tt.pattern, tt.ws)
			if result != tt.expected {
				t.Errorf("ExpandWorkspace(%q, %q) = %q, want %q", tt.pattern, tt.ws, result, tt.expected)
			}
		})
	}
}
