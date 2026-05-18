package router

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/isellar/hyperios/internal/cache"
	"github.com/isellar/hyperios/internal/config"
	"github.com/isellar/hyperios/internal/module"
	"github.com/isellar/hyperios/internal/types"
	"gopkg.in/yaml.v3"
)

// GeneratorConfig holds configuration for the template generator.
type GeneratorConfig struct {
	MinClusterSize int
	MinSuccessRate float64
	AutoApprove    bool
	PendingPath    string
	TemplatesPath  string
}

// DefaultGeneratorConfig returns a GeneratorConfig with safe defaults.
func DefaultGeneratorConfig(dataDir string) GeneratorConfig {
	return GeneratorConfig{
		MinClusterSize: 3,
		MinSuccessRate: 0.8,
		AutoApprove:    false,
		PendingPath:    filepath.Join(dataDir, "generated_templates.yaml"),
		TemplatesPath:  filepath.Join(dataDir, "templates.yaml"),
	}
}

// GeneratorConfigFrom merges user config with defaults.
func GeneratorConfigFrom(cfg *config.Config, dataDir string) GeneratorConfig {
	gc := DefaultGeneratorConfig(dataDir)
	if cfg.Generator.MinClusterSize > 0 {
		gc.MinClusterSize = cfg.Generator.MinClusterSize
	}
	if cfg.Generator.MinSuccessRate > 0 {
		gc.MinSuccessRate = cfg.Generator.MinSuccessRate
	}
	if cfg.Generator.AutoApprove {
		gc.AutoApprove = true
	}
	return gc
}

// GeneratedTemplate represents a template auto-generated from cached plans.
type GeneratedTemplate struct {
	Name          string         `yaml:"name"`
	Patterns      []string       `yaml:"patterns"`
	Plan          TemplatePlan   `yaml:"plan"`
	SourceCount   int            `yaml:"source_count"`
	Confidence    float64        `yaml:"confidence"`
	SourceIntents []string       `yaml:"source_intents"`
	GeneratedAt   time.Time      `yaml:"generated_at"`
}

// SlotInfo describes a variable position found across plans in a cluster.
type SlotInfo struct {
	Position int      // index in command array
	Name     string   // e.g., "source", "destination"
	Values   []string // concrete values from each plan (ordered by cluster)
}

// pendingFile represents the structure of generated_templates.yaml.
type pendingFile struct {
	Pending []GeneratedTemplate `yaml:"pending"`
}

// Generator extracts reusable templates from cached plans.
type Generator struct {
	cache    *cache.PlanCache
	registry *TemplateRegistry
	config   GeneratorConfig
	state    *generatorState
}

// NewGenerator creates a new Generator.
func NewGenerator(cfg GeneratorConfig, cache *cache.PlanCache, registry *TemplateRegistry) *Generator {
	g := &Generator{
		cache:    cache,
		registry: registry,
		config:   cfg,
	}
	return g
}

// NewGeneratorWithMetrics creates a Generator with metrics tracking enabled.
func NewGeneratorWithMetrics(cfg GeneratorConfig, cache *cache.PlanCache, registry *TemplateRegistry, metricsPath, retiredPath string) *Generator {
	g := NewGenerator(cfg, cache, registry)
	initGeneratorState(g, metricsPath, retiredPath)
	return g
}

// Run executes the full template generation pipeline.
// Returns generated templates (empty slice if none).
func (g *Generator) Run() ([]GeneratedTemplate, error) {
	entries := g.cache.All()
	if len(entries) == 0 {
		return nil, nil
	}

	// Stage 1: Cluster by plan structure
	clusters := g.clusterBySignature(entries)

	var results []GeneratedTemplate
	for _, cluster := range clusters {
		tmpl, err := g.generateFromCluster(cluster)
		if err != nil {
			continue
		}
		results = append(results, *tmpl)
	}

	return results, nil
}

// PlanSignature returns a structural fingerprint of a plan.
func PlanSignature(plan *types.ActionPlan) string {
	parts := make([]string, len(plan.Steps))
	for i, step := range plan.Steps {
		deps := strings.Join(step.DependsOn, ",")
		if deps == "" {
			deps = "-"
		}
		parts[i] = fmt.Sprintf("%s|%s|%s",
			step.Capability.Type,
			step.OnFailure,
			deps,
		)
	}
	return strings.Join(parts, "||")
}

// clusterBySignature groups cached plans by structural signature.
// Only includes clusters meeting min size and success rate thresholds.
func (g *Generator) clusterBySignature(entries map[string]*cache.CachedPlan) [][]*cache.CachedPlan {
	sigMap := make(map[string][]*cache.CachedPlan)
	for _, entry := range entries {
		if entry.TotalExecs < 1 {
			continue
		}
		if entry.SuccessRate < g.config.MinSuccessRate {
			continue
		}
		if len(entry.Plan.Steps) == 0 {
			continue
		}
		sig := PlanSignature(entry.Plan)
		sigMap[sig] = append(sigMap[sig], entry)
	}

	var clusters [][]*cache.CachedPlan
	for _, plans := range sigMap {
		if len(plans) >= g.config.MinClusterSize {
			// Sort by intent for deterministic output
			sort.Slice(plans, func(i, j int) bool {
				return plans[i].Intent < plans[j].Intent
			})
			clusters = append(clusters, plans)
		}
	}

	// Sort clusters by size descending for deterministic output
	sort.Slice(clusters, func(i, j int) bool {
		return len(clusters[i]) > len(clusters[j])
	})

	return clusters
}

