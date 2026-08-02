// Package apiserver exposes the HyperiOS agent over a small HTTP API so that
// any UI (web, CLI, mobile, whatever) can drive it without depending on a
// particular frontend.
//
// Design: fire-and-forget by default. POST /api/goals submits a goal and
// immediately queues it for background execution; the caller does not wait
// for the agent to finish. Poll GET /api/goals/{id} (and .../result) to
// observe progress and outcome. Pass "draft": true to create a goal without
// queuing it — a later POST /api/goals/{id}/run queues it explicitly.
package apiserver

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/isellar/hyperios/internal/config"
	"github.com/isellar/hyperios/internal/goal_fulfillment"
	"github.com/isellar/hyperios/internal/io_toolbox"
	"github.com/isellar/hyperios/internal/memory"
	"github.com/isellar/hyperios/internal/module"
	"github.com/isellar/hyperios/internal/processor"
	"github.com/isellar/hyperios/internal/self_improvement"
	"github.com/isellar/hyperios/internal/types"
	"github.com/isellar/hyperios/ui/frontend"
)

// Modules is the narrow set of dependencies the API server needs. It mirrors
// (but does not import) cmd/hyperi's Modules struct so this package stays
// free of any cmd/-level import.
type Modules struct {
	GoalFulfillment *goal_fulfillment.GoalFulfillment
	Processor       *processor.Processor
	Memory          *memory.Memory
	SelfImprovement *self_improvement.SelfImprovement
	IOToolbox       *io_toolbox.IOToolbox
	// Config is the runtime configuration, used to surface autonomy level
	// and configured LLM provider/model in the status endpoint. May be nil
	// in tests that don't care about that surface.
	Config *config.Config
	// ResultStore persists goal outcomes (AgentResult) to disk, keyed by
	// goal ID, so a goal's result — most importantly a blocked goal's
	// failure reason — survives a process restart. If nil, the server
	// falls back to a fresh in-memory-only store (results lost on
	// restart), which is fine for tests but not for a real deployment.
	ResultStore *processor.ResultStore
}

// modules returns the named module.Module instances for generic health/report
// iteration, in a stable order.
func (m *Modules) namedModules() []module.Module {
	return []module.Module{m.GoalFulfillment, m.Processor, m.Memory, m.SelfImprovement, m.IOToolbox}
}

// Server is the HTTP API server. Construct with New, then call Start.
type Server struct {
	addr    string
	mods    *Modules
	mux     *http.ServeMux
	results *processor.ResultStore
}

// New constructs a Server bound to addr (e.g. ":8080") using the given
// wired Modules. If mods.ResultStore is nil, an in-memory-only ResultStore
// is created so the server still functions (e.g. in tests) — results just
// won't survive a restart in that case.
func New(addr string, mods *Modules) *Server {
	results := mods.ResultStore
	if results == nil {
		results, _ = processor.NewResultStore("") // empty path => in-memory only, never errors
	}

	s := &Server{
		addr:    addr,
		mods:    mods,
		mux:     http.NewServeMux(),
		results: results,
	}
	s.routes()
	return s
}

