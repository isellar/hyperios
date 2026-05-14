package capability

import (
	"fmt"
	"net/url"
	"os/exec"
	"strings"

	"github.com/isellar/hyperios/internal/manifest"
	"github.com/isellar/hyperios/internal/types"
)

// shellMetacharacters is the set of characters that indicate shell injection attempts.
// Since the executor uses exec.Command (no shell), these are never valid in arguments.
var shellMetacharacters = []string{"|", ";", "&&", "||", "$(", "`", ">", "<", "&", "\n", "\r"}

// ValidationResult is the output of CommandValidator.Validate.
type ValidationResult struct {
	Valid  bool
	Reason string // populated when Valid == false
}

// CommandValidator runs deterministic pre-checks on an ActionStep before it
// reaches the Executor. It is not an LLM — it is a cheap synchronous gate.
//
// Checks performed in order:
//  1. Structural — Command is non-empty; no shell metacharacters in any argument.
//  2. Allowlist  — Command[0] binary matches an entry in the registry for the declared capability type.
//  3. Consistency — for execute:config, Command[0] path must match Capability.Scope;
//     for network:outbound, host in Command[1] URL must match Capability.Scope.
//  4. Manifest path check — if a manifest store is set, checks path sensitivity
//     and PAM requirements for any Command arg that is an absolute filesystem path.
type CommandValidator struct {
	registry *Registry
	manifest *manifest.Store
}

// NewCommandValidator creates a CommandValidator backed by the given registry.
func NewCommandValidator(registry *Registry) *CommandValidator {
	return &CommandValidator{registry: registry}
}

// WithManifest returns a new CommandValidator with a manifest store attached.
// When set, the validator checks path sensitivity and PAM requirements.
func (v *CommandValidator) WithManifest(store *manifest.Store) *CommandValidator {
	return &CommandValidator{registry: v.registry, manifest: store}
}

// Validate runs all checks and returns a ValidationResult.
func (v *CommandValidator) Validate(step types.ActionStep) ValidationResult {
	// Check 1 — structural
	if result := v.checkStructural(step); !result.Valid {
		return result
	}

	// Check 2 — allowlist membership
	if result := v.checkAllowlist(step); !result.Valid {
		return result
	}

	// Check 3 — scope consistency
	if result := v.checkScopeConsistency(step); !result.Valid {
		return result
	}

	// Check 4 — manifest path check (active when manifest store is attached)
	if v.manifest != nil {
		if result := v.checkManifest(step); !result.Valid {
			return result
		}
	}

	return ValidationResult{Valid: true}
}

// checkStructural verifies Command is non-empty and contains no shell metacharacters.
// For execute:config, Command[1] is file content and is exempt from the metacharacter check
// (config files legitimately contain semicolons, pipes, redirects, etc.).
func (v *CommandValidator) checkStructural(step types.ActionStep) ValidationResult {
	if len(step.Command) == 0 {
		return ValidationResult{
			Valid:  false,
			Reason: fmt.Sprintf("step %q: Command is empty — every step must specify a literal command", step.ID),
		}
	}

	isConfigWrite := strings.HasPrefix(step.Capability.Type, "execute:config")

	for i, arg := range step.Command {
		// For execute:config, Command[1] is file content — exempt from metacharacter check.
		if isConfigWrite && i >= 1 {
			continue
		}
		for _, meta := range shellMetacharacters {
			if strings.Contains(arg, meta) {
				return ValidationResult{
					Valid:  false,
					Reason: fmt.Sprintf("step %q: Command[%d] contains shell metacharacter %q — shell injection not permitted", step.ID, i, meta),
				}
			}
		}
	}

	// For execute:config, Command[0] is a path not a binary — skip LookPath.
	// For network:outbound, Command[0] is an HTTP method — skip LookPath.
	capType := step.Capability.Type
	if strings.HasPrefix(capType, "execute:config") || strings.HasPrefix(capType, "network:outbound") {
		return ValidationResult{Valid: true}
	}

	// Verify the binary is resolvable. This catches typos and missing packages
	// before the step reaches the executor.
	binary := step.Command[0]
	if _, err := exec.LookPath(binary); err != nil {
		return ValidationResult{
			Valid:  false,
			Reason: fmt.Sprintf("step %q: binary %q not found in PATH: %v", step.ID, binary, err),
		}
	}

	return ValidationResult{Valid: true}
}