// generateFromCluster attempts to generate a template from a cluster of plans.
// Returns nil if the cluster cannot produce a valid template.
func (g *Generator) generateFromCluster(cluster []*cache.CachedPlan) (*GeneratedTemplate, error) {
	// Stage 2: Extract variable slots (supports single and multi-slot)
	slots, err := extractPlanSlots(cluster)
	if err != nil {
		return nil, err
	}

	if len(slots) == 0 {
		return nil, fmt.Errorf("no variable slots found")
	}

	if len(slots) > 3 {
		return nil, fmt.Errorf("too many variable positions (%d), max is 3", len(slots))
	}

	var patterns []string
	var tmplPlan TemplatePlan

	if len(slots) == 1 {
		// Single-slot: use existing logic
		slotPos := slots[0].Position
		slotName := slots[0].Name
		patterns = g.generatePatterns(cluster, slotPos, slotName)
		tmplPlan = g.buildTemplatePlan(cluster[0].Plan, slotPos, slotName)
	} else {
		// Multi-slot: use new logic
		patterns = g.generateMultiSlotPatterns(cluster, slots)
		tmplPlan = g.buildMultiSlotTemplatePlan(cluster[0].Plan, slots)
	}

	if len(patterns) == 0 {
		return nil, fmt.Errorf("no patterns generated")
	}

	// Generate template name
	name := g.generateTemplateName(tmplPlan.Name)

	// Collect source intents
	sourceIntents := make([]string, len(cluster))
	for i, c := range cluster {
		sourceIntents[i] = c.Intent
	}

	tmpl := &GeneratedTemplate{
		Name:          name,
		Patterns:      patterns,
		Plan:          tmplPlan,
		SourceCount:   len(cluster),
		Confidence:    1.0,
		SourceIntents: sourceIntents,
		GeneratedAt:   time.Now(),
	}

	// Stage 4: Validate
	if len(slots) == 1 {
		if !g.validateTemplate(tmpl, cluster, slots[0].Position, slots[0].Name) {
			return nil, fmt.Errorf("template validation failed")
		}
	} else {
		if !validateMultiSlotTemplate(tmpl, cluster, slots) {
			return nil, fmt.Errorf("multi-slot template validation failed")
		}
	}

	return tmpl, nil
}

// extractSlot finds the single variable position across all commands in the cluster.
// Returns the position, slot name, or error if not a valid single-slot cluster.
func (g *Generator) extractSlot(cluster []*cache.CachedPlan) (int, string, error) {
	if len(cluster) == 0 {
		return -1, "", fmt.Errorf("empty cluster")
	}

	// Find the command with the most positions to use as reference
	var maxLen int
	for _, c := range cluster {
		if len(c.Plan.Steps) == 0 {
			continue
		}
		// Check first step's command
		if len(c.Plan.Steps[0].Command) > maxLen {
			maxLen = len(c.Plan.Steps[0].Command)
		}
	}

	if maxLen == 0 {
		return -1, "", fmt.Errorf("no commands found")
	}

	// For single-step plans, find variable positions in the command array
	// We need to check all steps, but for Phase 5A we focus on single-step plans
	variablePositions := make(map[int]bool)

	for stepIdx := 0; stepIdx < len(cluster[0].Plan.Steps); stepIdx++ {
		refCmd := cluster[0].Plan.Steps[stepIdx].Command
		if len(refCmd) == 0 {
			continue
		}

		for pos := 0; pos < len(refCmd); pos++ {
			refVal := refCmd[pos]
			isVariable := false
			for _, c := range cluster[1:] {
				if stepIdx >= len(c.Plan.Steps) {
					continue
				}
				cmd := c.Plan.Steps[stepIdx].Command
				if pos >= len(cmd) {
					continue
				}
				if cmd[pos] != refVal {
					isVariable = true
					break
				}
			}
			if isVariable {
				variablePositions[pos] = true
			}
		}
	}

	if len(variablePositions) == 0 {
		return -1, "", fmt.Errorf("no variable positions found")
	}

	if len(variablePositions) > 1 {
		return -1, "", fmt.Errorf("multiple variable positions (%d), skipping multi-slot", len(variablePositions))
	}

	// Get the single variable position
	var slotPos int
	for pos := range variablePositions {
		slotPos = pos
		break
	}

	// Derive slot name from capability scope
	slotName := g.deriveSlotName(cluster[0].Plan.Steps[0])
	if slotName == "" {
		slotName = fmt.Sprintf("arg%d", slotPos+1)
	}

	return slotPos, slotName, nil
}

// deriveSlotName extracts the placeholder name from a step's capability scope.
// "apt:{package}" → "package", "systemctl:start:{service}" → "service"
func (g *Generator) deriveSlotName(step types.ActionStep) string {
	scope := step.Capability.Scope
	start := strings.Index(scope, "{")
	end := strings.Index(scope, "}")
	if start >= 0 && end > start {
		return scope[start+1 : end]
	}
	return ""
}

// generatePatterns creates regex patterns from intents by replacing slot values.
func (g *Generator) generatePatterns(cluster []*cache.CachedPlan, slotPos int, slotName string) []string {
	patternSet := make(map[string]bool)

	for _, c := range cluster {
		// Extract slot value from the first step's command
		if len(c.Plan.Steps) == 0 || len(c.Plan.Steps[0].Command) <= slotPos {
			continue
		}
		slotValue := c.Plan.Steps[0].Command[slotPos]

		pattern := generatePattern(c.Intent, slotValue, slotName)
		if pattern != "" {
			patternSet[pattern] = true
		}
	}

	patterns := make([]string, 0, len(patternSet))
	for p := range patternSet {
		patterns = append(patterns, p)
	}
	sort.Strings(patterns)
	return patterns
}

// generatePattern creates a regex pattern by replacing the slot value in the intent.
func generatePattern(intent, slotValue, slotName string) string {
	idx := strings.Index(strings.ToLower(intent), strings.ToLower(slotValue))
	if idx < 0 {
		return ""
	}
	prefix := intent[:idx]
	suffix := intent[idx+len(slotValue):]
	// If suffix is non-empty and > 2 words, skip — likely noise
	if len(strings.Fields(suffix)) > 2 {
		return ""
	}
	pattern := regexp.QuoteMeta(prefix) + "(?P<" + slotName + ">[a-zA-Z0-9_.-]+)" + regexp.QuoteMeta(suffix)
	return pattern
}

