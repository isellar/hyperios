package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/isellar/hyperios/internal/types"
)

func TestWriter_CreateAndAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-session.md")

	w, err := NewWriter(path, "test-session", "install nginx")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	if err := w.WriteStageStart("intent"); err != nil {
		t.Fatalf("WriteStageStart: %v", err)
	}
	if err := w.WriteStageComplete("intent", `{"intent":"install nginx"}`, "hyperi-intent"); err != nil {
		t.Fatalf("WriteStageComplete: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, "Session: test-session") {
		t.Error("expected session ID in header")
	}
	if !strings.Contains(content, "Status: in-progress") {
		t.Error("expected in-progress status")
	}
	if !strings.Contains(content, "stage: intent") {
		t.Error("expected intent stage meta")
	}
	if !strings.Contains(content, "status: completed") {
		t.Error("expected completed status in meta")
	}
	if !strings.Contains(content, "```hyperi-intent") {
		t.Error("expected hyperi-intent fence")
	}
}

func TestWriter_StepResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")

	w, err := NewWriter(path, "sess1", "test intent")
	if err != nil {
		t.Fatal(err)
	}

	step := types.ActionStep{
		ID:          "s1",
		Description: "check nginx",
		Command:     []string{"dpkg", "-l", "nginx"},
		OnFailure:   "skip",
	}

	result := &types.ExecutionResult{
		StepID:   "s1",
		Success:  true,
		Output:   "nginx is installed",
		Duration: 42,
	}

	if err := w.WriteStepVerdict(step, "approved", "no risks identified"); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteStepStart(step); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteStepResult(step, result); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	if !strings.Contains(content, "step_id: s1") {
		t.Error("expected step_id in meta")
	}
	if !strings.Contains(content, "result: success") {
		t.Error("expected result: success in meta")
	}
	if !strings.Contains(content, "```output") {
		t.Error("expected output fence")
	}
	if !strings.Contains(content, "nginx is installed") {
		t.Error("expected command output in output block")
	}
}

func TestParser_BasicParse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "parse-test.md")

	w, err := NewWriter(path, "sess2", "install curl")
	if err != nil {
		t.Fatal(err)
	}

	_ = w.WriteStageStart("intent")
	_ = w.WriteStageComplete("intent", "goal graph", "hyperi-intent")

	step := types.ActionStep{
		ID: "s1", Description: "check curl", Command: []string{"dpkg", "-l", "curl"}, OnFailure: "skip",
	}
	_ = w.WriteStepVerdict(step, "approved", "no risks")
	_ = w.WriteStepStart(step)
	_ = w.WriteStepResult(step, &types.ExecutionResult{StepID: "s1", Success: true, Duration: 10})

	state, err := ParsePlanDoc(path)
	if err != nil {
		t.Fatalf("ParsePlanDoc: %v", err)
	}

	if state.SessionID != "sess2" {
		t.Errorf("expected session sess2, got %q", state.SessionID)
	}
	if state.Status != StatusInProgress {
		t.Errorf("expected in-progress, got %q", state.Status)
	}

	intentStage, ok := state.Stages["intent"]
	if !ok {
		t.Fatal("expected intent stage in parsed state")
	}
	if intentStage.Status != "completed" {
		t.Errorf("expected intent completed, got %q", intentStage.Status)
	}

	s1, ok := state.Steps["s1"]
	if !ok {
		t.Fatal("expected step s1 in parsed state")
	}
	if s1.Result != "success" {
		t.Errorf("expected s1 result success, got %q", s1.Result)
	}
}

func TestParser_NextPendingStage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stage-test.md")

	w, err := NewWriter(path, "sess3", "test")
	if err != nil {
		t.Fatal(err)
	}
	_ = w.WriteStageStart("intent")
	_ = w.WriteStageComplete("intent", "output", "hyperi-intent")
	_ = w.WriteStageStart("plan")

	state, err := ParsePlanDoc(path)
	if err != nil {
		t.Fatal(err)
	}

	next := state.NextPendingStage()
	if next != "plan" {
		t.Errorf("expected next pending stage to be 'plan', got %q", next)
	}
}

func TestParser_NextPendingStep(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "step-test.md")

	w, err := NewWriter(path, "sess4", "test")
	if err != nil {
		t.Fatal(err)
	}

	s1 := types.ActionStep{ID: "s1", Description: "a", Command: []string{"ls"}, OnFailure: "halt"}
	s2 := types.ActionStep{ID: "s2", Description: "b", Command: []string{"ls"}, OnFailure: "halt"}

	_ = w.WriteStepVerdict(s1, "approved", "ok")
	_ = w.WriteStepStart(s1)
	_ = w.WriteStepResult(s1, &types.ExecutionResult{StepID: "s1", Success: true})
	_ = w.WriteStepVerdict(s2, "approved", "ok")

	state, err := ParsePlanDoc(path)
	if err != nil {
		t.Fatal(err)
	}

	next := state.NextPendingStep([]string{"s1", "s2"})
	if next != "s2" {
		t.Errorf("expected s2 as next pending step, got %q", next)
	}
}

func TestParser_AllStepsComplete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "complete-test.md")

	w, err := NewWriter(path, "sess5", "test")
	if err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"s1", "s2"} {
		step := types.ActionStep{ID: id, Description: id, Command: []string{"ls"}, OnFailure: "halt"}
		_ = w.WriteStepVerdict(step, "approved", "ok")
		_ = w.WriteStepStart(step)
		_ = w.WriteStepResult(step, &types.ExecutionResult{StepID: id, Success: true})
	}

	state, err := ParsePlanDoc(path)
	if err != nil {
		t.Fatal(err)
	}

	if !state.AllStepsComplete([]string{"s1", "s2"}) {
		t.Error("expected all steps complete")
	}
	if state.AllStepsComplete([]string{"s1", "s2", "s3"}) {
		t.Error("expected not all complete when s3 is absent")
	}
}

func TestWriter_Finalize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "final-test.md")

	w, err := NewWriter(path, "sess6", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Finalize(StatusCompleted); err != nil {
		t.Fatal(err)
	}

	state, err := ParsePlanDoc(path)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusCompleted {
		t.Errorf("expected completed, got %q", state.Status)
	}
}
