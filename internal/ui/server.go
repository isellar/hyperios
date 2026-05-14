package ui

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/isellar/hyperios/internal/session"
)

// windowLister is an interface over the compositor window manager so tests can inject fakes.
type windowLister interface {
	ListWindows() ([]WindowInfo, error)
	FocusWindow(windowID string) error
}

// WindowInfo describes a window managed by the compositor.
type WindowInfo struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	App   string `json:"app"`
}

// PipelineRunner is the function the server calls when a user_input arrives
// over WebSocket. It mirrors shell.PipelineRunner.
type PipelineRunner func(intent, sessionID string) error

type Server struct {
	addr           string
	manager        *session.Manager
	current        *session.State
	mu             sync.RWMutex
	clients        map[*websocketConn]bool
	clientsMu      sync.RWMutex
	screenCapture  *ScreenCapture
	windowMgr      windowLister
	appController  *AppController
	previousWindow string
	pipeline       PipelineRunner // nil if not wired
}

type websocketConn struct {
	conn *websocket.Conn
	send chan []byte
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func NewServer(addr string, mgr *session.Manager) *Server {
	return &Server{
		addr:          addr,
		manager:       mgr,
		clients:       make(map[*websocketConn]bool),
		screenCapture: NewScreenCapture(),
		windowMgr:     NewWindowManager(),
		appController: NewAppController(),
	}
}

// SetPipeline attaches a pipeline runner so the web UI can actually execute intents.
// Must be called before Start().
func (s *Server) SetPipeline(runner PipelineRunner) {
	s.pipeline = runner
}

func (s *Server) SetCurrent(state *session.State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = state
	s.broadcastSessionUpdate(state)
}

func (s *Server) Start() error {
	distFS := http.FileServer(http.Dir("ui/frontend/dist"))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.ServeFile(w, r, "ui/frontend/dist/index.html")
			return
		}
		distFS.ServeHTTP(w, r)
	})

	http.HandleFunc("/api/state", s.handleState)
	http.HandleFunc("/api/sessions", s.handleSessions)
	http.HandleFunc("/api/windows", s.handleWindows)
	http.HandleFunc("/ws", s.handleWebSocket)

	go s.startScreenCaptureLoop()

	return http.ListenAndServe(s.addr, nil)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, "ui/frontend/dist/index.html")
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.current == nil {
		json.NewEncoder(w).Encode(map[string]string{"status": "no active session"})
		return
	}
	json.NewEncoder(w).Encode(s.current)
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	sessions, err := s.manager.List()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(sessions)
}

func (s *Server) handleWindows(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	windows, err := s.windowMgr.ListWindows()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(windows)
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := &websocketConn{
		conn: conn,
		send: make(chan []byte, 256),
	}

	s.clientsMu.Lock()
	s.clients[client] = true
	s.clientsMu.Unlock()

	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, client)
		s.clientsMu.Unlock()
		close(client.send)
	}()

	go func() {
		for msg := range client.send {
			conn.WriteMessage(websocket.TextMessage, msg)
		}
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		s.handleWebSocketMessage(msg)
	}
}

func (s *Server) handleWebSocketMessage(data []byte) {
	var msg map[string]interface{}
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}

	msgType, _ := msg["type"].(string)

	switch msgType {
	case "user_input":
		text, _ := msg["text"].(string)
		s.handleUserInput(text)

	case "get_windows":
		windows, _ := s.windowMgr.ListWindows()
		s.broadcast(map[string]interface{}{
			"type":    "window_list",
			"windows": windows,
		})

	case "select_window":
		windowID, _ := msg["window_id"].(string)
		s.selectWindow(windowID)

	case "stop_capture":
		s.screenCapture.Stop()

	case "click":
		x, _ := msg["x"].(float64)
		y, _ := msg["y"].(float64)
		s.appController.Click(int(x), int(y))

	case "type":
		text, _ := msg["text"].(string)
		s.appController.Type(text)

	case "switch_view":
		view, _ := msg["view"].(string)
		s.previousWindow = s.screenCapture.CurrentWindow()
		s.broadcast(map[string]interface{}{
			"type": "view_changed",
			"view": view,
		})

	case "switch_back":
		if s.previousWindow != "" {
			s.selectWindow(s.previousWindow)
		}

	case "show_window":
		searchTerm, _ := msg["query"].(string)
		s.showWindowByQuery(searchTerm)
	}
}