// buildTemplatePlan creates a TemplatePlan from a cached plan with placeholders.
func (g *Generator) buildTemplatePlan(plan *types.ActionPlan, slotPos int, slotName string) TemplatePlan {
	tmplPlan := TemplatePlan{
		Name:  "auto_" + plan.Steps[0].Capability.Type,
		Steps: make([]TemplateStep, len(plan.Steps)),
	}

	for i, step := range plan.Steps {
		ts := TemplateStep{
			ID:          step.ID,
			Description: step.Description,
			Capability:  step.Capability,
			Reversible:  step.Reversible,
			OnFailure:   step.OnFailure,
		}
		if len(step.DependsOn) > 0 {
			ts.DependsOn = make([]string, len(step.DependsOn))
			copy(ts.DependsOn, step.DependsOn)
		}

		// Replace slot value with placeholder in command
		ts.Command = make([]string, len(step.Command))
		copy(ts.Command, step.Command)
		if slotPos < len(ts.Command) {
			ts.Command[slotPos] = "{" + slotName + "}"
		}

		// Replace in capability scope
		ts.Capability.Scope = strings.ReplaceAll(ts.Capability.Scope, ts.Command[slotPos], "{"+slotName+"}")
		// Also try direct replacement if scope has the old value
		if slotPos < len(step.Command) {
			ts.Capability.Scope = strings.ReplaceAll(ts.Capability.Scope, step.Command[slotPos], "{"+slotName+"}")
		}

		tmplPlan.Steps[i] = ts
	}

	return tmplPlan
}

// generateTemplateName creates a unique template name, handling collisions.
func (g *Generator) generateTemplateName(base string) string {
	// Check existing templates for collision
	existing := make(map[string]bool)
	for _, tmpl := range g.registry.templates {
		existing[tmpl.Name] = true
	}

	name := "auto_" + base
	if !existing[name] {
		return name
	}

	for i := 1; i <= 999; i++ {
		candidate := fmt.Sprintf("%s_%03d", name, i)
		if !existing[candidate] {
			return candidate
		}
	}

	return fmt.Sprintf("%s_%d", name, time.Now().Unix())
}

// validateTemplate fills the template with each cached plan's slot value and verifies the result.
func (g *Generator) validateTemplate(tmpl *GeneratedTemplate, cluster []*cache.CachedPlan, slotPos int, slotName string) bool {
	for _, cached := range cluster {
		if len(cached.Plan.Steps) == 0 || len(cached.Plan.Steps[0].Command) <= slotPos {
			return false
		}
		slotValue := cached.Plan.Steps[0].Command[slotPos]

		filled := g.registry.Fill(&TemplateEntry{
			Name:     tmpl.Name,
			Patterns: tmpl.Patterns,
			Plan:     tmpl.Plan,
		}, map[string]string{slotName: slotValue})

		if !g.plansMatch(filled, cached.Plan, slotPos, slotName) {
			return false
		}
	}
	return true
}

// plansMatch compares a filled template plan against a cached plan.
func (g *Generator) plansMatch(filled *types.ActionPlan, cached *types.ActionPlan, slotPos int, slotName string) bool {
	if len(filled.Steps) != len(cached.Steps) {
		return false
	}

	for i := range filled.Steps {
		fStep := filled.Steps[i]
		cStep := cached.Steps[i]

		if fStep.Capability.Type != cStep.Capability.Type {
			return false
		}
		if fStep.OnFailure != cStep.OnFailure {
			return false
		}
		if len(fStep.Command) != len(cStep.Command) {
			return false
		}
		for j := range fStep.Command {
			if j == slotPos {
				continue // skip the slot position
			}
			if fStep.Command[j] != cStep.Command[j] {
				return false
			}
		}
	}

	return true
}

// ── Multi-Slot Functions (Phase 5B) ──────────────────────────────────────────

// extractPlanSlots finds all variable positions across all commands in the cluster.
// Returns SlotInfo for each position where values differ.
// Skips multi-step plans (Phase 5B scope: single-step only).
func extractPlanSlots(cluster []*cache.CachedPlan) ([]SlotInfo, error) {
	if len(cluster) == 0 {
		return nil, fmt.Errorf("empty cluster")
	}

	firstPlan := cluster[0].Plan
	if len(firstPlan.Steps) == 0 {
		return nil, fmt.Errorf("no steps in plan")
	}

	if len(firstPlan.Steps) != 1 {
		return nil, fmt.Errorf("multi-step plans not supported in Phase 5B")
	}

	step := firstPlan.Steps[0]
	cmdLen := len(step.Command)
	if cmdLen == 0 {
		return nil, fmt.Errorf("no commands found")
	}

	// Verify all plans have same command length (they should — same signature)
	for _, c := range cluster {
		if len(c.Plan.Steps) != 1 || len(c.Plan.Steps[0].Command) != cmdLen {
			return nil, fmt.Errorf("command length mismatch across cluster")
		}
	}

	var slots []SlotInfo
	for pos := 0; pos < cmdLen; pos++ {
		var values []string
		allSame := true
		firstValue := cluster[0].Plan.Steps[0].Command[pos]

		for _, cached := range cluster {
			val := cached.Plan.Steps[0].Command[pos]
			values = append(values, val)
			if val != firstValue {
				allSame = false
			}
		}

		if !allSame {
			// Check for empty slot values
			hasEmpty := false
			for _, v := range values {
				if v == "" {
					hasEmpty = true
					break
				}
			}
			if hasEmpty {
				return nil, fmt.Errorf("empty slot value at position %d", pos)
			}

			slotName := deriveSlotNameByPosition(cluster, pos)
			slots = append(slots, SlotInfo{
				Position: pos,
				Name:     slotName,
				Values:   values,
			})
		}
	}

	if len(slots) == 0 {
		return nil, fmt.Errorf("no variable positions found")
	}

	return slots, nil
}

// extractPlaceholders finds all {name} patterns in a scope string.
func extractPlaceholders(scope string) []string {
	re := regexp.MustCompile(`\{([^}]+)\}`)
	matches := re.FindAllStringSubmatch(scope, -1)
	var names []string
	for _, m := range matches {
		names = append(names, m[1])
	}
	return names
}

