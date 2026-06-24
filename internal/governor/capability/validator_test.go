package capability

import (
	"testing"

	"github.com/isellar/hyperios/internal/types"
)

func newTestValidator() *CommandValidator {
	r := NewRegistry()
	r.patterns["execute:shell"] = []string{"grep", "ls", "find", "dpkg"}
	r.patterns["execute:git"] = []string{"git:*"}
	r.patterns["execute:package"] = []string{"apt:*"}
	r.patterns["execute:process"] = []string{"systemctl:*"}
	r.patterns["execute:config"] = []string{"/etc/**", "/var/lib/hyperi/**"}
	r.patterns["network:outbound"] = []string{"api.example.com", "*"}
	return NewCommandValidator(r)
}

func TestCommandValidator_EmptyCommand(t *testing.T) {
	v := newTestValidator()
	result := v.Validate(types.ActionStep{
		ID:         "s1",
		Capability: types.Capability{Type: "execute:shell", Scope: "grep"},
		Command:    []string{},
	})
	if result.Valid {
		t.Error("expected invalid for empty Command")
	}
}

func TestCommandValidator_ShellMetacharacter(t *testing.T) {
	v := newTestValidator()
	cases := []struct {
		name string
		arg  string
	}{
		{"pipe", "foo|bar"},
		{"semicolon", "foo;bar"},
		{"and-and", "foo&&bar"},
		{"subshell", "$(rm -rf /)"},
		{"backtick", "`rm -rf /`"},
		{"redirect-out", "foo>bar"},
		{"redirect-in", "foo<bar"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := v.Validate(types.ActionStep{
				ID:         "s1",
				Capability: types.Capability{Type: "execute:shell", Scope: "grep"},
				Command:    []string{"grep", tc.arg},
			})
			if result.Valid {
				t.Errorf("expected invalid for arg %q containing metacharacter", tc.arg)
			}
		})
	}
}

func TestCommandValidator_AllowlistMembership(t *testing.T) {
	v := newTestValidator()

	result := v.Validate(types.ActionStep{
		ID:         "s1",
		Capability: types.Capability{Type: "execute:shell", Scope: "ls"},
		Command:    []string{"ls", "-la", "/tmp"},
	})
	if !result.Valid {
		if result.Reason != "" && contains(result.Reason, "not on the execute:shell allowlist") {
			t.Errorf("expected ls to be on allowlist, got: %s", result.Reason)
		}
	}

	result = v.Validate(types.ActionStep{
		ID:         "s2",
		Capability: types.Capability{Type: "execute:shell", Scope: "rm"},
		Command:    []string{"rm", "-rf", "/tmp/test"},
	})
	if result.Valid {
		t.Error("expected invalid for rm which is not on allowlist")
	}
}

func TestCommandValidator_ConfigScope_Mismatch(t *testing.T) {
	v := newTestValidator()
	result := v.Validate(types.ActionStep{
		ID:         "s1",
		Capability: types.Capability{Type: "execute:config", Scope: "/etc/nginx/nginx.conf"},
		Command:    []string{"/etc/nginx/other.conf", "content"},
	})
	if result.Valid {
		t.Error("expected invalid when Command[0] path does not match Capability.Scope")
	}
}

func TestCommandValidator_ConfigScope_Match(t *testing.T) {
	v := newTestValidator()
	result := v.Validate(types.ActionStep{
		ID:         "s1",
		Capability: types.Capability{Type: "execute:config", Scope: "/etc/nginx/nginx.conf"},
		Command:    []string{"/etc/nginx/nginx.conf", "server { listen 80; }"},
	})
	if !result.Valid {
		t.Errorf("expected valid for matching config path, got: %s", result.Reason)
	}
}

func TestCommandValidator_NetworkOutbound_HostMismatch(t *testing.T) {
	v := newTestValidator()
	result := v.Validate(types.ActionStep{
		ID:         "s1",
		Capability: types.Capability{Type: "network:outbound", Scope: "api.example.com"},
		Command:    []string{"GET", "https://evil.com/data"},
	})
	if result.Valid {
		t.Error("expected invalid when URL host does not match Capability.Scope")
	}
}

func TestCommandValidator_NetworkOutbound_HostMatch(t *testing.T) {
	v := newTestValidator()
	result := v.Validate(types.ActionStep{
		ID:         "s1",
		Capability: types.Capability{Type: "network:outbound", Scope: "api.example.com"},
		Command:    []string{"GET", "https://api.example.com/status"},
	})
	if !result.Valid {
		t.Errorf("expected valid for matching host, got: %s", result.Reason)
	}
}

func TestCommandValidator_NetworkOutbound_Wildcard(t *testing.T) {
	v := newTestValidator()
	result := v.Validate(types.ActionStep{
		ID:         "s1",
		Capability: types.Capability{Type: "network:outbound", Scope: "*"},
		Command:    []string{"GET", "https://anything.com/data"},
	})
	if !result.Valid {
		t.Errorf("expected valid for wildcard scope, got: %s", result.Reason)
	}
}

func TestCommandValidator_NetworkOutbound_MissingURL(t *testing.T) {
	v := newTestValidator()
	result := v.Validate(types.ActionStep{
		ID:         "s1",
		Capability: types.Capability{Type: "network:outbound", Scope: "api.example.com"},
		Command:    []string{"GET"},
	})
	if result.Valid {
		t.Error("expected invalid for missing URL in Command")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
