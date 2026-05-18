package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/isellar/hyperios/internal/types"
)

func TestNewWriter_CreatesFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")

	w, err := NewWriter(path, "session-abc", "install nginx")
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	_ = w // Writer is append-only, no close needed

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "# Task: install nginx") {
		t.Error("missing task heading")
	}
	if !strings.Contains(content, "Session: session-abc") {
		t.Error("missing session ID")
	}
	if !strings.Contains(content, "Status: in-progress") {
		t.Error("missing in-progress status")
	}
	if !strings.Contains(content, "Attempt: 1") {
		t.Error("missing attempt 1")
	}
	if !strings.Contains(content, "Created: ") {
		t.Error("missing Created timestamp")
	}
	if !strings.Contains(content, "Updated: ") {
		t.Error("missing Updated timestamp")
	}
}

func TestWriteStageStart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")

	w, err := NewWriter(path, "session-1", "test intent")
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	if err := w.WriteStageStart("intent"); err != nil {
		t.Fatalf("WriteStageStart failed: %v", err)
	}
	_ = w

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "## Intent") {
		t.Error("missing ## Intent heading")
	}
	if !strings.Contains(content, "```hyperi-meta") {
		t.Error("missing hyperi-meta fence")
	}
	if !strings.Contains(content, "stage: intent") {
		t.Error("missing stage: intent")
	}
	if !strings.Contains(content, "status: in-progress") {
		t.Error("missing status: in-progress")
	}
}

func TestWriteStageComplete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")

	w, err := NewWriter(path, "session-1", "test intent")
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	if err := w.WriteStageComplete("intent", "some output", "hyperi-intent"); err != nil {
		t.Fatalf("WriteStageComplete failed: %v", err)
	}
	_ = w

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "stage: intent") {
		t.Error("missing stage: intent")
	}
	if !strings.Contains(content, "status: completed") {
		t.Error("missing status: completed")
	}
	if !strings.Contains(content, "```hyperi-intent") {
		t.Error("missing hyperi-intent fence")
	}
	if !strings.Contains(content, "some output") {
		t.Error("missing output content")
	}
}

func TestWriteStageFailed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")

	w, err := NewWriter(path, "session-1", "test intent")
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	if err := w.WriteStageFailed("intent", &stepError{msg: "something went wrong"}); err != nil {
		t.Fatalf("WriteStageFailed failed: %v", err)
	}
	_ = w

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "status: failed") {
		t.Error("missing status: failed")
	}
	if !strings.Contains(content, "error: something went wrong") {
		t.Error("missing error message")
	}
}

type stepError struct{ msg string }

func (e *stepError) Error() string { return e.msg }

func TestWriteStepResult_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")

	w, err := NewWriter(path, "session-1", "test intent")
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	step := types.ActionStep{
		ID:          "s1",
		Description: "install nginx",
		OnFailure:   "skip",
	}
	result := &types.ExecutionResult{
		StepID:   "s1",
		Success:  true,
		Output:   "nginx installed",
		Duration: 1500,
	}

	if err := w.WriteStepResult(step, result); err != nil {
		t.Fatalf("WriteStepResult failed: %v", err)
	}
	_ = w

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "step_id: s1") {
		t.Error("missing step_id: s1")
	}
	if !strings.Contains(content, "result: success") {
		t.Error("missing result: success")
	}
	if !strings.Contains(content, "exit_code: 0") {
		t.Error("missing exit_code: 0")
	}
	if !strings.Contains(content, "duration_ms: 1500") {
		t.Error("missing duration_ms")
	}
	if !strings.Contains(content, "on_failure: skip") {
		t.Error("missing on_failure: skip")
	}
	if !strings.Contains(content, "```output") {
		t.Error("missing output fence")
	}
	if !strings.Contains(content, "nginx installed") {
		t.Error("missing output content")
	}
}

func TestWriteStepResult_Failure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")

	w, err := NewWriter(path, "session-1", "test intent")
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	step := types.ActionStep{
		ID:          "s2",
		Description: "start service",
		OnFailure:   "halt",
	}
	result := &types.ExecutionResult{
		StepID:   "s2",
		Success:  false,
		Error:    "service failed to start",
		Duration: 500,
	}

	if err := w.WriteStepResult(step, result); err != nil {
		t.Fatalf("WriteStepResult failed: %v", err)
	}
	_ = w

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "result: failure") {
		t.Error("missing result: failure")
	}
	if !strings.Contains(content, "exit_code: 1") {
		t.Error("missing exit_code: 1")
	}
	if !strings.Contains(content, "```output") {
		t.Error("missing output fence")
	}
	if !strings.Contains(content, "service failed to start") {
		t.Error("missing error output")
	}
}