// deriveSlotNameByPosition derives a meaningful slot name from capability scope placeholders.
// Maps the nth variable position to the nth placeholder in the scope.
func deriveSlotNameByPosition(cluster []*cache.CachedPlan, position int) string {
	scope := cluster[0].Plan.Steps[0].Capability.Scope
	placeholders := extractPlaceholders(scope)

	// Count how many variable positions exist before this one
	variableCount := 0
	firstPlan := cluster[0].Plan.Steps[0]
	for pos := 0; pos < position; pos++ {
		if pos < len(firstPlan.Command) {
			refVal := firstPlan.Command[pos]
			isVariable := false
			for _, c := range cluster[1:] {
				if pos < len(c.Plan.Steps[0].Command) && c.Plan.Steps[0].Command[pos] != refVal {
					isVariable = true
					break
				}
			}
			if isVariable {
				variableCount++
			}
		}
	}

	if len(placeholders) > variableCount {
		return placeholders[variableCount]
	}
	return fmt.Sprintf("arg%d", position+1)
}

// generateMultiSlotPatterns creates regex patterns from intents by replacing ALL slot values.
func (g *Generator) generateMultiSlotPatterns(cluster []*cache.CachedPlan, slots []SlotInfo) []string {
	patternSet := make(map[string]bool)

	for i, c := range cluster {
		pattern := generateMultiSlotPattern(c.Intent, slots, i)
		if pattern != "" {
			patternSet[pattern] = true
		}
	}

	patterns := make([]string, 0, len(patternSet))
	for p := range patternSet {
		patterns = append(patterns, p)
	}
	sort.Strings(patterns)
	return patterns
}

// generateMultiSlotPattern creates a regex pattern by replacing all slot values with capture groups.
// Replaces longer values first to avoid partial matches.
func generateMultiSlotPattern(intent string, slots []SlotInfo, planIndex int) string {
	// Sort slots by value length (descending) to replace longer strings first
	sortedSlots := make([]SlotInfo, len(slots))
	copy(sortedSlots, slots)
	sort.Slice(sortedSlots, func(i, j int) bool {
		return len(sortedSlots[i].Values[planIndex]) > len(sortedSlots[j].Values[planIndex])
	})

	// Find all positions in the original intent
	type repl struct {
		start       int
		length      int
		replacement string
	}
	var repls []repl

	// Use a mutable copy to mark replaced regions
	working := []rune(intent)
	for _, slot := range sortedSlots {
		value := slot.Values[planIndex]
		lowerIntent := strings.ToLower(string(working))
		lowerValue := strings.ToLower(value)
		idx := strings.Index(lowerIntent, lowerValue)
		if idx < 0 {
			return ""
		}
		suffix := string(working[idx+len(value):])
		if len(strings.Fields(suffix)) > 5 {
			return ""
		}
		replStr := fmt.Sprintf("(?P<%s>[^\\s]+)", slot.Name)
		repls = append(repls, repl{
			start:       idx,
			length:      len(value),
			replacement: replStr,
		})
		// Mark replaced region with a unique marker to prevent re-matching
		for i := idx; i < idx+len(value) && i < len(working); i++ {
			working[i] = '\x00'
		}
	}

	// Sort by position ascending
	sort.Slice(repls, func(i, j int) bool {
		return repls[i].start < repls[j].start
	})

	// Build result by concatenating segments
	var sb strings.Builder
	lastEnd := 0
	for _, r := range repls {
		sb.WriteString(intent[lastEnd:r.start])
		sb.WriteString(r.replacement)
		lastEnd = r.start + r.length
	}
	sb.WriteString(intent[lastEnd:])

	return sb.String()
}

// buildMultiSlotTemplatePlan creates a TemplatePlan with multiple slot placeholders.
func (g *Generator) buildMultiSlotTemplatePlan(plan *types.ActionPlan, slots []SlotInfo) TemplatePlan {
	tmplPlan := TemplatePlan{
		Name:  "auto_" + plan.Steps[0].Capability.Type,
		Steps: make([]TemplateStep, len(plan.Steps)),
	}

	for i, step := range plan.Steps {
		ts := TemplateStep{
			ID:          step.ID,
			Description: step.Description,
			Capability:  step.Capability,
			Reversible:  step.Reversible,
			OnFailure:   step.OnFailure,
		}
		if len(step.DependsOn) > 0 {
			ts.DependsOn = make([]string, len(step.DependsOn))
			copy(ts.DependsOn, step.DependsOn)
		}

		// Replace slot values with placeholders in command
		ts.Command = make([]string, len(step.Command))
		copy(ts.Command, step.Command)
		for _, slot := range slots {
			if slot.Position < len(ts.Command) {
				ts.Command[slot.Position] = "{" + slot.Name + "}"
			}
		}

		// Replace in capability scope
		for _, slot := range slots {
			if slot.Position < len(step.Command) {
				ts.Capability.Scope = strings.ReplaceAll(ts.Capability.Scope, step.Command[slot.Position], "{"+slot.Name+"}")
			}
		}

		tmplPlan.Steps[i] = ts
	}

	return tmplPlan
}

// validateMultiSlotTemplate fills the template with all slot values from each cached plan.
func validateMultiSlotTemplate(tmpl *GeneratedTemplate, cluster []*cache.CachedPlan, slots []SlotInfo) bool {
	tr := &TemplateRegistry{}
	for i, cached := range cluster {
		slotValues := make(map[string]string)
		for _, slot := range slots {
			slotValues[slot.Name] = slot.Values[i]
		}

		filled := tr.Fill(&TemplateEntry{
			Name:     tmpl.Name,
			Patterns: tmpl.Patterns,
			Plan:     tmpl.Plan,
		}, slotValues)

		if !multiSlotPlansMatch(filled, cached.Plan, slots) {
			return false
		}
	}
	return true
}

