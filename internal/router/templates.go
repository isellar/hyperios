package router

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/isellar/hyperios/internal/types"
	"gopkg.in/yaml.v3"
)

// TemplateStep mirrors types.ActionStep but with template placeholders.
type TemplateStep struct {
	ID          string             `yaml:"id"`
	Description string             `yaml:"description"`
	Capability  types.Capability   `yaml:"capability"`
	Command     []string           `yaml:"command"`
	Reversible  bool               `yaml:"reversible"`
	DependsOn   []string           `yaml:"depends_on,omitempty"`
	OnFailure   string             `yaml:"on_failure,omitempty"`
}

// TemplatePlan is a parameterized action plan.
type TemplatePlan struct {
	Name  string         `yaml:"name"`
	Steps []TemplateStep `yaml:"steps"`
}

// TemplateEntry defines a reusable intent template with pattern matching.
type TemplateEntry struct {
	Name     string       `yaml:"name"`
	Patterns []string     `yaml:"patterns"`
	Plan     TemplatePlan `yaml:"plan"`

	compiled []*regexp.Regexp
}

// TemplateRegistry stores and matches intent templates.
type TemplateRegistry struct {
	templates []*TemplateEntry
}

// NewTemplateRegistry creates a registry, loading templates from YAML if path is provided.
func NewTemplateRegistry(yamlPath string) (*TemplateRegistry, error) {
	tr := &TemplateRegistry{}
	if yamlPath != "" {
		if err := tr.LoadFromFile(yamlPath); err != nil {
			return nil, fmt.Errorf("load templates: %w", err)
		}
	}
	return tr, nil
}

// LoadFromFile loads templates from a YAML file.
func (tr *TemplateRegistry) LoadFromFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read template file: %w", err)
	}

	var config struct {
		Templates map[string]TemplateEntry `yaml:"templates"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("parse templates: %w", err)
	}

	for name, tmpl := range config.Templates {
		if tmpl.Name == "" {
			tmpl.Name = name
		}
		tmpl.compiled = make([]*regexp.Regexp, len(tmpl.Patterns))
		for j, pattern := range tmpl.Patterns {
			re, err := regexp.Compile("(?i)" + pattern)
			if err != nil {
				return fmt.Errorf("compile pattern %q for template %q: %w", pattern, tmpl.Name, err)
			}
			tmpl.compiled[j] = re
		}
		tr.templates = append(tr.templates, &tmpl)
	}

	return nil
}

// LoadFromFileAppend loads templates from a YAML file and appends them to the registry.
// Unlike LoadFromFile, this does not replace existing templates.
func (tr *TemplateRegistry) LoadFromFileAppend(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read template file: %w", err)
	}

	var config struct {
		Templates map[string]TemplateEntry `yaml:"templates"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("parse templates: %w", err)
	}

	for name, tmpl := range config.Templates {
		if tmpl.Name == "" {
			tmpl.Name = name
		}
		tmpl.compiled = make([]*regexp.Regexp, len(tmpl.Patterns))
		for j, pattern := range tmpl.Patterns {
			re, err := regexp.Compile("(?i)" + pattern)
			if err != nil {
				return fmt.Errorf("compile pattern %q for template %q: %w", pattern, tmpl.Name, err)
			}
			tmpl.compiled[j] = re
		}
		tr.templates = append(tr.templates, &tmpl)
	}

	return nil
}

// Match checks if an intent matches any template. Returns the template and extracted slots.
func (tr *TemplateRegistry) Match(intent string) (*TemplateEntry, map[string]string) {
	for _, tmpl := range tr.templates {
		for _, re := range tmpl.compiled {
			match := re.FindStringSubmatch(intent)
			if match != nil {
				slots := extractSlots(re, match)
				return tmpl, slots
			}
		}
	}
	return nil, nil
}

// Fill creates a concrete ActionPlan from a template with slots filled in.
func (tr *TemplateRegistry) Fill(tmpl *TemplateEntry, slots map[string]string) *types.ActionPlan {
	plan := &types.ActionPlan{
		Executor: types.ExecutorLocal,
		Steps:    make([]types.ActionStep, len(tmpl.Plan.Steps)),
	}

	for i, ts := range tmpl.Plan.Steps {
		step := types.ActionStep{
			ID:         ts.ID,
			Reversible: ts.Reversible,
			OnFailure:  ts.OnFailure,
		}

		if len(ts.DependsOn) > 0 {
			step.DependsOn = make([]string, len(ts.DependsOn))
			copy(step.DependsOn, ts.DependsOn)
		}

		step.Description = fillTemplate(ts.Description, slots)
		step.Command = make([]string, len(ts.Command))
		for j, cmd := range ts.Command {
			step.Command[j] = fillTemplate(cmd, slots)
		}

		step.Capability.Type = fillTemplate(ts.Capability.Type, slots)
		step.Capability.Scope = fillTemplate(ts.Capability.Scope, slots)

		plan.Steps[i] = step
	}

	return plan
}

func extractSlots(re *regexp.Regexp, match []string) map[string]string {
	slots := make(map[string]string)
	for i, name := range re.SubexpNames() {
		if i > 0 && name != "" {
			slots[name] = match[i]
		}
	}
	return slots
}

func fillTemplate(s string, slots map[string]string) string {
	for key, value := range slots {
		s = strings.ReplaceAll(s, "{"+key+"}", value)
	}
	return s
}
