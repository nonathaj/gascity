package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/sessionlog"
)

// outputTurn is a single conversation turn in the unified output response.
type outputTurn struct {
	Role      string `json:"role"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp,omitempty"`
}

// agentOutputResponse is the response for GET /v0/agent/{name}/output.
type agentOutputResponse struct {
	Agent      string                     `json:"agent"`
	Format     string                     `json:"format"` // "conversation" or "text"
	Turns      []outputTurn               `json:"turns"`
	Pagination *sessionlog.PaginationInfo `json:"pagination,omitempty"`
}

// handleAgentOutput returns unified conversation output for an agent.
// Tries structured session logs first, falls back to Peek().
//
// Query params:
//   - tail: number of compaction segments to return (default 1, 0 = all)
//   - before: message UUID cursor for loading older messages
func (s *Server) handleAgentOutput(w http.ResponseWriter, r *http.Request, name string) {
	cfg := s.state.Config()
	agentCfg, ok := findAgent(cfg, name)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "agent "+name+" not found")
		return
	}

	// Try structured session log first.
	if resp, ok := s.trySessionLogOutput(r, name, agentCfg); ok {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Fall back to Peek() — raw terminal text.
	s.peekFallbackOutput(w, name, cfg)
}

// trySessionLogOutput attempts to read structured conversation data from
// a Claude JSONL session file. Returns false if no session file is found.
func (s *Server) trySessionLogOutput(r *http.Request, name string, agentCfg config.Agent) (*agentOutputResponse, bool) {
	workDir := s.resolveAgentWorkDir(agentCfg)
	if workDir == "" {
		return nil, false
	}

	searchPaths := s.sessionLogSearchPaths
	if searchPaths == nil {
		searchPaths = sessionlog.DefaultSearchPaths()
	}
	path := sessionlog.FindSessionFile(searchPaths, workDir)
	if path == "" {
		return nil, false
	}

	tail := 1
	if v := r.URL.Query().Get("tail"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			tail = n
		}
	}
	before := r.URL.Query().Get("before")

	var sess *sessionlog.Session
	var err error
	if before != "" {
		sess, err = sessionlog.ReadFileOlder(path, tail, before)
	} else {
		sess, err = sessionlog.ReadFile(path, tail)
	}
	if err != nil {
		return nil, false
	}

	turns := make([]outputTurn, 0, len(sess.Messages))
	for _, e := range sess.Messages {
		turn := entryToTurn(e)
		if turn.Text == "" {
			continue
		}
		turns = append(turns, turn)
	}

	return &agentOutputResponse{
		Agent:      name,
		Format:     "conversation",
		Turns:      turns,
		Pagination: sess.Pagination,
	}, true
}

// peekFallbackOutput returns raw terminal text wrapped as a single turn.
func (s *Server) peekFallbackOutput(w http.ResponseWriter, name string, cfg *config.City) {
	sp := s.state.SessionProvider()
	sessionName := agentSessionName(s.state.CityName(), name, cfg.Workspace.SessionTemplate)

	if !sp.IsRunning(sessionName) {
		writeError(w, http.StatusNotFound, "not_found", "agent "+name+" not running")
		return
	}

	output, err := sp.Peek(sessionName, 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	turns := []outputTurn{}
	if output != "" {
		turns = append(turns, outputTurn{Role: "output", Text: output})
	}

	writeJSON(w, http.StatusOK, agentOutputResponse{
		Agent:  name,
		Format: "text",
		Turns:  turns,
	})
}

// resolveAgentWorkDir returns the absolute working directory for an agent.
// For rig-scoped agents, this is the rig's Path. For city-scoped agents,
// this is the city root.
func (s *Server) resolveAgentWorkDir(a config.Agent) string {
	if a.Dir == "" {
		return s.state.CityPath()
	}
	cfg := s.state.Config()
	for _, rig := range cfg.Rigs {
		if rig.Name == a.Dir {
			return rig.Path
		}
	}
	return ""
}

// entryToTurn converts a sessionlog Entry to a human-readable outputTurn.
func entryToTurn(e *sessionlog.Entry) outputTurn {
	turn := outputTurn{
		Role: e.Type,
	}
	if !e.Timestamp.IsZero() {
		turn.Timestamp = e.Timestamp.Format("2006-01-02T15:04:05Z07:00")
	}

	// Try plain string content (message is a JSON object with string content).
	if text := e.TextContent(); text != "" {
		turn.Text = text
		return turn
	}

	// Try structured content blocks — extract human-readable text.
	if blocks := e.ContentBlocks(); len(blocks) > 0 {
		var parts []string
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if b.Text != "" {
					parts = append(parts, b.Text)
				}
			case "tool_use":
				if b.Name != "" {
					parts = append(parts, "["+b.Name+"]")
				}
			case "thinking":
				if b.Text != "" {
					parts = append(parts, b.Text)
				}
			}
		}
		turn.Text = strings.Join(parts, "\n")
		return turn
	}

	// Claude JSONL double-encodes the message field as a JSON string
	// containing JSON. Unwrap and try again.
	turn.Text = unwrapDoubleEncoded(e.Message)
	return turn
}