// multiSlotPlansMatch compares a filled template plan against a cached plan for multi-slot.
func multiSlotPlansMatch(filled *types.ActionPlan, cached *types.ActionPlan, slots []SlotInfo) bool {
	if len(filled.Steps) != len(cached.Steps) {
		return false
	}

	// Build set of slot positions for quick lookup
	slotPositions := make(map[int]bool)
	for _, slot := range slots {
		slotPositions[slot.Position] = true
	}

	for i := range filled.Steps {
		fStep := filled.Steps[i]
		cStep := cached.Steps[i]

		if fStep.Capability.Type != cStep.Capability.Type {
			return false
		}
		if fStep.OnFailure != cStep.OnFailure {
			return false
		}
		if len(fStep.Command) != len(cStep.Command) {
			return false
		}
		for j := range fStep.Command {
			if slotPositions[j] {
				continue // skip slot positions
			}
			if fStep.Command[j] != cStep.Command[j] {
				return false
			}
		}
	}

	return true
}

// AutoDeploy appends a generated template directly to templates.yaml.
func (g *Generator) AutoDeploy(t GeneratedTemplate) error {
	if err := os.MkdirAll(filepath.Dir(g.config.TemplatesPath), 0o755); err != nil {
		return fmt.Errorf("create templates dir: %w", err)
	}

	// Read existing templates
	var existing struct {
		Templates map[string]TemplateEntry `yaml:"templates"`
	}
	data, err := os.ReadFile(g.config.TemplatesPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read templates: %w", err)
		}
		existing.Templates = make(map[string]TemplateEntry)
	} else {
		if err := yaml.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("parse templates: %w", err)
		}
		if existing.Templates == nil {
			existing.Templates = make(map[string]TemplateEntry)
		}
	}

	// Add the new template
	existing.Templates[t.Name] = TemplateEntry{
		Name:     t.Name,
		Patterns: t.Patterns,
		Plan:     t.Plan,
	}

	out, err := yaml.Marshal(&existing)
	if err != nil {
		return fmt.Errorf("marshal templates: %w", err)
	}

	if err := os.WriteFile(g.config.TemplatesPath, out, 0o644); err != nil {
		return fmt.Errorf("write templates: %w", err)
	}

	return nil
}

// SavePending writes a generated template to the pending file.
func (g *Generator) SavePending(t GeneratedTemplate) error {
	if err := os.MkdirAll(filepath.Dir(g.config.PendingPath), 0o755); err != nil {
		return fmt.Errorf("create pending dir: %w", err)
	}

	var pf pendingFile
	data, err := os.ReadFile(g.config.PendingPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read pending: %w", err)
		}
		pf.Pending = []GeneratedTemplate{}
	} else {
		if err := yaml.Unmarshal(data, &pf); err != nil {
			return fmt.Errorf("parse pending: %w", err)
		}
		if pf.Pending == nil {
			pf.Pending = []GeneratedTemplate{}
		}
	}

	pf.Pending = append(pf.Pending, t)

	out, err := yaml.Marshal(&pf)
	if err != nil {
		return fmt.Errorf("marshal pending: %w", err)
	}

	if err := os.WriteFile(g.config.PendingPath, out, 0o644); err != nil {
		return fmt.Errorf("write pending: %w", err)
	}

	return nil
}

// ListPending returns all pending generated templates.
func (g *Generator) ListPending() ([]GeneratedTemplate, error) {
	data, err := os.ReadFile(g.config.PendingPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read pending: %w", err)
	}

	var pf pendingFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("parse pending: %w", err)
	}

	return pf.Pending, nil
}

// Approve moves a pending template to the main templates file and removes it from pending.
func (g *Generator) Approve(name string) error {
	pending, err := g.ListPending()
	if err != nil {
		return err
	}

	var target *GeneratedTemplate
	var idx = -1
	for i, t := range pending {
		if t.Name == name {
			target = &pending[i]
			idx = i
			break
		}
	}
	if target == nil {
		return fmt.Errorf("pending template %q not found", name)
	}

	// Deploy to templates
	if err := g.AutoDeploy(*target); err != nil {
		return err
	}

	// Remove from pending
	pending = append(pending[:idx], pending[idx+1:]...)
	pf := pendingFile{Pending: pending}
	out, err := yaml.Marshal(&pf)
	if err != nil {
		return fmt.Errorf("marshal pending: %w", err)
	}
	if err := os.WriteFile(g.config.PendingPath, out, 0o644); err != nil {
		return fmt.Errorf("write pending: %w", err)
	}

	return nil
}

// Reject removes a pending template without deploying it.
func (g *Generator) Reject(name string) error {
	pending, err := g.ListPending()
	if err != nil {
		return err
	}

	var idx = -1
	for i, t := range pending {
		if t.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("pending template %q not found", name)
	}

	pending = append(pending[:idx], pending[idx+1:]...)
	pf := pendingFile{Pending: pending}
	out, err := yaml.Marshal(&pf)
	if err != nil {
		return fmt.Errorf("marshal pending: %w", err)
	}
	if err := os.WriteFile(g.config.PendingPath, out, 0o644); err != nil {
		return fmt.Errorf("write pending: %w", err)
	}

	return nil
}

// ── Phase 5C: Metrics, Self-Tuning, Lifecycle, Module Interface ──────────────

// TemplateMetrics tracks per-template performance.
type TemplateMetrics struct {
	Name           string    `json:"name"`
	CreatedAt      time.Time `json:"created_at"`
	LastUsed       time.Time `json:"last_used"`
	HitCount       int       `json:"hit_count"`
	ExecCount      int       `json:"exec_count"`
	SuccessCount   int       `json:"success_count"`
	FailCount      int       `json:"fail_count"`
	FalsePositives int       `json:"false_positives"`
	AvgLatencyMs   int       `json:"avg_latency_ms"`
	Status         string    `json:"status"` // "active", "trusted", "review", "retired"
}

// GeneratorMetrics tracks generator-level performance.
type GeneratorMetrics struct {
	TotalTemplates    int               `json:"total_templates"`
	ActiveTemplates   int               `json:"active_templates"`
	RetiredTemplates  int               `json:"retired_templates"`
	TotalHits         int               `json:"total_hits"`
	HitRate           float64           `json:"hit_rate"`
	FalsePositiveRate float64           `json:"false_positive_rate"`
	AvgConfidence     float64           `json:"avg_confidence"`
	PendingCount      int               `json:"pending_count"`
	LastRun           time.Time         `json:"last_run"`
	Parameters        GeneratorConfig   `json:"parameters"`
	TemplateMetrics   []TemplateMetrics `json:"template_metrics"`
}

