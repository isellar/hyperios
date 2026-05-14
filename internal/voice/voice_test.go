package voice

import (
	"testing"
)

func TestCleanTranscript(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain text unchanged",
			input: "check disk space",
			want:  "check disk space",
		},
		{
			name:  "strips leading timestamp",
			input: "[00:00:00.000]   check disk space",
			want:  "check disk space",
		},
		{
			name:  "strips timestamp range lines",
			input: "[00:00:00.000 --> 00:00:02.000]\ncheck disk space",
			want:  "check disk space",
		},
		{
			name:  "trims whitespace",
			input: "  install nginx  ",
			want:  "install nginx",
		},
		{
			name:  "joins multiple lines",
			input: "[00:00:00.000]   check disk\n[00:00:01.000]   space please",
			want:  "check disk space please",
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
		{
			name:  "only whitespace",
			input: "   \n\n  ",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanTranscript(tt.input)
			if got != tt.want {
				t.Errorf("cleanTranscript(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsAvailable_MissingModel(t *testing.T) {
	if IsAvailable("/nonexistent/model.bin", "/usr/local/bin/whisper-cli") {
		t.Error("expected IsAvailable to return false for missing model")
	}
}

func TestIsAvailable_MissingCLI(t *testing.T) {
	// Use /dev/null as a stand-in for the model (exists, but wrong content — that's fine for availability check)
	if IsAvailable("/dev/null", "/nonexistent/whisper-cli-xyz") {
		t.Error("expected IsAvailable to return false for missing CLI")
	}
}
