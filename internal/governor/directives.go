// Package governor — DirectiveStore manages loading and compliance checking
// of behavioral constraints (directives) for the agent.
//
// Immutable directives are loaded from a read-only system path and cannot be
// altered at runtime. Mutable directives are user-configurable preferences.
// Together they form the behavioral policy the Governor enforces before any
// goal is approved.
package governor

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/isellar/hyperios/internal/types"
	"gopkg.in/yaml.v3"
)

// directivesFileSchema maps to the YAML structure used in
// config/directives-immutable.yaml and config/directives-mutable.yaml.
type directivesFileSchema struct {
	Directives []types.Directive `yaml:"directives"`
}

// DirectiveStore holds loaded directives and answers compliance queries.
// It is safe to create with a zero value; call LoadDirectives before use.
type DirectiveStore struct {
	directives []types.Directive
}

// LoadDirectives reads immutable and mutable directive YAML files and merges
// them into the store, sorted by ascending priority. Missing files are silently
// skipped (non-fatal). Files that exist but are malformed return an error.
func (s *DirectiveStore) LoadDirectives(immutablePath, mutablePath string) error {
	var all []types.Directive

	if immutablePath != "" {
		directives, err := readDirectivesFile(immutablePath)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("directives: load immutable %q: %w", immutablePath, err)
		}
		// Force immutable flag regardless of what the file says.
		for i := range directives {
			directives[i].Immutable = true
		}
		all = append(all, directives...)
	}

	if mutablePath != "" {
		directives, err := readDirectivesFile(mutablePath)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("directives: load mutable %q: %w", mutablePath, err)
		}
		all = append(all, directives...)
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].Priority < all[j].Priority
	})

	s.directives = all
	return nil
}

// AllDirectives returns all loaded directives in priority order (lowest number
// = highest priority).
func (s *DirectiveStore) AllDirectives() []types.Directive {
	out := make([]types.Directive, len(s.directives))
	copy(out, s.directives)
	return out
}

// CheckCompliance tests whether description is compliant with all loaded
// directives. It returns (true, nil) when no violations are detected.
//
// The current implementation uses keyword matching as a fast first-pass heuristic.
// Immutable directives are checked with a stricter keyword set derived from their
// description. Mutable directives are checked as preferences and are treated as
// violated only when the description explicitly contradicts them.
//
// Returns (false, violated) where violated is the slice of violated directive
// descriptions when any violation is found.
func (s *DirectiveStore) CheckCompliance(description string) (bool, []string) {
	lower := strings.ToLower(description)

	var violated []string
	for _, d := range s.directives {
		if s.violatesDirective(lower, d) {
			violated = append(violated, d.Description)
		}
	}

	return len(violated) == 0, violated
}

// violatesDirective returns true if the description (already lowercased)
// appears to violate the given directive.
func (s *DirectiveStore) violatesDirective(lowerDesc string, d types.Directive) bool {
	switch d.ID {
	case "safety-no-harm":
		// Flag intent to cause harm.
		harmKeywords := []string{"harm", "hurt", "damage", "injure", "attack", "kill"}
		return containsAny(lowerDesc, harmKeywords)

	case "safety-no-delete":
		// Flag destructive data-loss operations.
		deleteKeywords := []string{"delete", "rm -rf", "wipe", "destroy", "purge", "erase", "format"}
		return containsAny(lowerDesc, deleteKeywords)

	case "safety-no-compromise":
		// Flag commands that could compromise system security.
		compromiseKeywords := []string{
			"chmod 777", "chmod a+w", "disable firewall", "disable selinux",
			"sudoers", "nopasswd", "visudo", "write to /etc/passwd",
		}
		return containsAny(lowerDesc, compromiseKeywords)

	case "safety-no-secrets":
		// Flag exposure of credentials.
		secretKeywords := []string{
			"print api key", "echo api key", "export secret", "reveal password",
			"print secret", "show credentials", "output credentials",
		}
		return containsAny(lowerDesc, secretKeywords)

	case "safety-no-network-exfil":
		// Flag sending user data to external services without auth.
		exfilKeywords := []string{
			"send user data", "upload user data", "exfiltrate",
			"post to external", "transmit user",
		}
		return containsAny(lowerDesc, exfilKeywords)

	default:
		// For directives without a hard-coded check (e.g. mutable preferences),
		// no violation is detected via keyword matching.
		return false
	}
}

// containsAny returns true if s contains any of the given substrings.
func containsAny(s string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}

// readDirectivesFile reads and parses a directives YAML file.
func readDirectivesFile(path string) ([]types.Directive, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var df directivesFileSchema
	if err := yaml.Unmarshal(data, &df); err != nil {
		return nil, fmt.Errorf("parse %q: %w", path, err)
	}
	return df.Directives, nil
}