// checkAllowlist verifies that the declared capability is on the allowlist.
// For execute:shell, the binary name (Command[0]) must match the allowlist scope.
// For other types, Capability.Scope is checked as-is against the registry.
func (v *CommandValidator) checkAllowlist(step types.ActionStep) ValidationResult {
	if v.registry == nil {
		return ValidationResult{Valid: true}
	}

	capType := step.Capability.Type

	// For execute:shell, the allowlist scope is the binary name.
	// We check both the declared scope and the actual Command[0] binary.
	if strings.HasPrefix(capType, "execute:shell") {
		binaryScope := types.Capability{Type: capType, Scope: step.Command[0]}
		if !v.registry.Check(binaryScope) {
			return ValidationResult{
				Valid:  false,
				Reason: fmt.Sprintf("step %q: binary %q is not on the execute:shell allowlist", step.ID, step.Command[0]),
			}
		}
		return ValidationResult{Valid: true}
	}

	// For all other types, check the declared capability scope.
	if !v.registry.Check(step.Capability) {
		return ValidationResult{
			Valid:  false,
			Reason: fmt.Sprintf("step %q: capability %s %s is not on the allowlist", step.ID, capType, step.Capability.Scope),
		}
	}

	return ValidationResult{Valid: true}
}

// checkScopeConsistency verifies that Command args are consistent with Capability.Scope
// for capability types where the two must agree.
func (v *CommandValidator) checkScopeConsistency(step types.ActionStep) ValidationResult {
	capType := step.Capability.Type

	switch {
	case strings.HasPrefix(capType, "execute:config"):
		// For config writes: Command[0] must be the target path and must match Capability.Scope.
		if len(step.Command) < 2 {
			return ValidationResult{
				Valid:  false,
				Reason: fmt.Sprintf("step %q: execute:config requires Command[path, content], got %d args", step.ID, len(step.Command)),
			}
		}
		if step.Command[0] != step.Capability.Scope {
			return ValidationResult{
				Valid:  false,
				Reason: fmt.Sprintf("step %q: execute:config Command[0] path %q does not match Capability.Scope %q", step.ID, step.Command[0], step.Capability.Scope),
			}
		}

	case strings.HasPrefix(capType, "network:outbound"):
		// For outbound requests: the host in Command[1] URL must match Capability.Scope.
		if len(step.Command) < 2 {
			return ValidationResult{
				Valid:  false,
				Reason: fmt.Sprintf("step %q: network:outbound requires Command[method, url], got %d args", step.ID, len(step.Command)),
			}
		}
		u, err := url.Parse(step.Command[1])
		if err != nil {
			return ValidationResult{
				Valid:  false,
				Reason: fmt.Sprintf("step %q: network:outbound Command[1] is not a valid URL: %v", step.ID, err),
			}
		}
		// Scope may be a hostname or "*" (wildcard).
		if step.Capability.Scope != "*" && u.Hostname() != step.Capability.Scope {
			return ValidationResult{
				Valid:  false,
				Reason: fmt.Sprintf("step %q: network:outbound URL host %q does not match Capability.Scope %q", step.ID, u.Hostname(), step.Capability.Scope),
			}
		}
	}

	return ValidationResult{Valid: true}
}

// checkManifest looks up any absolute filesystem path arguments in the system
// manifest and enforces path-level policies.
func (v *CommandValidator) checkManifest(step types.ActionStep) ValidationResult {
	isConfigWrite := strings.HasPrefix(step.Capability.Type, "execute:config")

	for i, arg := range step.Command {
		if isConfigWrite && i > 0 {
			break
		}
		if !strings.HasPrefix(arg, "/") {
			continue
		}
		entry := v.manifest.Lookup(arg)
		if entry == nil {
			continue
		}
		if entry.Sensitivity == "critical" {
			return ValidationResult{
				Valid:  false,
				Reason: fmt.Sprintf("step %q: path %q has critical sensitivity and cannot be accessed by the agent", step.ID, arg),
			}
		}
		if entry.RequiresPAM {
			return ValidationResult{
				Valid:  false,
				Reason: fmt.Sprintf("step %q: path %q requires PAM authentication (sensitivity: %s)", step.ID, arg, entry.Sensitivity),
			}
		}
	}
	return ValidationResult{Valid: true}
}
