package capability

import (
	"fmt"
	"net/url"
	"os/exec"
	"strings"

	"github.com/isellar/hyperios/internal/manifest"
	"github.com/isellar/hyperios/internal/types"
)

var shellMetacharacters = []string{"|", ";", "&&", "||", "$(", "`", ">", "<", "&", "\n", "\r"}

type ValidationResult struct {
	Valid  bool
	Reason string
}

type CommandValidator struct {
	registry *Registry
	manifest *manifest.Store
}

func NewCommandValidator(registry *Registry) *CommandValidator {
	return &CommandValidator{registry: registry}
}

func (v *CommandValidator) WithManifest(store *manifest.Store) *CommandValidator {
	return &CommandValidator{registry: v.registry, manifest: store}
}

func (v *CommandValidator) Validate(step types.ActionStep) ValidationResult {
	if result := v.checkStructural(step); !result.Valid {
		return result
	}

	if result := v.checkAllowlist(step); !result.Valid {
		return result
	}

	if result := v.checkScopeConsistency(step); !result.Valid {
		return result
	}

	if v.manifest != nil {
		if result := v.checkManifest(step); !result.Valid {
			return result
		}
	}

	return ValidationResult{Valid: true}
}

func (v *CommandValidator) checkStructural(step types.ActionStep) ValidationResult {
	if len(step.Command) == 0 {
		return ValidationResult{
			Valid:  false,
			Reason: fmt.Sprintf("step %q: Command is empty — every step must specify a literal command", step.ID),
		}
	}

	isConfigWrite := strings.HasPrefix(step.Capability.Type, "execute:config")

	for i, arg := range step.Command {
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

	capType := step.Capability.Type
	if strings.HasPrefix(capType, "execute:config") || strings.HasPrefix(capType, "network:outbound") {
		return ValidationResult{Valid: true}
	}

	binary := step.Command[0]
	if _, err := exec.LookPath(binary); err != nil {
		return ValidationResult{
			Valid:  false,
			Reason: fmt.Sprintf("step %q: binary %q not found in PATH: %v", step.ID, binary, err),
		}
	}

	return ValidationResult{Valid: true}
}

func (v *CommandValidator) checkAllowlist(step types.ActionStep) ValidationResult {
	if v.registry == nil {
		return ValidationResult{Valid: true}
	}

	capType := step.Capability.Type

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

	if !v.registry.Check(step.Capability) {
		return ValidationResult{
			Valid:  false,
			Reason: fmt.Sprintf("step %q: capability %s %s is not on the allowlist", step.ID, capType, step.Capability.Scope),
		}
	}

	return ValidationResult{Valid: true}
}

func (v *CommandValidator) checkScopeConsistency(step types.ActionStep) ValidationResult {
	capType := step.Capability.Type

	switch {
	case strings.HasPrefix(capType, "execute:config"):
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
		if step.Capability.Scope != "*" && u.Hostname() != step.Capability.Scope {
			return ValidationResult{
				Valid:  false,
				Reason: fmt.Sprintf("step %q: network:outbound URL host %q does not match Capability.Scope %q", step.ID, u.Hostname(), step.Capability.Scope),
			}
		}
	}

	return ValidationResult{Valid: true}
}

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
