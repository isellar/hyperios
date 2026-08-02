package apiserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/isellar/hyperios/internal/config"
	"github.com/isellar/hyperios/internal/goal_fulfillment"
	"github.com/isellar/hyperios/internal/io_toolbox"
	"github.com/isellar/hyperios/internal/llm"
	"github.com/isellar/hyperios/internal/memory"
	"github.com/isellar/hyperios/internal/processor"
	"github.com/isellar/hyperios/internal/types"
)

// stubCompleter satisfies llm.Completer with a fixed response, for tests that
// don't care about actual model behavior.
type stubCompleter struct {
	response string
}

func (s *stubCompleter) Complete(_ context.Context, _, _ string) (string, error) {
	return s.response, nil
}

func (s *stubCompleter) CompleteWithRetry(ctx context.Context, system, user string) (string, error) {
	return s.Complete(ctx, system, user)
}

var _ llm.Completer = (*stubCompleter)(nil)

// newTestServer builds a fully-wired Server backed by real modules (memory,
// io_toolbox) and a stub LLM, using temp-dir storage so tests are hermetic.
// The ResultStore is backed by a real temp-dir file (not in-memory-only) so
// tests exercise the same persistence path production wiring uses; use
// newTestServerAt if a test needs to reuse the same underlying directory
// across two Server instances (e.g. to simulate a restart).
func newTestServer(t *testing.T) (*Server, *Modules) {
	t.Helper()
	return newTestServerAt(t, t.TempDir())
}

// newTestServerAt is like newTestServer but takes an explicit directory,
// letting a test construct a second Server against the same on-disk state
// to simulate a process restart.
func newTestServerAt(t *testing.T, tmp string) (*Server, *Modules) {
	t.Helper()

	cfg := config.Defaults()
	cfg.MemoryStoragePath = filepath.Join(tmp, "memory")

	mem := memory.NewMemory(cfg)
	toolbox := io_toolbox.NewIOToolbox(cfg)
	completer := &stubCompleter{response: "ok"}

	proc := processor.NewProcessor(cfg, completer, nil)
	proc.SetToolbox(toolbox)
	proc.SetMemory(mem)

	gf, err := goal_fulfillment.New(completer, &memAdapter{mem}, proc, filepath.Join(tmp, "goals"))
	if err != nil {
		t.Fatalf("goal_fulfillment.New: %v", err)
	}
	proc.SetGoalFulfillment(gf)

	resultStore, err := processor.NewResultStore(filepath.Join(tmp, "goals", "agent_results.json"))
	if err != nil {
		t.Fatalf("processor.NewResultStore: %v", err)
	}

	mods := &Modules{
		GoalFulfillment: gf,
		Processor:       proc,
		Memory:          mem,
		IOToolbox:       toolbox,
		ResultStore:     resultStore,
	}
	return New(":0", mods), mods
}

// memAdapter bridges *memory.Memory to goal_fulfillment.MemoryProvider,
// mirroring cmd/hyperi/wiring.go's memoryAdapter (kept package-local here to
// avoid importing cmd/hyperi from a test).
type memAdapter struct {
	m *memory.Memory
}

func (a *memAdapter) GetContext(key string) (string, error) { return a.m.GetContext(key) }
func (a *memAdapter) StoreContext(key, value string) error  { return a.m.StoreContext(key, value) }

// ---------------------------------------------------------------------------
// storeOutcomeMemory tests
// ---------------------------------------------------------------------------

func TestStoreOutcomeMemory_Success(t *testing.T) {
	srv, mods := newTestServer(t)

	result := &processor.AgentResult{
		GoalID:  "g1",
		Success: true,
		Output:  "created three files successfully",
		Steps: []processor.AgentStep{
			{Tool: "shell", Input: "mkdir -p /tmp/foo", Output: "", IsError: false},
		},
	}

	srv.storeOutcomeMemory("g1", "create three files", result)

	entries, err := mods.Memory.SearchContext("create three files")
	if err != nil {
		t.Fatalf("SearchContext: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 matching memory entry, got %d", len(entries))
	}

	text, ok := entries[0].Value.(string)
	if !ok {
		t.Fatalf("expected string value, got %T", entries[0].Value)
	}
	if !strings.Contains(text, "succeeded") {
		t.Errorf("expected outcome summary to mention success, got:\n%s", text)
	}
	if !strings.Contains(text, "created three files successfully") {
		t.Errorf("expected outcome summary to include agent output, got:\n%s", text)
	}
	if !strings.Contains(text, "mkdir -p /tmp/foo") {
		t.Errorf("expected outcome summary to include tool step, got:\n%s", text)
	}

	foundSuccessTag := false
	for _, tag := range entries[0].Tags {
		if tag == "success" {
			foundSuccessTag = true
		}
	}
	if !foundSuccessTag {
		t.Errorf("expected 'success' tag on stored entry, got tags: %v", entries[0].Tags)
	}
}