func TestWriteStepResult_Skipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")

	w, err := NewWriter(path, "session-1", "test intent")
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	step := types.ActionStep{
		ID:          "s3",
		Description: "check chrome",
		OnFailure:   "skip",
	}

	if err := w.WriteStepSkipped(step, "google-chrome not found"); err != nil {
		t.Fatalf("WriteStepSkipped failed: %v", err)
	}
	_ = w

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "status: skipped") {
		t.Error("missing status: skipped")
	}
	if !strings.Contains(content, "result: skipped") {
		t.Error("missing result: skipped")
	}
	if !strings.Contains(content, "reason: google-chrome not found") {
		t.Error("missing reason")
	}
}

func TestWriteReplanHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")

	w, err := NewWriter(path, "session-1", "test intent")
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	if err := w.WriteReplanHeader(2, "s1", 2, true); err != nil {
		t.Fatalf("WriteReplanHeader failed: %v", err)
	}
	_ = w

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "## Re-plan 2") {
		t.Error("missing ## Re-plan 2 heading")
	}
	if !strings.Contains(content, "Triggered by: Step s1 failure") {
		t.Error("missing trigger info")
	}
	if !strings.Contains(content, "Attempt: 2 of 3") {
		t.Error("missing attempt info")
	}
	if !strings.Contains(content, "User confirmation: required") {
		t.Error("missing confirmation requirement")
	}
}

func TestOpenWriter_PreservesExistingContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")

	w1, err := NewWriter(path, "session-1", "original intent")
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}
	if err := w1.WriteStageStart("intent"); err != nil {
		t.Fatalf("WriteStageStart failed: %v", err)
	}
	_ = w1

	w2, err := OpenWriter(path, "session-1", "original intent", 2)
	if err != nil {
		t.Fatalf("OpenWriter failed: %v", err)
	}
	if err := w2.WriteReplanHeader(2, "s1", 2, false); err != nil {
		t.Fatalf("WriteReplanHeader failed: %v", err)
	}
	_ = w2

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "# Task: original intent") {
		t.Error("original task heading missing")
	}
	if !strings.Contains(content, "## Intent") {
		t.Error("original stage heading missing")
	}
	if !strings.Contains(content, "## Re-plan 2") {
		t.Error("replan section missing")
	}
}

func TestExitCode_Helper(t *testing.T) {
	success := &types.ExecutionResult{Success: true}
	if exitCode(success) != 0 {
		t.Errorf("exitCode(success) = %d, want 0", exitCode(success))
	}

	failure := &types.ExecutionResult{Success: false}
	if exitCode(failure) != 1 {
		t.Errorf("exitCode(failure) = %d, want 1", exitCode(failure))
	}
}

func TestWriter_RoundTripWithParser(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")

	w, err := NewWriter(path, "session-rt", "round trip test")
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	if err := w.WriteStageStart("intent"); err != nil {
		t.Fatalf("WriteStageStart failed: %v", err)
	}

	step := types.ActionStep{
		ID:          "s1",
		Description: "test step",
		OnFailure:   "skip",
	}
	result := &types.ExecutionResult{
		StepID:   "s1",
		Success:  true,
		Duration: 100,
	}
	if err := w.WriteStepResult(step, result); err != nil {
		t.Fatalf("WriteStepResult failed: %v", err)
	}

	if err := w.WriteStageComplete("intent", "intent output", "hyperi-intent"); err != nil {
		t.Fatalf("WriteStageComplete failed: %v", err)
	}
	_ = w

	state, err := ParsePlanDoc(path)
	if err != nil {
		t.Fatalf("ParsePlanDoc failed: %v", err)
	}

	if state.SessionID != "session-rt" {
		t.Errorf("SessionID = %q, want %q", state.SessionID, "session-rt")
	}

	stage, ok := state.Stages["intent"]
	if !ok {
		t.Fatal("intent stage not found in parsed state")
	}
	if stage.Status != "completed" {
		t.Errorf("intent stage status = %q, want %q", stage.Status, "completed")
	}

	stepState, ok := state.Steps["s1"]
	if !ok {
		t.Fatal("s1 step not found in parsed state")
	}
	if stepState.Status != "completed" {
		t.Errorf("s1 step status = %q, want %q", stepState.Status, "completed")
	}
}