// metricsFile represents the structure of template_metrics.json.
type metricsFile struct {
	Generator GeneratorMetrics    `json:"generator"`
	Templates map[string]TemplateMetrics `json:"templates"`
}

// PatternStrictness controls the regex character class for slots.
type PatternStrictness string

const (
	StrictnessStrict   PatternStrictness = "strict"
	StrictnessModerate PatternStrictness = "moderate"
	StrictnessLoose    PatternStrictness = "loose"
)

// Generator extended fields for Phase 5C.
type generatorState struct {
	mu              sync.RWMutex
	metrics         GeneratorMetrics
	templateMetrics map[string]TemplateMetrics
	lastError       error
	metricsPath     string
	retiredPath     string
	totalIntents    int
}

func initGeneratorState(g *Generator, metricsPath, retiredPath string) {
	g.state = &generatorState{
		templateMetrics: make(map[string]TemplateMetrics),
		metricsPath:     metricsPath,
		retiredPath:     retiredPath,
	}
	_ = g.LoadMetrics()
}

// RecordTemplateHit records that a template matched an intent.
func (g *Generator) RecordTemplateHit(name string, latencyMs int) {
	if g.state == nil {
		return
	}
	g.state.mu.Lock()
	defer g.state.mu.Unlock()

	m, ok := g.state.templateMetrics[name]
	if !ok {
		m = TemplateMetrics{
			Name:      name,
			CreatedAt: time.Now(),
			Status:    "active",
		}
	}
	m.HitCount++
	m.LastUsed = time.Now()
	if latencyMs > 0 {
		if m.AvgLatencyMs == 0 {
			m.AvgLatencyMs = latencyMs
		} else {
			m.AvgLatencyMs = (m.AvgLatencyMs + latencyMs) / 2
		}
	}
	g.state.templateMetrics[name] = m
	g.state.totalIntents++
}

// RecordTemplateExecution records the outcome of a template's plan execution.
func (g *Generator) RecordTemplateExecution(name string, success bool) {
	if g.state == nil {
		return
	}
	g.state.mu.Lock()
	defer g.state.mu.Unlock()

	m, ok := g.state.templateMetrics[name]
	if !ok {
		m = TemplateMetrics{
			Name:      name,
			CreatedAt: time.Now(),
			Status:    "active",
		}
	}
	m.ExecCount++
	if success {
		m.SuccessCount++
	} else {
		m.FailCount++
	}
	g.state.templateMetrics[name] = m
}

// RecordTemplateFalsePositive records a false positive match.
func (g *Generator) RecordTemplateFalsePositive(name string) {
	if g.state == nil {
		return
	}
	g.state.mu.Lock()
	defer g.state.mu.Unlock()

	m, ok := g.state.templateMetrics[name]
	if !ok {
		m = TemplateMetrics{
			Name:      name,
			CreatedAt: time.Now(),
			Status:    "active",
		}
	}
	m.FalsePositives++
	g.state.templateMetrics[name] = m
}

// RecordIntent records that an intent was processed (for hit rate calculation).
func (g *Generator) RecordIntent() {
	if g.state == nil {
		return
	}
	g.state.mu.Lock()
	defer g.state.mu.Unlock()
	g.state.totalIntents++
}

// SelfTune runs the self-tuning cycle: adjusts parameters and manages lifecycle.
func (g *Generator) SelfTune() error {
	if g.state == nil {
		return fmt.Errorf("generator state not initialized")
	}

	g.state.mu.Lock()
	defer g.state.mu.Unlock()

	metrics := g.computeMetricsLocked(0)

	// Check for insufficient data
	totalHits := metrics["total_hits"].(int)
	if totalHits < 5 {
		g.state.lastError = nil
		return fmt.Errorf("insufficient data for tuning (total_hits=%d)", totalHits)
	}

	fpRate := metrics["false_positive_rate"].(float64)
	hitRate := metrics["hit_rate"].(float64)
	activeTemplates := metrics["active_templates"].(int)

	// Save previous parameters for rollback
	prevConfig := g.config

	// Tune MinClusterSize
	if fpRate > 0.1 && g.config.MinClusterSize < 10 {
		g.config.MinClusterSize++
	} else if hitRate < 0.05 && g.config.MinClusterSize > 2 {
		g.config.MinClusterSize--
	}

	// Tune MinSuccessRate
	if fpRate > 0.15 && g.config.MinSuccessRate < 0.95 {
		g.config.MinSuccessRate = roundTo2(g.config.MinSuccessRate + 0.05)
	} else if activeTemplates < 5 && g.config.MinSuccessRate > 0.7 {
		g.config.MinSuccessRate = roundTo2(g.config.MinSuccessRate - 0.05)
	}

	// Manage template lifecycle
	if err := g.manageLifecycleLocked(); err != nil {
		g.config = prevConfig
		return fmt.Errorf("lifecycle management failed: %w", err)
	}

	g.state.lastError = nil
	activeCount := 0
	for _, tm := range g.state.templateMetrics {
		if tm.Status != "retired" {
			activeCount++
		}
	}
	g.state.metrics = GeneratorMetrics{
		TotalTemplates:    len(g.state.templateMetrics),
		ActiveTemplates:   activeCount,
		Parameters:        g.config,
		LastRun:           time.Now(),
	}

	if err := g.saveMetricsLocked(); err != nil {
		g.config = prevConfig
		return fmt.Errorf("failed to persist tuning: %w", err)
	}

	return nil
}

func roundTo2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}