func TestStoreOutcomeMemory_Failure(t *testing.T) {
	srv, mods := newTestServer(t)

	result := &processor.AgentResult{
		GoalID:  "g2",
		Success: false,
		Error:   "permission denied",
	}

	srv.storeOutcomeMemory("g2", "delete protected file", result)

	entries, err := mods.Memory.SearchContext("delete protected file")
	if err != nil {
		t.Fatalf("SearchContext: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 matching memory entry, got %d", len(entries))
	}

	text := entries[0].Value.(string)
	if !strings.Contains(text, "failed") {
		t.Errorf("expected outcome summary to mention failure, got:\n%s", text)
	}
	if !strings.Contains(text, "permission denied") {
		t.Errorf("expected outcome summary to include error, got:\n%s", text)
	}

	foundFailureTag := false
	for _, tag := range entries[0].Tags {
		if tag == "failure" {
			foundFailureTag = true
		}
	}
	if !foundFailureTag {
		t.Errorf("expected 'failure' tag, got tags: %v", entries[0].Tags)
	}
}

func TestStoreOutcomeMemory_NilMemory(t *testing.T) {
	srv := New(":0", &Modules{}) // no Memory wired
	result := &processor.AgentResult{GoalID: "g1", Success: true, Output: "done"}

	// Must not panic.
	srv.storeOutcomeMemory("g1", "some goal", result)
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate short string: got %q", got)
	}
	long := strings.Repeat("a", 20)
	got := truncate(long, 5)
	if got != "aaaaa..." {
		t.Errorf("truncate long string: got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Directive API handler tests
// ---------------------------------------------------------------------------

func TestHandleListDirectives_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/directives", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	directives, ok := resp["directives"]
	if !ok {
		t.Fatal("expected 'directives' key in response")
	}
	slice, ok := directives.([]any)
	if !ok || len(slice) != 0 {
		t.Errorf("expected empty directives list, got: %v", directives)
	}
}

func TestHandleAddDirective(t *testing.T) {
	srv, mods := newTestServer(t)

	body := bytes.NewBufferString(`{"description":"always verify available disk space before large writes","priority":5}`)
	req := httptest.NewRequest("POST", "/api/directives", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Directive should now be in memory.
	directives, err := mods.Memory.ListDirectives()
	if err != nil {
		t.Fatalf("ListDirectives: %v", err)
	}
	if len(directives) != 1 {
		t.Fatalf("expected 1 directive after POST, got %d", len(directives))
	}
	if directives[0].Priority != 5 {
		t.Errorf("expected priority 5, got %d", directives[0].Priority)
	}
}

func TestHandleAddDirective_EmptyDescription(t *testing.T) {
	srv, _ := newTestServer(t)

	body := bytes.NewBufferString(`{"description":""}`)
	req := httptest.NewRequest("POST", "/api/directives", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleRemoveDirective(t *testing.T) {
	srv, mods := newTestServer(t)

	_ = mods.Memory.AddDirective(types.Directive{ID: "d1", Description: "temp rule"})

	req := httptest.NewRequest("DELETE", "/api/directives/d1", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	directives, _ := mods.Memory.ListDirectives()
	if len(directives) != 0 {
		t.Errorf("expected 0 directives after DELETE, got %d", len(directives))
	}
}

func TestHandleRemoveDirective_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest("DELETE", "/api/directives/nonexistent-id", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Clarification / answer flow tests
// ---------------------------------------------------------------------------

// newClarifyingTestServer is like newTestServer but the stub LLM always
// responds as if clarification were needed, so handleCreateGoal exercises
// the "needs attention" path.
func newClarifyingTestServer(t *testing.T, question string) (*Server, *Modules) {
	t.Helper()

	tmp := t.TempDir()
	cfg := config.Defaults()
	cfg.MemoryStoragePath = filepath.Join(tmp, "memory")

	mem := memory.NewMemory(cfg)
	toolbox := io_toolbox.NewIOToolbox(cfg)
	clarifyResp := `{"intent":"x","context":"","goals":[],"clarification_needed":true,"clarification_question":"` + question + `"}`
	completer := &stubCompleter{response: clarifyResp}

	proc := processor.NewProcessor(cfg, completer, nil)
	proc.SetToolbox(toolbox)
	proc.SetMemory(mem)

	gf, err := goal_fulfillment.New(completer, &memAdapter{mem}, proc, filepath.Join(tmp, "goals"))
	if err != nil {
		t.Fatalf("goal_fulfillment.New: %v", err)
	}
	proc.SetGoalFulfillment(gf)

	mods := &Modules{GoalFulfillment: gf, Processor: proc, Memory: mem, IOToolbox: toolbox}
	return New(":0", mods), mods
}

func TestHandleCreateGoal_ClarificationNeeded(t *testing.T) {
	srv, _ := newClarifyingTestServer(t, "What should the notification say?")

	body := bytes.NewBufferString(`{"description":"notify me about something"}`)
	req := httptest.NewRequest("POST", "/api/goals", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Goal types.Goal `json:"goal"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Goal.ClarificationQuestion != "What should the notification say?" {
		t.Errorf("expected clarification question in response, got %q", resp.Goal.ClarificationQuestion)
	}
	if !resp.Goal.NeedsAttention {
		t.Error("expected NeedsAttention=true in response")
	}
	if resp.Goal.State != types.GoalStateRefining {
		t.Errorf("expected state %q, got %q", types.GoalStateRefining, resp.Goal.State)
	}
}

func TestHandleAnswerGoal(t *testing.T) {
	srv, mods := newClarifyingTestServer(t, "How should notifications appear?")

	body := bytes.NewBufferString(`{"description":"set up notifications"}`)
	req := httptest.NewRequest("POST", "/api/goals", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	var created struct {
		Goal types.Goal `json:"goal"`
	}
	json.NewDecoder(w.Body).Decode(&created)
	if created.Goal.ClarificationQuestion == "" {
		t.Fatal("expected clarification question on created goal")
	}

	// Now switch the underlying stub to succeed so answering can proceed
	// past refinement. handleAnswerGoal re-runs RefineGoal internally, so we
	// need a completer that returns a valid non-clarifying response for the
	// *next* call. Simplest: replace the goal's own state via AnswerGoal and
	// verify the question is cleared even if refinement fails again — the
	// important behavior under test is the fold-in-and-clear, not full
	// end-to-end execution (covered by goal_fulfillment's own tests).
	answerBody := bytes.NewBufferString(`{"answer":"Desktop notifications via notify-send"}`)
	answerReq := httptest.NewRequest("POST", "/api/goals/"+created.Goal.ID+"/answer", answerBody)
	answerReq.Header.Set("Content-Type", "application/json")
	answerW := httptest.NewRecorder()
	srv.mux.ServeHTTP(answerW, answerReq)

	if answerW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", answerW.Code, answerW.Body.String())
	}

	// Regardless of what the second refinement round decides, the goal
	// should reflect the answer having been recorded.
	stored, err := mods.GoalFulfillment.GetGoal(created.Goal.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if !strings.Contains(stored.Description, "Desktop notifications via notify-send") {
		t.Errorf("expected answer folded into description, got %q", stored.Description)
	}
}

func TestHandleAnswerGoal_EmptyAnswer(t *testing.T) {
	srv, _ := newClarifyingTestServer(t, "some question")

	body := bytes.NewBufferString(`{"description":"a goal"}`)
	req := httptest.NewRequest("POST", "/api/goals", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	var created struct {
		Goal types.Goal `json:"goal"`
	}
	json.NewDecoder(w.Body).Decode(&created)

	answerBody := bytes.NewBufferString(`{"answer":""}`)
	answerReq := httptest.NewRequest("POST", "/api/goals/"+created.Goal.ID+"/answer", answerBody)
	answerW := httptest.NewRecorder()
	srv.mux.ServeHTTP(answerW, answerReq)

	if answerW.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty answer, got %d", answerW.Code)
	}
}

func TestHandleAnswerGoal_NoQuestionPending(t *testing.T) {
	srv, mods := newTestServer(t)

	goal, err := mods.GoalFulfillment.SubmitGoal("a normal goal, no clarification needed")
	if err != nil {
		t.Fatalf("SubmitGoal: %v", err)
	}

	answerBody := bytes.NewBufferString(`{"answer":"an answer nobody asked for"}`)
	answerReq := httptest.NewRequest("POST", "/api/goals/"+goal.ID+"/answer", answerBody)
	answerW := httptest.NewRecorder()
	srv.mux.ServeHTTP(answerW, answerReq)

	if answerW.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", answerW.Code, answerW.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Delete goal tests
// ---------------------------------------------------------------------------

func TestHandleDeleteGoal(t *testing.T) {
	srv, mods := newTestServer(t)

	goal, err := mods.GoalFulfillment.SubmitGoal("a goal to delete")
	if err != nil {
		t.Fatalf("SubmitGoal: %v", err)
	}

	req := httptest.NewRequest("DELETE", "/api/goals/"+goal.ID, nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if _, err := mods.GoalFulfillment.GetGoal(goal.ID); err == nil {
		t.Error("expected goal to be gone after delete")
	}
}

func TestHandleDeleteGoal_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest("DELETE", "/api/goals/nonexistent-id", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Result persistence tests — the regression coverage for "blocked goal
// failure reasons disappeared after a server restart".
// ---------------------------------------------------------------------------

// TestRecordResult_PersistsAndIsRetrievable verifies that recordResult (the
// callback wired into Processor.RunLoop) writes through to the ResultStore,
// and that GET /api/goals/{id}/result can retrieve it afterward.
func TestRecordResult_PersistsAndIsRetrievable(t *testing.T) {
	srv, _ := newTestServer(t)

	result := &processor.AgentResult{
		GoalID:  "g-blocked",
		Success: false,
		Error:   "steam: command not found",
	}
	srv.recordResult(result)

	req := httptest.NewRequest("GET", "/api/goals/g-blocked/result", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Result processor.AgentResult `json:"result"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Result.Error != "steam: command not found" {
		t.Errorf("expected error %q, got %q", "steam: command not found", resp.Result.Error)
	}
}

// TestRecordResult_SurvivesServerRestart is the end-to-end regression test:
// a result recorded by one Server instance must still be retrievable by a
// brand new Server instance pointed at the same data directory, simulating
// a process restart. Before ResultStore existed, results only lived in an
// in-memory map and this would fail.
func TestRecordResult_SurvivesServerRestart(t *testing.T) {
	tmp := t.TempDir()

	srv1, _ := newTestServerAt(t, tmp)
	srv1.recordResult(&processor.AgentResult{
		GoalID:  "g-blocked",
		Success: false,
		Error:   "permission denied writing /etc/steam.conf",
	})

	// Simulate a restart: a fresh Server (and fresh ResultStore) against the
	// same directory, not reusing srv1 in any way.
	srv2, _ := newTestServerAt(t, tmp)

	req := httptest.NewRequest("GET", "/api/goals/g-blocked/result", nil)
	w := httptest.NewRecorder()
	srv2.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected result to survive restart, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Result processor.AgentResult `json:"result"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Result.Error != "permission denied writing /etc/steam.conf" {
		t.Errorf("expected error to survive restart, got %q", resp.Result.Error)
	}
}

// TestHandleDeleteGoal_RemovesPersistedResult verifies that deleting a goal
// also removes its persisted result, so results don't accumulate forever
// for goals the user has explicitly dismissed.
func TestHandleDeleteGoal_RemovesPersistedResult(t *testing.T) {
	srv, mods := newTestServer(t)

	goal, err := mods.GoalFulfillment.SubmitGoal("a goal that will fail")
	if err != nil {
		t.Fatalf("SubmitGoal: %v", err)
	}
	srv.recordResult(&processor.AgentResult{GoalID: goal.ID, Success: false, Error: "boom"})

	if _, ok := srv.getResult(goal.ID); !ok {
		t.Fatal("expected result to be present before delete")
	}

	req := httptest.NewRequest("DELETE", "/api/goals/"+goal.ID, nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if _, ok := srv.getResult(goal.ID); ok {
		t.Error("expected result to be gone after goal deletion")
	}
}

func TestHandleListDirectives_WithDirective(t *testing.T) {
	srv, mods := newTestServer(t)

	_ = mods.Memory.AddDirective(types.Directive{ID: "d1", Description: "check disk before write", Priority: 3})

	req := httptest.NewRequest("GET", "/api/directives", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Directives []types.Directive `json:"directives"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Directives) != 1 {
		t.Fatalf("expected 1 directive, got %d", len(resp.Directives))
	}
	if resp.Directives[0].ID != "d1" {
		t.Errorf("expected ID 'd1', got %q", resp.Directives[0].ID)
	}
}