// outputStreamPollInterval controls how often the stream checks for new output.
const outputStreamPollInterval = 2 * time.Second

// handleAgentOutputStream streams agent output as SSE events.
// New turns are sent as they appear; keepalives are sent every 15s.
//
// SSE event format:
//
//	event: turn
//	data: {"turns": [...]}
func (s *Server) handleAgentOutputStream(w http.ResponseWriter, r *http.Request, name string) {
	cfg := s.state.Config()
	agentCfg, ok := findAgent(cfg, name)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "agent "+name+" not found")
		return
	}

	// Set SSE headers before writing any data.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	if err := http.NewResponseController(w).Flush(); err != nil {
		_ = err
	}

	// Try session log streaming first, fall back to peek polling.
	workDir := s.resolveAgentWorkDir(agentCfg)
	searchPaths := s.sessionLogSearchPaths
	if searchPaths == nil {
		searchPaths = sessionlog.DefaultSearchPaths()
	}

	var logPath string
	if workDir != "" {
		logPath = sessionlog.FindSessionFile(searchPaths, workDir)
	}

	ctx := r.Context()
	if logPath != "" {
		s.streamSessionLog(ctx, w, name, logPath)
	} else {
		s.streamPeekOutput(ctx, w, name, cfg)
	}
}

// streamSessionLog polls a session log file and emits new turns as SSE events.
func (s *Server) streamSessionLog(ctx context.Context, w http.ResponseWriter, name string, logPath string) {
	poll := time.NewTicker(outputStreamPollInterval)
	defer poll.Stop()
	keepalive := time.NewTicker(sseKeepalive)
	defer keepalive.Stop()

	var lastModTime time.Time
	sentCount := 0
	var seq uint64

	// readAndEmit reads the file and sends any new turns.
	readAndEmit := func() {
		info, err := os.Stat(logPath)
		if err != nil {
			return
		}
		if info.ModTime().Equal(lastModTime) {
			return // file unchanged
		}
		lastModTime = info.ModTime()

		sess, err := sessionlog.ReadFile(logPath, 0) // tail=0: all messages
		if err != nil {
			return
		}

		turns := make([]outputTurn, 0, len(sess.Messages))
		for _, e := range sess.Messages {
			turn := entryToTurn(e)
			if turn.Text == "" {
				continue
			}
			turns = append(turns, turn)
		}

		if len(turns) <= sentCount {
			return // no new turns
		}
		newTurns := turns[sentCount:]
		sentCount = len(turns)
		seq++

		data, err := json.Marshal(agentOutputResponse{
			Agent:  name,
			Format: "conversation",
			Turns:  newTurns,
		})
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: turn\nid: %d\ndata: %s\n\n", seq, data) //nolint:errcheck
		if err := http.NewResponseController(w).Flush(); err != nil {
			_ = err
		}
	}

	// Emit initial state immediately.
	readAndEmit()

	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			readAndEmit()
		case <-keepalive.C:
			writeSSEComment(w, "keepalive")
		}
	}
}

// streamPeekOutput polls Peek() and emits changes as SSE events.
func (s *Server) streamPeekOutput(ctx context.Context, w http.ResponseWriter, name string, cfg *config.City) {
	sp := s.state.SessionProvider()
	sessionName := agentSessionName(s.state.CityName(), name, cfg.Workspace.SessionTemplate)

	poll := time.NewTicker(outputStreamPollInterval)
	defer poll.Stop()
	keepalive := time.NewTicker(sseKeepalive)
	defer keepalive.Stop()

	var lastOutput string
	var seq uint64

	emitPeek := func() {
		if !sp.IsRunning(sessionName) {
			return
		}
		output, err := sp.Peek(sessionName, 100)
		if err != nil || output == lastOutput {
			return
		}
		lastOutput = output
		seq++

		turns := []outputTurn{}
		if output != "" {
			turns = append(turns, outputTurn{Role: "output", Text: output})
		}
		data, err := json.Marshal(agentOutputResponse{
			Agent:  name,
			Format: "text",
			Turns:  turns,
		})
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: turn\nid: %d\ndata: %s\n\n", seq, data) //nolint:errcheck
		if err := http.NewResponseController(w).Flush(); err != nil {
			_ = err
		}
	}

	// Emit initial state immediately.
	emitPeek()

	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			emitPeek()
		case <-keepalive.C:
			writeSSEComment(w, "keepalive")
		}
	}
}

// unwrapDoubleEncoded handles Claude's double-encoded message format
// where the "message" field is a JSON string containing a JSON object.
// Returns the human-readable content text, or "" if not parseable.
func unwrapDoubleEncoded(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	// Try to unwrap: raw might be a JSON string like "{\"role\":...}"
	var inner string
	if err := json.Unmarshal(raw, &inner); err != nil {
		return ""
	}
	// Now inner is the JSON object as a string. Parse it.
	var mc sessionlog.MessageContent
	if err := json.Unmarshal([]byte(inner), &mc); err != nil {
		return ""
	}
	// Try string content.
	var s string
	if err := json.Unmarshal(mc.Content, &s); err == nil && s != "" {
		return s
	}
	// Try array of content blocks.
	var blocks []sessionlog.ContentBlock
	if err := json.Unmarshal(mc.Content, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}