// Start runs the background goal-processing loop and then blocks serving
// HTTP until ctx is cancelled or the server errors.
func (s *Server) Start(ctx context.Context) error {
	go s.mods.Processor.RunLoop(ctx, 500*time.Millisecond, s.recordResult)

	srv := &http.Server{
		Addr:    s.addr,
		Handler: s.mux,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("apiserver: listening on %s", s.addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

// recordResult is called by Processor.RunLoop after each goal run. It
// persists the result (to disk, via ResultStore) for later retrieval via the
// API, writes an outcome summary into Memory so future goals can recall it,
// feeds the outcome to SelfImprovement, and periodically triggers an
// analysis cycle.
func (s *Server) recordResult(result *processor.AgentResult) {
	if result == nil || result.GoalID == "" {
		return
	}
	if err := s.results.Save(result); err != nil {
		log.Printf("apiserver: persist result for goal %s: %v", result.GoalID, err)
	}

	description := ""
	if goal, err := s.mods.GoalFulfillment.GetGoal(result.GoalID); err == nil {
		description = goal.Description
	}

	s.storeOutcomeMemory(result.GoalID, description, result)

	if s.mods.SelfImprovement == nil {
		return
	}

	s.mods.SelfImprovement.RecordResult(self_improvement.GoalResult{
		GoalID:      result.GoalID,
		Description: description,
		Success:     result.Success,
		Output:      result.Output,
		ErrorMsg:    result.Error,
	})

	// Analyze immediately after every goal. The whole point of self-improvement
	// is to not hit the same problem twice — batching defeats that. The LLM
	// call is async so it never blocks goal execution.
	go func() {
		if err := s.mods.SelfImprovement.Analyze(); err != nil {
			log.Printf("apiserver: self-improvement analyze: %v", err)
		}
	}()
}

// storeOutcomeMemory persists a short, searchable summary of what happened
// when the agent worked this goal — what was asked, what tools it used, and
// whether it succeeded. This is the "write" half of the memory loop: without
// it, GoalFulfillment.Refiner's GetContext/SearchContext calls (the "read"
// half, wired since the initial goal_fulfillment implementation) have
// nothing meaningful to find, so future similar goals never benefit from
// past experience. Entries are tagged "goal_outcome" (+ "success"/"failure")
// and stored under a key derived from the goal ID, so they show up in
// ordinary substring search (Memory.SearchContext) whenever a future goal's
// description shares words with this one.
func (s *Server) storeOutcomeMemory(goalID, description string, result *processor.AgentResult) {
	if s.mods.Memory == nil {
		return
	}

	outcome := "succeeded"
	tag := "success"
	if !result.Success {
		outcome = "failed"
		tag = "failure"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Goal: %s\n", description)
	fmt.Fprintf(&sb, "Outcome: %s\n", outcome)
	if result.Output != "" {
		fmt.Fprintf(&sb, "Summary: %s\n", result.Output)
	}
	if result.Error != "" {
		fmt.Fprintf(&sb, "Error: %s\n", result.Error)
	}
	if len(result.Steps) > 0 {
		sb.WriteString("Steps taken:\n")
		for _, step := range result.Steps {
			status := "ok"
			if step.IsError {
				status = "error"
			}
			fmt.Fprintf(&sb, "  - [%s] %s: %s\n", step.Tool, status, truncate(step.Input, 200))
		}
	}

	key := fmt.Sprintf("goal_outcome:%s", goalID)
	if err := s.mods.Memory.StoreContextTagged(key, sb.String(), []string{"goal_outcome", tag}); err != nil {
		log.Printf("apiserver: store outcome memory for goal %s: %v", goalID, err)
	}
}

// truncate shortens s to at most n runes, appending "..." if it was cut.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

func (s *Server) getResult(goalID string) (*processor.AgentResult, bool) {
	return s.results.Get(goalID)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/reports", s.handleReports)
	s.mux.HandleFunc("GET /api/tools", s.handleListTools)
	// Aggregated at-a-glance status for the web UI status bar: goal counts
	// by state, directive count, queued goals, and the configured/active
	// LLM backend. Distinct from /api/health (per-module ok/degraded) and
	// /api/reports (per-module raw metrics) — this endpoint exists purely
	// to save the UI from stitching those two together itself.
	s.mux.HandleFunc("GET /api/status", s.handleStatus)

	s.mux.HandleFunc("POST /api/goals", s.handleCreateGoal)
	s.mux.HandleFunc("GET /api/goals", s.handleListGoals)
	s.mux.HandleFunc("GET /api/goals/{id}", s.handleGetGoal)
	s.mux.HandleFunc("POST /api/goals/{id}/run", s.handleRunGoal)
	s.mux.HandleFunc("GET /api/goals/{id}/result", s.handleGetResult)
	// Answer a pending clarification question (goal.needs_attention == true).
	// Folds the answer into the goal's context, re-refines, and queues it —
	// same as a fresh submission, just with the extra information attached.
	s.mux.HandleFunc("POST /api/goals/{id}/answer", s.handleAnswerGoal)
	// Dismiss/remove a goal entirely (e.g. a stale or orphaned one with no
	// path forward). Distinct from cancellation-via-state since there is no
	// "cancelled" transition wired from arbitrary states yet — this is a
	// hard delete from tracking.
	s.mux.HandleFunc("DELETE /api/goals/{id}", s.handleDeleteGoal)

	// Directives: standing behavioral rules the agent applies to every goal.
	// Learned directives are written by SelfImprovement.Analyze; user-authored
	// directives can be added here directly. GET lists them; POST adds one;
	// DELETE /api/directives/{id} removes one.
	s.mux.HandleFunc("GET /api/directives", s.handleListDirectives)
	s.mux.HandleFunc("POST /api/directives", s.handleAddDirective)
	s.mux.HandleFunc("DELETE /api/directives/{id}", s.handleRemoveDirective)

	// Minimal web UI (see ui/frontend): served at "/" and does not shadow
	// any /api/* route above since Go's ServeMux prefers the more specific
	// pattern.
	s.mux.Handle("/", http.FileServer(http.FS(frontend.FS())))
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	out := make(map[string]module.ModuleHealth)
	for _, m := range s.mods.namedModules() {
		out[m.Name()] = m.Health()
	}
	writeJSON(w, http.StatusOK, map[string]any{"modules": out})
}

func (s *Server) handleReports(w http.ResponseWriter, r *http.Request) {
	window := 24 * time.Hour
	out := make(map[string]module.ModuleReport)
	for _, m := range s.mods.namedModules() {
		report, err := m.Report(r.Context(), window)
		if err != nil {
			out[m.Name()] = module.ModuleReport{ModuleName: m.Name(), Issues: []string{err.Error()}}
			continue
		}
		out[m.Name()] = report
	}
	writeJSON(w, http.StatusOK, map[string]any{"modules": out})
}

func (s *Server) handleListTools(w http.ResponseWriter, r *http.Request) {
	if s.mods.IOToolbox == nil {
		writeJSON(w, http.StatusOK, map[string]any{"tools": []string{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": s.mods.IOToolbox.ListTools()})
}

// statusResponse is the payload for GET /api/status — everything the web UI
// status bar needs in one round trip.
type statusResponse struct {
	Goals         goalCounts                 `json:"goals"`
	QueuedGoals   int                        `json:"queued_goals"`
	Directives    int                        `json:"directives"`
	AutonomyLevel int                        `json:"autonomy_level"`
	AutonomyText  string                     `json:"autonomy_text"`
	Model         *processor.ActiveModelInfo `json:"model,omitempty"`
	SelfModifyOn  bool                       `json:"self_modify_enabled"`
}

type goalCounts struct {
	Total     int `json:"total"`
	Active    int `json:"active"`
	Blocked   int `json:"blocked"`
	Refining  int `json:"refining"`
	Done      int `json:"done"`
	Cancelled int `json:"cancelled"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	resp := statusResponse{}

	if all, err := s.mods.GoalFulfillment.ListGoals(""); err == nil {
		resp.Goals.Total = len(all)
		for _, g := range all {
			switch g.State {
			case types.GoalStateActive:
				resp.Goals.Active++
			case types.GoalStateBlocked:
				resp.Goals.Blocked++
			case types.GoalStateRefining:
				resp.Goals.Refining++
			case types.GoalStateDone:
				resp.Goals.Done++
			case types.GoalStateCancelled:
				resp.Goals.Cancelled++
			}
		}
	}

	if s.mods.Processor != nil {
		resp.QueuedGoals = s.mods.Processor.QueuedGoals()
		model := s.mods.Processor.ModelInfo()
		resp.Model = &model
	}

	if s.mods.Memory != nil {
		if directives, err := s.mods.Memory.ListDirectives(); err == nil {
			resp.Directives = len(directives)
		}
	}

	if s.mods.Config != nil {
		resp.AutonomyLevel = s.mods.Config.AutonomyLevel
		resp.AutonomyText = config.AutonomyLevelText(s.mods.Config.AutonomyLevel)
		resp.SelfModifyOn = s.mods.Config.SelfModifyEnabled
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": resp})
}

type createGoalRequest struct {
	Description string `json:"description"`
	// Draft, if true, creates the goal without queuing it for execution.
	// Defaults to false (fire-and-forget: queued immediately).
	Draft bool `json:"draft"`
}

func (s *Server) handleCreateGoal(w http.ResponseWriter, r *http.Request) {
	var req createGoalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("decode request: %w", err))
		return
	}
	if req.Description == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("description must not be empty"))
		return
	}

	goal, err := s.mods.GoalFulfillment.SubmitGoal(req.Description)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Refine before queuing: this pulls in past memory context (prior
	// outcomes for similar goals, learned directives) so the agent starts
	// with the full picture rather than a blank slate. If the refiner needs
	// clarification, the returned goal carries the question
	// (ClarificationQuestion/NeedsAttention) and is persisted but NOT
	// queued — the caller answers via POST /api/goals/{id}/answer, which
	// re-refines and queues it. Any other refine error is non-fatal: we
	// proceed with the unrefined goal rather than blocking execution.
	if !req.Draft {
		refined, refineErr := s.mods.GoalFulfillment.RefineGoal(r.Context(), goal)
		var clarErr *goal_fulfillment.ClarificationNeededError
		switch {
		case refineErr == nil:
			goal = refined
		case errors.As(refineErr, &clarErr):
			// Needs the user's input before it can run at all — return as
			// refining/needs-attention without queuing.
			writeJSON(w, http.StatusCreated, map[string]any{"goal": refined})
			return
		default:
			log.Printf("apiserver: refine goal %s: %v (proceeding with unrefined goal)", goal.ID, refineErr)
		}

		if err := s.mods.Processor.QueueGoal(goal); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("queue goal: %w", err))
			return
		}
		if updated, err := s.mods.GoalFulfillment.GetGoal(goal.ID); err == nil {
			goal = updated
		}
	}

	writeJSON(w, http.StatusCreated, map[string]any{"goal": goal})
}

func (s *Server) handleListGoals(w http.ResponseWriter, r *http.Request) {
	state := types.GoalState(r.URL.Query().Get("state"))
	goals, err := s.mods.GoalFulfillment.ListGoals(state)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"goals": goals})
}

func (s *Server) handleGetGoal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	goal, err := s.mods.GoalFulfillment.GetGoal(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	resp := map[string]any{"goal": goal}
	if result, ok := s.getResult(id); ok {
		resp["result"] = result
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleRunGoal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	goal, err := s.mods.GoalFulfillment.GetGoal(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if err := s.mods.Processor.QueueGoal(goal); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("queue goal: %w", err))
		return
	}
	if updated, err := s.mods.GoalFulfillment.GetGoal(id); err == nil {
		goal = updated
	}
	writeJSON(w, http.StatusOK, map[string]any{"goal": goal})
}

func (s *Server) handleGetResult(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	result, ok := s.getResult(id)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("no result yet for goal %q", id))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": result})
}

func (s *Server) handleDeleteGoal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.mods.GoalFulfillment.DeleteGoal(id); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if err := s.results.Delete(id); err != nil {
		log.Printf("apiserver: delete persisted result for goal %s: %v", id, err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

type answerGoalRequest struct {
	Answer string `json:"answer"`
}

// handleAnswerGoal responds to a goal's pending clarification question. It
// folds the answer into the goal (via GoalFulfillment.AnswerGoal), then
// re-runs refinement — same as a fresh submission — and queues it if
// refinement succeeds. If the refiner needs *another* clarification, the
// goal is returned again with a new question rather than queued, so this
// can be called repeatedly for a back-and-forth.
func (s *Server) handleAnswerGoal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req answerGoalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("decode request: %w", err))
		return
	}
	if req.Answer == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("answer must not be empty"))
		return
	}

	goal, err := s.mods.GoalFulfillment.AnswerGoal(id, req.Answer)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	refined, refineErr := s.mods.GoalFulfillment.RefineGoal(r.Context(), goal)
	var clarErr *goal_fulfillment.ClarificationNeededError
	switch {
	case refineErr == nil:
		goal = refined
	case errors.As(refineErr, &clarErr):
		writeJSON(w, http.StatusOK, map[string]any{"goal": refined})
		return
	default:
		log.Printf("apiserver: re-refine goal %s after answer: %v (proceeding with unrefined goal)", id, refineErr)
	}

	if err := s.mods.Processor.QueueGoal(goal); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("queue goal: %w", err))
		return
	}
	if updated, err := s.mods.GoalFulfillment.GetGoal(goal.ID); err == nil {
		goal = updated
	}
	writeJSON(w, http.StatusOK, map[string]any{"goal": goal})
}

// ── Directive handlers ────────────────────────────────────────────────────────

func (s *Server) handleListDirectives(w http.ResponseWriter, r *http.Request) {
	if s.mods.Memory == nil {
		writeJSON(w, http.StatusOK, map[string]any{"directives": []any{}})
		return
	}
	directives, err := s.mods.Memory.ListDirectives()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"directives": directives})
}

type addDirectiveRequest struct {
	Description string `json:"description"`
	Priority    int    `json:"priority"`
	Immutable   bool   `json:"immutable"`
}

func (s *Server) handleAddDirective(w http.ResponseWriter, r *http.Request) {
	if s.mods.Memory == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("memory module not available"))
		return
	}
	var req addDirectiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("decode request: %w", err))
		return
	}
	if req.Description == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("description must not be empty"))
		return
	}

	// Derive a stable ID from the description (same approach as
	// SelfImprovement uses for learned directives, so user-authored and
	// learned directives with identical text naturally de-duplicate).
	sum := sha256.Sum256([]byte(req.Description))
	d := types.Directive{
		ID:          fmt.Sprintf("user-%x", sum[:6]), // 12 hex chars
		Description: req.Description,
		Priority:    req.Priority,
		Immutable:   req.Immutable,
	}
	if err := s.mods.Memory.AddDirective(d); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"directive": d})
}

func (s *Server) handleRemoveDirective(w http.ResponseWriter, r *http.Request) {
	if s.mods.Memory == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("memory module not available"))
		return
	}
	id := r.PathValue("id")
	if err := s.mods.Memory.RemoveDirective(id); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": id})
}

// ── JSON helpers ──────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
