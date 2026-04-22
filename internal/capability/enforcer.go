package capability

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/isellar/hyperios/internal/types"
)

var ErrCapabilityNotGranted = &CapabilityNotGrantedError{}

type CapabilityNotGrantedError struct {
	Capability types.Capability
}

func (e *CapabilityNotGrantedError) Error() string {
	return fmt.Sprintf("capability not granted: %s %s", e.Capability.Type, e.Capability.Scope)
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