func (s *Server) handleUserInput(text string) {
	trimmed := strings.TrimSpace(text)
	lower := strings.ToLower(trimmed)

	// Built-in display commands handled without pipeline
	if strings.HasPrefix(lower, "show ") || strings.HasPrefix(lower, "show me ") {
		appName := strings.TrimPrefix(strings.TrimPrefix(lower, "show "), "show me ")
		s.handleShowCommand(appName)
		return
	}
	if strings.Contains(lower, "switch back") || strings.Contains(lower, "go back") {
		s.handleSwitchBack()
		return
	}

	// Route all other input to the agent pipeline
	if s.pipeline != nil && trimmed != "" {
		s.broadcast(map[string]interface{}{
			"type":    "session_update",
			"status":  "processing",
			"message": "Running: " + trimmed,
		})
		// Run in background so the WebSocket handler returns immediately
		go func() {
			if err := s.pipeline(trimmed, ""); err != nil {
				s.broadcast(map[string]interface{}{
					"type":    "notification",
					"title":   "Pipeline error",
					"message": err.Error(),
				})
			}
		}()
		return
	}

	// Pipeline not wired — echo back
	s.broadcast(map[string]interface{}{
		"type":    "session_update",
		"status":  "processing",
		"message": "Processing: " + trimmed,
	})
}

func (s *Server) handleShowCommand(appName string) {
	windows, err := s.windowMgr.ListWindows()
	if err != nil {
		s.broadcast(map[string]interface{}{
			"type":    "notification",
			"title":   "Error",
			"message": "Could not list windows",
		})
		return
	}

	for _, w := range windows {
		if strings.Contains(strings.ToLower(w.App), appName) ||
			strings.Contains(strings.ToLower(w.Title), appName) {
			s.previousWindow = s.screenCapture.CurrentWindow()
			s.screenCapture.SetWindow(w.ID)
			s.windowMgr.FocusWindow(w.ID)
			s.broadcast(map[string]interface{}{
				"type":    "notification",
				"title":   "View Changed",
				"message": "Now showing: " + w.App,
				"view":    "passthrough",
			})
			return
		}
	}

	s.broadcast(map[string]interface{}{
		"type":    "notification",
		"title":   "Not Found",
		"message": "Could not find: " + appName,
	})
}

func (s *Server) showWindowByQuery(query string) {
	s.handleShowCommand(strings.ToLower(strings.TrimSpace(query)))
}

func (s *Server) handleSwitchBack() {
	if s.previousWindow == "" {
		s.broadcast(map[string]interface{}{
			"type":    "notification",
			"title":   "No Previous View",
			"message": "No previous view to switch back to",
		})
		return
	}

	s.screenCapture.SetWindow(s.previousWindow)
	s.windowMgr.FocusWindow(s.previousWindow)
	s.broadcast(map[string]interface{}{
		"type":    "notification",
		"title":   "Switched Back",
		"message": "Returned to previous view",
		"view":    "passthrough",
	})
}

func (s *Server) selectWindow(windowID string) {
	s.previousWindow = s.screenCapture.CurrentWindow()
	s.screenCapture.SetWindow(windowID)
	s.windowMgr.FocusWindow(windowID)
}

func (s *Server) broadcast(msg map[string]interface{}) {
	data, _ := json.Marshal(msg)

	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()

	for client := range s.clients {
		select {
		case client.send <- data:
		default:
		}
	}
}

func (s *Server) broadcastSessionUpdate(state *session.State) {
	if state == nil {
		return
	}

	status := "started"
	if state.Plan != nil && len(state.Plan.Steps) > 0 {
		remaining := len(state.Plan.Steps) - len(state.Completed)
		if len(state.Completed) > 0 {
			if remaining == 0 {
				status = "completed"
			} else {
				status = "in_progress"
			}
		}
	}

	s.broadcast(map[string]interface{}{
		"type":    "session_update",
		"status":  status,
		"session": state,
	})
}

func (s *Server) startScreenCaptureLoop() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		if s.screenCapture.IsCapturing() {
			img, err := s.screenCapture.Capture()
			if err != nil {
				continue
			}
			s.broadcast(map[string]interface{}{
				"type":  "screenshot",
				"image": string(img),
			})
		}
	}
}

func (s *Server) SendNotification(title, message, view string) {
	s.broadcast(map[string]interface{}{
		"type":    "notification",
		"title":   title,
		"message": message,
		"view":    view,
	})
}
