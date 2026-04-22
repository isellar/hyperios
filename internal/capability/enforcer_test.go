package capability

import (
	"testing"
	"time"

	"github.com/isellar/hyperios/internal/types"
)

func TestEnforcer_CanExecute(t *testing.T) {
	r := NewRegistry()
	r.patterns["execute:shell"] = []string{"ls", "grep"}
	e := NewEnforcer(r)

	step := types.ActionStep{
		Capability: types.Capability{Type: "execute:shell", Scope: "ls"},
	}

	if !e.CanExecute(step) {
		t.Error("expected CanExecute to return true for allowed capability")
	}
}

func TestEnforcer_CanExecute_NotAllowed(t *testing.T) {
	r := NewRegistry()
	r.patterns["execute:shell"] = []string{"ls"}
	e := NewEnforcer(r)

	step := types.ActionStep{
		Capability: types.Capability{Type: "execute:shell", Scope: "rm"},
	}

	if e.CanExecute(step) {
		t.Error("expected CanExecute to return false for non-allowed capability")
	}
}

func TestEnforcer_Validate_Allowed(t *testing.T) {
	r := NewRegistry()
	r.patterns["execute:shell"] = []string{"ls"}
	e := NewEnforcer(r)

	step := types.ActionStep{
		Capability: types.Capability{Type: "execute:shell", Scope: "ls"},
	}

	err := e.Validate(step)
	if err != nil {
		t.Errorf("expected Validate to return nil, got %v", err)
	}
}

func TestEnforcer_Validate_NotAllowed_NoPrompt(t *testing.T) {
	r := NewRegistry()
	r.patterns["execute:shell"] = []string{"ls"}
	e := NewEnforcer(r)
	e.SetPromptEnabled(false)

	step := types.ActionStep{
		Capability: types.Capability{Type: "execute:shell", Scope: "rm"},
	}

	err := e.Validate(step)
	if err == nil {
		t.Error("expected Validate to return error")
	}
}

func TestEnforcer_SetGrantTTL(t *testing.T) {
	r := NewRegistry()
	e := NewEnforcer(r)
	e.SetGrantTTL(2 * time.Hour)

	if e.grantTTL != 2*time.Hour {
		t.Errorf("expected TTL 2h, got %v", e.grantTTL)
	}
}