func (g *Generator) computeMetricsLocked(window time.Duration) map[string]any {
	totalHits := 0
	totalFP := 0
	totalExec := 0
	totalSuccess := 0
	activeCount := 0
	retiredCount := 0
	totalConfidence := 0.0
	templateCount := len(g.state.templateMetrics)

	now := time.Now()
	var tmplMetrics []TemplateMetrics

	for _, m := range g.state.templateMetrics {
		if window > 0 && now.Sub(m.LastUsed) > window {
			continue
		}
		totalHits += m.HitCount
		totalFP += m.FalsePositives
		totalExec += m.ExecCount
		totalSuccess += m.SuccessCount
		if m.Status == "retired" {
			retiredCount++
		} else {
			activeCount++
		}
		tmplMetrics = append(tmplMetrics, m)
		totalConfidence += 1.0
	}

	hitRate := 0.0
	if g.state.totalIntents > 0 {
		hitRate = float64(totalHits) / float64(g.state.totalIntents)
	}
	fpRate := 0.0
	if totalHits > 0 {
		fpRate = float64(totalFP) / float64(totalHits)
	}
	avgConfidence := 0.0
	if templateCount > 0 {
		avgConfidence = totalConfidence / float64(templateCount)
	}

	pendingCount := 0
	if g.config.PendingPath != "" {
		if pending, err := g.ListPending(); err == nil {
			pendingCount = len(pending)
		}
	}

	return map[string]any{
		"total_templates":     templateCount,
		"active_templates":    activeCount,
		"retired_templates":   retiredCount,
		"total_hits":          totalHits,
		"hit_rate":            hitRate,
		"false_positive_rate": fpRate,
		"avg_confidence":      avgConfidence,
		"pending_count":       pendingCount,
		"last_run":            g.state.metrics.LastRun,
		"min_cluster_size":    g.config.MinClusterSize,
		"min_success_rate":    g.config.MinSuccessRate,
		"template_metrics":    tmplMetrics,
	}
}

func (g *Generator) detectIssues(metrics map[string]any) []string {
	var issues []string

	fpRate := metrics["false_positive_rate"].(float64)
	hitRate := metrics["hit_rate"].(float64)
	activeTemplates := metrics["active_templates"].(int)

	if fpRate > 0.2 {
		issues = append(issues, fmt.Sprintf("high false positive rate: %.2f", fpRate))
	}
	if hitRate < 0.02 {
		issues = append(issues, fmt.Sprintf("low hit rate: %.4f", hitRate))
	}
	if activeTemplates == 0 {
		issues = append(issues, "no active templates — consider lowering MinClusterSize or MinSuccessRate")
	}

	tmplMetrics, ok := metrics["template_metrics"].([]TemplateMetrics)
	if ok {
		for _, tm := range tmplMetrics {
			if tm.ExecCount > 5 {
				successRate := float64(tm.SuccessCount) / float64(tm.ExecCount)
				if successRate < 0.7 {
					issues = append(issues, fmt.Sprintf("template %q has low success rate: %.2f", tm.Name, successRate))
				}
			}
		}
	}

	return issues
}

func (g *Generator) manageLifecycleLocked() error {
	now := time.Now()
	retired := make(map[string]TemplateMetrics)

	for name, m := range g.state.templateMetrics {
		if m.Status == "retired" {
			continue
		}

		// Retirement: unused for 90+ days or FP rate > 0.3
		if now.Sub(m.LastUsed) > 90*24*time.Hour {
			m.Status = "retired"
			g.state.templateMetrics[name] = m
			retired[name] = m
			continue
		}
		if m.ExecCount > 0 {
			fpRate := float64(m.FalsePositives) / float64(m.HitCount)
			if fpRate > 0.3 {
				m.Status = "retired"
				g.state.templateMetrics[name] = m
				retired[name] = m
				continue
			}
		}

		// Promotion: SuccessRate > 0.95 AND HitCount > 20
		if m.ExecCount > 0 {
			successRate := float64(m.SuccessCount) / float64(m.ExecCount)
			if successRate > 0.95 && m.HitCount > 20 {
				m.Status = "trusted"
			}
		}

		// Demotion: SuccessRate < 0.7 AND HitCount > 5
		if m.ExecCount > 5 {
			successRate := float64(m.SuccessCount) / float64(m.ExecCount)
			if successRate < 0.7 {
				m.Status = "review"
			}
		}

		g.state.templateMetrics[name] = m
	}

	// Archive retired templates
	if len(retired) > 0 {
		if err := g.archiveRetired(retired); err != nil {
			return fmt.Errorf("archive retired: %w", err)
		}
	}

	return nil
}

func (g *Generator) archiveRetired(retired map[string]TemplateMetrics) error {
	if g.state.retiredPath == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(g.state.retiredPath), 0o755); err != nil {
		return fmt.Errorf("create retired dir: %w", err)
	}

	var existing struct {
		Retired []TemplateMetrics `yaml:"retired"`
	}
	data, err := os.ReadFile(g.state.retiredPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read retired: %w", err)
		}
		existing.Retired = []TemplateMetrics{}
	} else {
		if err := yaml.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("parse retired: %w", err)
		}
		if existing.Retired == nil {
			existing.Retired = []TemplateMetrics{}
		}
	}

	for _, m := range retired {
		existing.Retired = append(existing.Retired, m)
	}

	out, err := yaml.Marshal(&existing)
	if err != nil {
		return fmt.Errorf("marshal retired: %w", err)
	}

	return os.WriteFile(g.state.retiredPath, out, 0o644)
}

func (g *Generator) saveMetricsLocked() error {
	if g.state.metricsPath == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(g.state.metricsPath), 0o755); err != nil {
		return fmt.Errorf("create metrics dir: %w", err)
	}

	mf := metricsFile{
		Generator: g.state.metrics,
		Templates: g.state.templateMetrics,
	}

	data, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metrics: %w", err)
	}

	return os.WriteFile(g.state.metricsPath, data, 0o644)
}

// SaveMetrics persists metrics to disk.
func (g *Generator) SaveMetrics() error {
	if g.state == nil {
		return fmt.Errorf("generator state not initialized")
	}
	g.state.mu.Lock()
	defer g.state.mu.Unlock()

	m := g.computeMetricsLocked(0)
	activeCount := 0
	retiredCount := 0
	for _, tm := range g.state.templateMetrics {
		if tm.Status == "retired" {
			retiredCount++
		} else {
			activeCount++
		}
	}
	g.state.metrics = GeneratorMetrics{
		TotalTemplates:    len(g.state.templateMetrics),
		ActiveTemplates:   activeCount,
		RetiredTemplates:  retiredCount,
		TotalHits:         m["total_hits"].(int),
		HitRate:           m["hit_rate"].(float64),
		FalsePositiveRate: m["false_positive_rate"].(float64),
		AvgConfidence:     m["avg_confidence"].(float64),
		PendingCount:      m["pending_count"].(int),
		Parameters:        g.config,
		LastRun:           time.Now(),
	}
	return g.saveMetricsLocked()
}

