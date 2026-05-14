package capability

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/isellar/hyperios/internal/types"
)

// ErrCapabilityNotGranted is the sentinel error for capability denials.
// Use errors.As (not errors.Is) to extract the Capability field:
//
//	var capErr *capability.CapabilityNotGrantedError
//	if errors.As(err, &capErr) {
//	    // capErr.Capability contains type and scope
//	}
//
// errors.Is also works via the Is() method for simple presence checks.
var ErrCapabilityNotGranted = &CapabilityNotGrantedError{}

type CapabilityNotGrantedError struct {
	Capability types.Capability
}

func (e *CapabilityNotGrantedError) Error() string {
	return fmt.Sprintf("capability not granted: %s %s", e.Capability.Type, e.Capability.Scope)
}

// Is makes errors.Is(err, ErrCapabilityNotGranted) work correctly.
// Two CapabilityNotGrantedErrors are considered equal by type regardless of
// which Capability they carry.
func (e *CapabilityNotGrantedError) Is(target error) bool {
	_, ok := target.(*CapabilityNotGrantedError)
	return ok
}

// AsCapabilityNotGranted is a convenience wrapper around errors.As.
// Returns the typed error and true if err is a CapabilityNotGrantedError,
// allowing callers to access the denied Capability without a type assertion.
func AsCapabilityNotGranted(err error) (*CapabilityNotGrantedError, bool) {
	var e *CapabilityNotGrantedError
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

type Enforcer struct {
	registry  *Registry
	canPrompt bool
	grantTTL  time.Duration
}

func NewEnforcer(registry *Registry) *Enforcer {
	return &Enforcer{
		registry:  registry,
		canPrompt: true,
		grantTTL:  24 * time.Hour,
	}
}

func (e *Enforcer) SetPromptEnabled(enabled bool) {
	e.canPrompt = enabled
}

func (e *Enforcer) SetGrantTTL(ttl time.Duration) {
	e.grantTTL = ttl
}

func (e *Enforcer) Validate(step types.ActionStep) error {
	if e.registry.Check(step.Capability) {
		return nil
	}

	if !e.canPrompt {
		return &CapabilityNotGrantedError{Capability: step.Capability}
	}

	if !isTerminal() {
		return &CapabilityNotGrantedError{Capability: step.Capability}
	}

	fmt.Fprintf(os.Stderr, "\nCapability not granted: %s %s\n", step.Capability.Type, step.Capability.Scope)
	fmt.Fprint(os.Stderr, "Grant this capability? (y/n): ")

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil || strings.TrimSpace(strings.ToLower(input)) != "y" {
		return &CapabilityNotGrantedError{Capability: step.Capability}
	}

	e.registry.Grant(step.Capability, e.grantTTL)
	fmt.Fprintf(os.Stderr, "Granted: %s %s (expires in %v)\n", step.Capability.Type, step.Capability.Scope, e.grantTTL)

	return nil
}

func isTerminal() bool {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

func (e *Enforcer) CanExecute(step types.ActionStep) bool {
	return e.registry.Check(step.Capability)
}
