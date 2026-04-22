package capability

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/isellar/hyperios/internal/types"
)

func TestRegistry_Check_Allowlist(t *testing.T) {
	r := NewRegistry()
	r.patterns["execute:shell"] = []string{"ls", "grep"}

	tests := []struct {
		name     string
		cap      types.Capability
		expected bool
	}{
		{
			name:     "exact match",
			cap:      types.Capability{Type: "execute:shell", Scope: "ls"},
			expected: true,
		},
		{
			name:     "non-allowed",
			cap:      types.Capability{Type: "execute:shell", Scope: "rm"},
			expected: false,
		},
		{
			name:     "unknown type",
			cap:      types.Capability{Type: "execute:delete", Scope: "file"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.Check(tt.cap)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestRegistry_Check_Grants(t *testing.T) {
	r := NewRegistry()
	r.patterns["execute:shell"] = []string{"ls"}
	r.grants["execute:shell:custom"] = time.Now().Add(time.Hour)

	tests := []struct {
		name     string
		cap      types.Capability
		expected bool
	}{
		{
			name:     "grant takes precedence",
			cap:      types.Capability{Type: "execute:shell", Scope: "custom"},
			expected: true,
		},
		{
			name:     "expired grant",
			cap:      types.Capability{Type: "execute:shell", Scope: "expired"},
			expected: false,
		},
	}

	r.grants["execute:shell:expired"] = time.Now().Add(-time.Hour)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.Check(tt.cap)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestRegistry_Grant(t *testing.T) {
	r := NewRegistry()
	r.grantsFile = filepath.Join(t.TempDir(), "test_grants.json")

	cap := types.Capability{Type: "execute:shell", Scope: "test"}
	r.Grant(cap, time.Hour)

	if !r.Check(cap) {
		t.Error("expected granted capability to be checked")
	}
}

func TestRegistry_Revoke(t *testing.T) {
	r := NewRegistry()
	r.grantsFile = filepath.Join(t.TempDir(), "test_grants.json")

	cap := types.Capability{Type: "execute:shell", Scope: "test"}
	r.Grant(cap, time.Hour)
	r.Revoke(cap)

	if r.Check(cap) {
		t.Error("expected revoked capability to not be checked")
	}
}

func TestRegistry_Check_PackageCapability(t *testing.T) {
	r := NewRegistry()
	r.patterns["execute:package"] = []string{"apt:*", "flatpak:*"}

	tests := []struct {
		name     string
		cap      types.Capability
		expected bool
	}{
		{
			name:     "apt install allowed by wildcard",
			cap:      types.Capability{Type: "execute:package", Scope: "apt:curl"},
			expected: true,
		},
		{
			name:     "flatpak install allowed",
			cap:      types.Capability{Type: "execute:package", Scope: "flatpak:org.gnome.Gedit"},
			expected: true,
		},
		{
			name:     "snap not allowed",
			cap:      types.Capability{Type: "execute:package", Scope: "snap:firefox"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.Check(tt.cap)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}