// LoadMetrics loads metrics from disk.
func (g *Generator) LoadMetrics() error {
	if g.state == nil || g.state.metricsPath == "" {
		return nil
	}

	data, err := os.ReadFile(g.state.metricsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read metrics: %w", err)
	}

	var mf metricsFile
	if err := json.Unmarshal(data, &mf); err != nil {
		return fmt.Errorf("parse metrics: %w", err)
	}

	g.state.metrics = mf.Generator
	g.state.templateMetrics = mf.Templates
	if g.state.templateMetrics == nil {
		g.state.templateMetrics = make(map[string]TemplateMetrics)
	}

	return nil
}

// GetTemplateMetrics returns metrics for a specific template.
func (g *Generator) GetTemplateMetrics(name string) (TemplateMetrics, bool) {
	if g.state == nil {
		return TemplateMetrics{}, false
	}
	g.state.mu.RLock()
	defer g.state.mu.RUnlock()
	m, ok := g.state.templateMetrics[name]
	return m, ok
}

// GetAllTemplateMetrics returns all template metrics.
func (g *Generator) GetAllTemplateMetrics() map[string]TemplateMetrics {
	if g.state == nil {
		return nil
	}
	g.state.mu.RLock()
	defer g.state.mu.RUnlock()
	result := make(map[string]TemplateMetrics)
	for k, v := range g.state.templateMetrics {
		result[k] = v
	}
	return result
}

// RetireTemplate manually retires a template.
func (g *Generator) RetireTemplate(name string) error {
	if g.state == nil {
		return fmt.Errorf("generator state not initialized")
	}
	g.state.mu.Lock()
	defer g.state.mu.Unlock()

	m, ok := g.state.templateMetrics[name]
	if !ok {
		return fmt.Errorf("template %q not found in metrics", name)
	}
	m.Status = "retired"
	g.state.templateMetrics[name] = m

	if err := g.archiveRetired(map[string]TemplateMetrics{name: m}); err != nil {
		return err
	}
	return g.saveMetricsLocked()
}

// PromoteTemplate manually promotes a template to trusted.
func (g *Generator) PromoteTemplate(name string) error {
	if g.state == nil {
		return fmt.Errorf("generator state not initialized")
	}
	g.state.mu.Lock()
	defer g.state.mu.Unlock()

	m, ok := g.state.templateMetrics[name]
	if !ok {
		return fmt.Errorf("template %q not found in metrics", name)
	}
	m.Status = "trusted"
	g.state.templateMetrics[name] = m
	return g.saveMetricsLocked()
}

// GetGeneratorMetrics returns the current generator-level metrics.
func (g *Generator) GetGeneratorMetrics() GeneratorMetrics {
	if g.state == nil {
		return GeneratorMetrics{}
	}
	g.state.mu.RLock()
	defer g.state.mu.RUnlock()
	return g.state.metrics
}

// ── Module Interface Implementation ──────────────────────────────────────────

// Name returns the module name.
func (g *Generator) Name() string { return "generator" }

// Report returns metrics and issues for the given time window.
func (g *Generator) Report(ctx context.Context, window time.Duration) (module.ModuleReport, error) {
	if g.state == nil {
		return module.ModuleReport{ModuleName: "generator"}, fmt.Errorf("generator state not initialized")
	}
	g.state.mu.RLock()
	defer g.state.mu.RUnlock()

	metrics := g.computeMetricsLocked(window)
	issues := g.detectIssues(metrics)

	return module.ModuleReport{
		ModuleName: "generator",
		Window:     window,
		Metrics:    metrics,
		Issues:     issues,
	}, nil
}

// Tune applies a tuning change to the generator.
func (g *Generator) Tune(ctx context.Context, change module.TuningChange) error {
	if change.Module != "generator" {
		return fmt.Errorf("wrong module: %s", change.Module)
	}
	if g.state == nil {
		return fmt.Errorf("generator state not initialized")
	}

	g.state.mu.Lock()
	defer g.state.mu.Unlock()

	prevConfig := g.config

	switch change.Path {
	case "min_cluster_size":
		val, ok := change.Value.(int)
		if !ok || val < 2 || val > 10 {
			return fmt.Errorf("invalid min_cluster_size: %v", change.Value)
		}
		g.config.MinClusterSize = val
	case "min_success_rate":
		val, ok := change.Value.(float64)
		if !ok || val < 0.5 || val > 0.95 {
			return fmt.Errorf("invalid min_success_rate: %v", change.Value)
		}
		g.config.MinSuccessRate = val
	case "auto_approve":
		val, ok := change.Value.(bool)
		if !ok {
			return fmt.Errorf("invalid auto_approve: %v", change.Value)
		}
		g.config.AutoApprove = val
	default:
		return fmt.Errorf("unknown tuning path: %s", change.Path)
	}

	if err := g.saveMetricsLocked(); err != nil {
		g.config = prevConfig
		return fmt.Errorf("failed to persist tuning: %w", err)
	}

	return nil
}

// Health returns the current health status of the generator.
func (g *Generator) Health() module.ModuleHealth {
	if g.state == nil {
		return module.ModuleHealth{
			Status:    "degraded",
			Details:   "generator state not initialized",
			Timestamp: time.Now(),
		}
	}
	g.state.mu.RLock()
	defer g.state.mu.RUnlock()

	if g.state.lastError != nil {
		return module.ModuleHealth{
			Status:    "degraded",
			Details:   g.state.lastError.Error(),
			Timestamp: time.Now(),
		}
	}
	return module.ModuleHealth{
		Status:    "healthy",
		Timestamp: time.Now(),
	}
}
