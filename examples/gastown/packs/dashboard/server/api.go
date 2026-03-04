package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// CommandRequest is the JSON request body for /api/run.
type CommandRequest struct {
	// Command is the command to run (without the binary prefix).
	// Example: "status --json" or "mail inbox"
	Command string `json:"command"`
	// Timeout in seconds (optional; see default/max run timeouts)
	Timeout int `json:"timeout,omitempty"`
	// Confirmed must be true for commands that require confirmation.
	Confirmed bool `json:"confirmed,omitempty"`
}

// CommandResponse is the JSON response from /api/run.
type CommandResponse struct {
	Success    bool   `json:"success"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"duration_ms"`
	Command    string `json:"command"`
}

// CommandListResponse is the JSON response from /api/commands.
type CommandListResponse struct {
	Commands []CommandInfo `json:"commands"`
}

// APIHandler handles API requests for the dashboard.
type APIHandler struct {
	// cityPath is the path to the city directory (used as working directory for gc commands).
	cityPath string
	// cityName is the human-readable city name.
	cityName string
	// apiURL is the GC API server URL. When set, handlers route through
	// the API instead of spawning subprocesses.
	apiURL string
	// apiClient is the shared HTTP client for API calls (nil when apiURL is empty).
	apiClient *http.Client
	// defaultRunTimeout is the default timeout for command execution.
	defaultRunTimeout time.Duration
	// maxRunTimeout is the maximum allowed timeout for command execution.
	maxRunTimeout time.Duration
	// Options cache
	optionsCache     *OptionsResponse
	optionsCacheTime time.Time
	optionsCacheMu   sync.RWMutex
	// cmdSem limits concurrent command executions to prevent resource exhaustion.
	cmdSem chan struct{}
	// csrfToken is validated on POST requests to prevent cross-site request forgery.
	csrfToken string
}

const optionsCacheTTL = 30 * time.Second

// maxConcurrentCommands limits how many subprocesses can run at once.
// handleOptions alone spawns 7; allow headroom for other concurrent handlers.
const maxConcurrentCommands = 12

// NewAPIHandler creates a new API handler with the given configuration.
func NewAPIHandler(cityPath, cityName, apiURL string, defaultRunTimeout, maxRunTimeout time.Duration, csrfToken string) *APIHandler {
	if csrfToken == "" {
		log.Printf("WARNING: APIHandler created with empty CSRF token — POST requests will not be protected")
	}
	h := &APIHandler{
		cityPath:          cityPath,
		cityName:          cityName,
		apiURL:            strings.TrimRight(apiURL, "/"),
		defaultRunTimeout: defaultRunTimeout,
		maxRunTimeout:     maxRunTimeout,
		cmdSem:            make(chan struct{}, maxConcurrentCommands),
		csrfToken:         csrfToken,
	}
	if h.apiURL != "" {
		h.apiClient = &http.Client{Timeout: 15 * time.Second}
	}
	return h
}

// useAPI returns true when the API client is configured.
func (h *APIHandler) useAPI() bool {
	return h.apiURL != "" && h.apiClient != nil
}

// apiGet performs a GET against the GC API server and returns the body.
func (h *APIHandler) apiGet(path string) ([]byte, error) {
	resp, err := h.apiClient.Get(h.apiURL + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return body, fmt.Errorf("API %s: status %d", path, resp.StatusCode)
	}
	return body, nil
}

// apiPost performs a POST against the GC API server and returns the body.
func (h *APIHandler) apiPost(path string, payload any) ([]byte, error) {
	var reqBody io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(data)
	}
	req, err := http.NewRequest(http.MethodPost, h.apiURL+path, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "1")

	resp, err := h.apiClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return body, fmt.Errorf("API POST %s: status %d: %s", path, resp.StatusCode, string(body))
	}
	return body, nil
}

// apiGetListRaw returns the raw "items" from an API list response.
func (h *APIHandler) apiGetListRaw(path string) (json.RawMessage, error) {
	body, err := h.apiGet(path)
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		Items json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, err
	}
	return wrapper.Items, nil
}

// ServeHTTP routes API requests to the appropriate handler.
func (h *APIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// No CORS headers — the dashboard is served from the same origin.

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Validate CSRF token on all POST requests.
	if r.Method == http.MethodPost && h.csrfToken != "" {
		if r.Header.Get("X-Dashboard-Token") != h.csrfToken {
			h.sendError(w, "Invalid or missing dashboard token", http.StatusForbidden)
			return
		}
	}

	path := strings.TrimPrefix(r.URL.Path, "/api")
	switch {
	case path == "/run" && r.Method == http.MethodPost:
		h.handleRun(w, r)
	case path == "/commands" && r.Method == http.MethodGet:
		h.handleCommands(w, r)
	case path == "/options" && r.Method == http.MethodGet:
		h.handleOptions(w, r)
	case path == "/mail/inbox" && r.Method == http.MethodGet:
		h.handleMailInbox(w, r)
	case path == "/mail/threads" && r.Method == http.MethodGet:
		h.handleMailThreads(w, r)
	case path == "/mail/read" && r.Method == http.MethodGet:
		h.handleMailRead(w, r)
	case path == "/mail/send" && r.Method == http.MethodPost:
		h.handleMailSend(w, r)
	case path == "/issues/show" && r.Method == http.MethodGet:
		h.handleIssueShow(w, r)
	case path == "/issues/create" && r.Method == http.MethodPost:
		h.handleIssueCreate(w, r)
	case path == "/issues/close" && r.Method == http.MethodPost:
		h.handleIssueClose(w, r)
	case path == "/issues/update" && r.Method == http.MethodPost:
		h.handleIssueUpdate(w, r)
	case path == "/pr/show" && r.Method == http.MethodGet:
		h.handlePRShow(w, r)
	case path == "/crew" && r.Method == http.MethodGet:
		h.handleCrew(w, r)
	case path == "/ready" && r.Method == http.MethodGet:
		h.handleReady(w, r)
	case path == "/events" && r.Method == http.MethodGet:
		h.handleSSE(w, r)
	case path == "/session/preview" && r.Method == http.MethodGet:
		h.handleSessionPreview(w, r)
	default:
		http.Error(w, "Not found", http.StatusNotFound)
	}
}

// handleRun executes a command and returns the result.
func (h *APIHandler) handleRun(w http.ResponseWriter, r *http.Request) {
	var req CommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate command against whitelist
	meta, err := ValidateCommand(req.Command)
	if err != nil {
		h.sendError(w, fmt.Sprintf("Command blocked: %v", err), http.StatusForbidden)
		return
	}

	// Enforce server-side confirmation for dangerous commands
	if meta.Confirm && !req.Confirmed {
		h.sendError(w, "This command requires confirmation (set confirmed: true)", http.StatusForbidden)
		return
	}

	// Try API fast-path for recognized commands.
	if h.useAPI() {
		if output, ok := h.runViaAPI(req.Command); ok {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(CommandResponse{
				Command: req.Command,
				Success: true,
				Output:  output,
			})
			return
		}
	}

	// Determine timeout
	timeout := h.defaultRunTimeout
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
		if timeout > h.maxRunTimeout {
			timeout = h.maxRunTimeout
		}
	}

	// Parse command into args
	args := parseCommandArgs(req.Command)
	if len(args) == 0 {
		h.sendError(w, "Empty command", http.StatusBadRequest)
		return
	}

	// Sanitize args
	args = SanitizeArgs(args)

	// Execute command using the binary specified in command metadata
	start := time.Now()
	output, err := h.runValidatedCommand(r.Context(), timeout, meta.Binary, args)
	duration := time.Since(start)

	resp := CommandResponse{
		Command:    req.Command,
		DurationMs: duration.Milliseconds(),
	}

	if err != nil {
		resp.Success = false
		resp.Error = err.Error()
		resp.Output = output // Include partial output on error
	} else {
		resp.Success = true
		resp.Output = output
	}

	// Log command execution (but not for safe read-only commands to reduce noise)
	if !meta.Safe || !resp.Success {
		_ = meta // silence unused warning for now
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// runViaAPI attempts to execute a recognized command via the GC API instead of
// spawning a subprocess. Returns (output, true) if the command was handled, or
// ("", false) to fall through to subprocess execution.
func (h *APIHandler) runViaAPI(command string) (string, bool) {
	parts := parseCommandArgs(command)
	if len(parts) == 0 {
		return "", false
	}

	switch parts[0] {
	case "status":
		body, err := h.apiGet("/v0/status")
		if err != nil {
			return "", false
		}
		return string(body), true

	case "agent":
		if len(parts) >= 2 && parts[1] == "list" {
			body, err := h.apiGet("/v0/agents")
			if err != nil {
				return "", false
			}
			return string(body), true
		}

	case "list":
		// bd list
		body, err := h.apiGet("/v0/beads")
		if err != nil {
			return "", false
		}
		return string(body), true

	case "show":
		// bd show <id>
		if len(parts) >= 2 {
			body, err := h.apiGet("/v0/bead/" + parts[1])
			if err != nil {
				return "", false
			}
			return string(body), true
		}

	case "mail":
		if len(parts) >= 2 {
			switch parts[1] {
			case "inbox", "check":
				body, err := h.apiGet("/v0/mail")
				if err != nil {
					return "", false
				}
				return string(body), true
			}
		}

	case "convoy":
		if len(parts) >= 2 {
			switch parts[1] {
			case "list":
				body, err := h.apiGet("/v0/convoys")
				if err != nil {
					return "", false
				}
				return string(body), true
			case "show", "status":
				if len(parts) >= 3 {
					body, err := h.apiGet("/v0/convoy/" + parts[2])
					if err != nil {
						return "", false
					}
					return string(body), true
				}
			}
		}

	case "rig":
		if len(parts) >= 2 && parts[1] == "list" {
			body, err := h.apiGet("/v0/rigs")
			if err != nil {
				return "", false
			}
			return string(body), true
		}

	case "hooks":
		if len(parts) >= 2 && parts[1] == "list" {
			body, err := h.apiGet("/v0/beads?status=hooked")
			if err != nil {
				return "", false
			}
			return string(body), true
		}

	case "sling":
		// POST /v0/sling with remaining args
		payload := map[string]interface{}{}
		if len(parts) >= 2 {
			payload["bead"] = parts[1]
		}
		if len(parts) >= 3 {
			payload["rig"] = parts[2]
		}
		body, err := h.apiPost("/v0/sling", payload)
		if err != nil {
			return "", false
		}
		return string(body), true
	}

	return "", false
}

// handleCommands returns the list of available commands for the palette.
func (h *APIHandler) handleCommands(w http.ResponseWriter, _ *http.Request) {
	resp := CommandListResponse{
		Commands: GetCommandList(),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// runValidatedCommand executes a command with the appropriate binary (gc or bd).
// The binary string comes from CommandMeta.Binary and determines the working directory.
func (h *APIHandler) runValidatedCommand(ctx context.Context, timeout time.Duration, binary string, args []string) (string, error) {
	dir := h.cityPath
	if binary == "bd" {
		// bd commands need to run from a rig directory where .beads/ lives.
		// Use the first rig's path as default. The caller can override via args.
		dir = h.cityPath
	}
	return h.runCommandWithSem(ctx, timeout, binary, args, dir)
}

// runCommandWithSem executes a command with semaphore-based concurrency limiting.
func (h *APIHandler) runCommandWithSem(ctx context.Context, timeout time.Duration, binary string, args []string, dir string) (string, error) {
	// Apply timeout first so it bounds both semaphore wait and command execution.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Acquire semaphore slot to limit concurrent subprocess spawns.
	select {
	case h.cmdSem <- struct{}{}:
		defer func() { <-h.cmdSem }()
	case <-ctx.Done():
		return "", fmt.Errorf("command slot unavailable: %w", ctx.Err())
	}

	return runCommand(ctx, binary, args, dir)
}

// runCommand creates an exec.CommandContext, sets Dir if provided, runs, and returns combined output.
func runCommand(ctx context.Context, binary string, args []string, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	// Ensure the command doesn't wait for stdin
	cmd.Stdin = nil

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	// Combine stdout and stderr for output
	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}

	if ctx.Err() == context.DeadlineExceeded {
		return output, fmt.Errorf("command timed out")
	}

	if err != nil {
		return output, fmt.Errorf("command failed: %v", err)
	}

	return output, nil
}

// sendError sends a JSON error response.
func (h *APIHandler) sendError(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(CommandResponse{
		Success: false,
		Error:   message,
	})
}

// ---------- Mail types and handlers ----------

// MailMessage represents a mail message for the API.
type MailMessage struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Subject   string `json:"subject"`
	Body      string `json:"body,omitempty"`
	Timestamp string `json:"timestamp"`
	Read      bool   `json:"read"`
	Priority  string `json:"priority,omitempty"`
	ThreadID  string `json:"thread_id,omitempty"`
	ReplyTo   string `json:"reply_to,omitempty"`
}

// MailInboxResponse is the response for /api/mail/inbox.
type MailInboxResponse struct {
	Messages    []MailMessage `json:"messages"`
	UnreadCount int           `json:"unread_count"`
	Total       int           `json:"total"`
}

// MailThread represents a group of messages in a conversation thread.
type MailThread struct {
	ThreadID    string        `json:"thread_id"`
	Subject     string        `json:"subject"`
	LastMessage MailMessage   `json:"last_message"`
	Messages    []MailMessage `json:"messages"`
	Count       int           `json:"count"`
	UnreadCount int           `json:"unread_count"`
}

// MailThreadsResponse is the response for /api/mail/threads.
type MailThreadsResponse struct {
	Threads     []MailThread `json:"threads"`
	UnreadCount int          `json:"unread_count"`
	Total       int          `json:"total"`
}

// handleMailInbox returns the user's inbox.
func (h *APIHandler) handleMailInbox(w http.ResponseWriter, r *http.Request) {
	if h.useAPI() {
		h.handleMailInboxAPI(w, r)
		return
	}
	h.handleMailInboxSubprocess(w, r)
}

func (h *APIHandler) handleMailInboxAPI(w http.ResponseWriter, _ *http.Request) {
	itemsRaw, err := h.apiGetListRaw("/v0/mail")
	if err != nil {
		h.sendError(w, "Failed to fetch inbox: "+err.Error(), http.StatusInternalServerError)
		return
	}
	var apiMsgs []apiMailMessage
	if err := json.Unmarshal(itemsRaw, &apiMsgs); err != nil {
		h.sendError(w, "Failed to parse inbox: "+err.Error(), http.StatusInternalServerError)
		return
	}

	messages := make([]MailMessage, 0, len(apiMsgs))
	unread := 0
	for _, m := range apiMsgs {
		msg := MailMessage{
			ID:        m.ID,
			From:      m.From,
			To:        m.To,
			Subject:   m.Subject,
			Body:      m.Body,
			Timestamp: m.CreatedAt.Format(time.RFC3339),
			Read:      m.Read,
			ThreadID:  m.ThreadID,
			ReplyTo:   m.ReplyTo,
		}
		if !m.Read {
			unread++
		}
		messages = append(messages, msg)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(MailInboxResponse{
		Messages:    messages,
		UnreadCount: unread,
		Total:       len(messages),
	})
}

func (h *APIHandler) handleMailInboxSubprocess(w http.ResponseWriter, r *http.Request) {
	output, err := h.runCommandWithSem(r.Context(), 10*time.Second, "gc", []string{"mail", "inbox", "--json"}, h.cityPath)
	if err != nil {
		output, err = h.runCommandWithSem(r.Context(), 10*time.Second, "gc", []string{"mail", "inbox"}, h.cityPath)
		if err != nil {
			h.sendError(w, "Failed to fetch inbox: "+err.Error(), http.StatusInternalServerError)
			return
		}
		messages := parseMailInboxText(output)
		unread := 0
		for _, m := range messages {
			if !m.Read {
				unread++
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(MailInboxResponse{
			Messages:    messages,
			UnreadCount: unread,
			Total:       len(messages),
		})
		return
	}

	var messages []MailMessage
	if err := json.Unmarshal([]byte(output), &messages); err != nil {
		h.sendError(w, "Failed to parse inbox: "+err.Error(), http.StatusInternalServerError)
		return
	}

	unread := 0
	for _, m := range messages {
		if !m.Read {
			unread++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(MailInboxResponse{
		Messages:    messages,
		UnreadCount: unread,
		Total:       len(messages),
	})
}

// handleMailThreads returns the inbox grouped by conversation threads.
func (h *APIHandler) handleMailThreads(w http.ResponseWriter, r *http.Request) {
	if h.useAPI() {
		h.handleMailThreadsAPI(w)
		return
	}
	h.handleMailThreadsSubprocess(w, r)
}

func (h *APIHandler) handleMailThreadsAPI(w http.ResponseWriter) {
	itemsRaw, err := h.apiGetListRaw("/v0/mail")
	if err != nil {
		h.sendError(w, "Failed to fetch inbox: "+err.Error(), http.StatusInternalServerError)
		return
	}
	var apiMsgs []apiMailMessage
	if err := json.Unmarshal(itemsRaw, &apiMsgs); err != nil {
		h.sendError(w, "Failed to parse inbox: "+err.Error(), http.StatusInternalServerError)
		return
	}

	messages := make([]MailMessage, 0, len(apiMsgs))
	for _, m := range apiMsgs {
		messages = append(messages, MailMessage{
			ID:        m.ID,
			From:      m.From,
			To:        m.To,
			Subject:   m.Subject,
			Body:      m.Body,
			Timestamp: m.CreatedAt.Format(time.RFC3339),
			Read:      m.Read,
			ThreadID:  m.ThreadID,
			ReplyTo:   m.ReplyTo,
		})
	}

	threads := groupIntoThreads(messages)
	unread := 0
	for _, t := range threads {
		unread += t.UnreadCount
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(MailThreadsResponse{
		Threads:     threads,
		UnreadCount: unread,
		Total:       len(messages),
	})
}

func (h *APIHandler) handleMailThreadsSubprocess(w http.ResponseWriter, r *http.Request) {
	output, err := h.runCommandWithSem(r.Context(), 10*time.Second, "gc", []string{"mail", "inbox", "--json"}, h.cityPath)
	if err != nil {
		// Fall back to text parsing
		output, err = h.runCommandWithSem(r.Context(), 10*time.Second, "gc", []string{"mail", "inbox"}, h.cityPath)
		if err != nil {
			h.sendError(w, "Failed to fetch inbox: "+err.Error(), http.StatusInternalServerError)
			return
		}
		messages := parseMailInboxText(output)
		threads := groupIntoThreads(messages)
		unread := 0
		for _, t := range threads {
			unread += t.UnreadCount
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(MailThreadsResponse{
			Threads:     threads,
			UnreadCount: unread,
			Total:       len(messages),
		})
		return
	}

	var messages []MailMessage
	if err := json.Unmarshal([]byte(output), &messages); err != nil {
		h.sendError(w, "Failed to parse inbox: "+err.Error(), http.StatusInternalServerError)
		return
	}

	threads := groupIntoThreads(messages)
	unread := 0
	for _, t := range threads {
		unread += t.UnreadCount
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(MailThreadsResponse{
		Threads:     threads,
		UnreadCount: unread,
		Total:       len(messages),
	})
}

// groupIntoThreads groups messages into conversation threads.
// Messages are grouped by ThreadID when available, otherwise by ReplyTo chain,
// and finally by subject similarity as a fallback.
func groupIntoThreads(messages []MailMessage) []MailThread {
	// Map from thread key to slice of messages
	threadMap := make(map[string][]MailMessage)
	// Track message ID -> thread key for reply-to chaining
	msgToThread := make(map[string]string)
	// Maintain insertion order of thread keys
	var threadOrder []string
	threadSeen := make(map[string]bool)

	for _, msg := range messages {
		var threadKey string

		// Priority 1: Use ThreadID if present
		if msg.ThreadID != "" {
			threadKey = "thread:" + msg.ThreadID
		} else if msg.ReplyTo != "" {
			// Priority 2: Follow reply-to chain
			if parentKey, ok := msgToThread[msg.ReplyTo]; ok {
				threadKey = parentKey
			} else {
				// Start a new thread anchored to the reply-to ID
				threadKey = "reply:" + msg.ReplyTo
			}
		} else {
			// Priority 3: Standalone message (its own thread)
			threadKey = "msg:" + msg.ID
		}

		threadMap[threadKey] = append(threadMap[threadKey], msg)
		msgToThread[msg.ID] = threadKey

		if !threadSeen[threadKey] {
			threadOrder = append(threadOrder, threadKey)
			threadSeen[threadKey] = true
		}
	}

	// Build thread structs, ordered by most recent message
	var threads []MailThread
	for _, key := range threadOrder {
		msgs := threadMap[key]
		if len(msgs) == 0 {
			continue
		}

		// Last message is the most recent (messages come in chronological order)
		last := msgs[len(msgs)-1]

		// Use the first message's subject as the thread subject (strip Re: prefixes)
		subject := msgs[0].Subject
		subject = strings.TrimPrefix(subject, "Re: ")
		subject = strings.TrimPrefix(subject, "RE: ")

		unread := 0
		for _, m := range msgs {
			if !m.Read {
				unread++
			}
		}

		threadID := key
		if last.ThreadID != "" {
			threadID = last.ThreadID
		}

		threads = append(threads, MailThread{
			ThreadID:    threadID,
			Subject:     subject,
			LastMessage: last,
			Messages:    msgs,
			Count:       len(msgs),
			UnreadCount: unread,
		})
	}

	return threads
}

// handleMailRead reads a specific message by ID.
func (h *APIHandler) handleMailRead(w http.ResponseWriter, r *http.Request) {
	msgID := r.URL.Query().Get("id")
	if msgID == "" {
		h.sendError(w, "Missing message ID", http.StatusBadRequest)
		return
	}
	if !isValidID(msgID) {
		h.sendError(w, "Invalid message ID format", http.StatusBadRequest)
		return
	}

	if h.useAPI() {
		body, err := h.apiGet("/v0/mail/" + msgID)
		if err != nil {
			h.sendError(w, "Failed to read message: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// Mark as read via API.
		_, _ = h.apiPost("/v0/mail/"+msgID+"/read", nil)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
		return
	}

	output, err := h.runCommandWithSem(r.Context(), 10*time.Second, "gc", []string{"mail", "read", msgID}, h.cityPath)
	if err != nil {
		h.sendError(w, "Failed to read message: "+err.Error(), http.StatusInternalServerError)
		return
	}

	msg := parseMailReadOutput(output, msgID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(msg)
}

// MailSendRequest is the request body for /api/mail/send.
type MailSendRequest struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	ReplyTo string `json:"reply_to,omitempty"`
}

// handleMailSend sends a new message.
func (h *APIHandler) handleMailSend(w http.ResponseWriter, r *http.Request) {
	var req MailSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.To == "" || req.Subject == "" {
		h.sendError(w, "Missing required fields (to, subject)", http.StatusBadRequest)
		return
	}
	if !isValidMailAddress(req.To) {
		h.sendError(w, "Invalid recipient format", http.StatusBadRequest)
		return
	}
	if req.ReplyTo != "" && !isValidID(req.ReplyTo) {
		h.sendError(w, "Invalid reply-to ID format", http.StatusBadRequest)
		return
	}

	const maxSubjectLen = 500
	const maxBodyLen = 100_000
	if len(req.Subject) > maxSubjectLen {
		h.sendError(w, fmt.Sprintf("Subject too long (max %d bytes)", maxSubjectLen), http.StatusBadRequest)
		return
	}
	if len(req.Body) > maxBodyLen {
		h.sendError(w, fmt.Sprintf("Body too long (max %d bytes)", maxBodyLen), http.StatusBadRequest)
		return
	}
	if strings.Contains(req.Subject, "\x00") || strings.Contains(req.Body, "\x00") {
		h.sendError(w, "Subject and body cannot contain null bytes", http.StatusBadRequest)
		return
	}

	if h.useAPI() {
		apiReq := map[string]string{
			"to":      req.To,
			"subject": req.Subject,
			"body":    req.Body,
		}
		_, err := h.apiPost("/v0/mail", apiReq)
		if err != nil {
			h.sendError(w, "Failed to send message: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Message sent",
		})
		return
	}

	args := []string{"mail", "send"}
	args = append(args, "-s", req.Subject)
	if req.Body != "" {
		args = append(args, "-m", req.Body)
	}
	if req.ReplyTo != "" {
		args = append(args, "--reply-to", req.ReplyTo)
	}
	args = append(args, "--", req.To)

	output, err := h.runCommandWithSem(r.Context(), 30*time.Second, "gc", args, h.cityPath)
	if err != nil {
		h.sendError(w, "Failed to send message: "+err.Error()+"\n"+output, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Message sent",
		"output":  output,
	})
}

// parseMailInboxText parses text output from "gc mail inbox".
func parseMailInboxText(output string) []MailMessage {
	var messages []MailMessage
	lines := strings.Split(output, "\n")

	var current *MailMessage
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "(no messages)") {
			continue
		}

		// Check for numbered message line
		if len(trimmed) > 2 && trimmed[0] >= '1' && trimmed[0] <= '9' && trimmed[1] == '.' {
			// Save previous message
			if current != nil {
				messages = append(messages, *current)
			}
			current = &MailMessage{}
			rest := strings.TrimSpace(trimmed[2:])
			if strings.Contains(rest, "unread") || strings.HasPrefix(rest, "*") {
				current.Read = false
				current.Subject = strings.TrimSpace(strings.TrimPrefix(rest, "*"))
			} else {
				current.Read = true
				current.Subject = rest
			}
		} else if current != nil && current.ID == "" && strings.Contains(trimmed, " from ") {
			parts := strings.SplitN(trimmed, " from ", 2)
			if len(parts) == 2 {
				current.ID = strings.TrimSpace(parts[0])
				current.From = strings.TrimSpace(parts[1])
			}
		} else if current != nil && current.Timestamp == "" && (strings.Contains(trimmed, "-") || strings.Contains(trimmed, ":")) {
			current.Timestamp = trimmed
		}
	}
	// Don't forget the last one
	if current != nil && current.ID != "" {
		messages = append(messages, *current)
	}

	return messages
}

// parseMailReadOutput parses the output from "gc mail read <id>".
func parseMailReadOutput(output string, msgID string) MailMessage {
	msg := MailMessage{ID: msgID}
	lines := strings.Split(output, "\n")

	inBody := false
	var bodyLines []string

	for _, line := range lines {
		if strings.HasPrefix(line, "Subject: ") {
			msg.Subject = strings.TrimSpace(strings.TrimPrefix(line, "Subject: "))
		} else if strings.HasPrefix(line, "From: ") {
			msg.From = strings.TrimPrefix(line, "From: ")
		} else if strings.HasPrefix(line, "To: ") {
			msg.To = strings.TrimPrefix(line, "To: ")
		} else if strings.HasPrefix(line, "ID: ") {
			msg.ID = strings.TrimPrefix(line, "ID: ")
		} else if strings.HasPrefix(line, "Thread: ") {
			msg.ThreadID = strings.TrimSpace(strings.TrimPrefix(line, "Thread: "))
		} else if strings.HasPrefix(line, "Reply-To: ") {
			msg.ReplyTo = strings.TrimSpace(strings.TrimPrefix(line, "Reply-To: "))
		} else if line == "" && msg.From != "" && !inBody {
			inBody = true
		} else if inBody {
			bodyLines = append(bodyLines, line)
		}
	}

	msg.Body = strings.TrimSpace(strings.Join(bodyLines, "\n"))
	return msg
}

// ---------- Options handler ----------

// OptionItem represents an option with name and status.
type OptionItem struct {
	Name    string `json:"name"`
	Status  string `json:"status,omitempty"`
	Running bool   `json:"running,omitempty"`
}

// OptionsResponse is the JSON response from /api/options.
type OptionsResponse struct {
	Rigs        []string     `json:"rigs,omitempty"`
	Agents      []OptionItem `json:"agents,omitempty"`
	Convoys     []string     `json:"convoys,omitempty"`
	Hooks       []string     `json:"hooks,omitempty"`
	Messages    []string     `json:"messages,omitempty"`
	Crew        []string     `json:"crew,omitempty"`
	Escalations []string     `json:"escalations,omitempty"`
}

// handleOptions returns dynamic options for command arguments.
// Results are cached for 30 seconds to avoid slow repeated fetches.
func (h *APIHandler) handleOptions(w http.ResponseWriter, r *http.Request) {
	// Check cache first — serialize under RLock to a buffer so we don't
	// hold the lock while writing to the ResponseWriter (which can block
	// on slow clients).
	h.optionsCacheMu.RLock()
	if h.optionsCache != nil && time.Since(h.optionsCacheTime) < optionsCacheTTL {
		data, err := json.Marshal(h.optionsCache)
		h.optionsCacheMu.RUnlock()
		if err == nil {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Cache", "HIT")
			_, _ = w.Write(data)
			_, _ = w.Write([]byte("\n"))
			return
		}
		// Marshal failure is unexpected; fall through to refetch.
	} else {
		h.optionsCacheMu.RUnlock()
	}

	var resp *OptionsResponse
	if h.useAPI() {
		resp = h.fetchOptionsAPI()
	} else {
		resp = h.fetchOptionsSubprocess(r)
	}

	// Update cache
	h.optionsCacheMu.Lock()
	h.optionsCache = resp
	h.optionsCacheTime = time.Now()
	h.optionsCacheMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "MISS")
	_ = json.NewEncoder(w).Encode(resp)
}

// fetchOptionsAPI fetches options data from the GC API server.
func (h *APIHandler) fetchOptionsAPI() *OptionsResponse {
	resp := &OptionsResponse{}
	var wg sync.WaitGroup
	var mu sync.Mutex

	wg.Add(4)

	// Fetch rigs
	go func() {
		defer wg.Done()
		itemsRaw, err := h.apiGetListRaw("/v0/rigs")
		if err != nil {
			log.Printf("warning: handleOptions API: rigs: %v", err)
			return
		}
		var rigs []struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(itemsRaw, &rigs) == nil {
			mu.Lock()
			for _, r := range rigs {
				resp.Rigs = append(resp.Rigs, r.Name)
			}
			mu.Unlock()
		}
	}()

	// Fetch agents (provides agents, crew, and convoys are a bonus)
	go func() {
		defer wg.Done()
		itemsRaw, err := h.apiGetListRaw("/v0/agents")
		if err != nil {
			log.Printf("warning: handleOptions API: agents: %v", err)
			return
		}
		var agents []apiAgentResponse
		if json.Unmarshal(itemsRaw, &agents) == nil {
			mu.Lock()
			for _, a := range agents {
				state := "stopped"
				if a.Running {
					state = "running"
				}
				resp.Agents = append(resp.Agents, OptionItem{
					Name:    a.Name,
					Status:  state,
					Running: a.Running,
				})
				rigPrefix := ""
				if a.Rig != "" {
					rigPrefix = a.Rig + "/"
				}
				resp.Crew = append(resp.Crew, rigPrefix+a.Name)
			}
			mu.Unlock()
		}
	}()

	// Fetch hooked beads (provides hooks list)
	go func() {
		defer wg.Done()
		itemsRaw, err := h.apiGetListRaw("/v0/beads?status=hooked")
		if err != nil {
			log.Printf("warning: handleOptions API: hooks: %v", err)
			return
		}
		var beads []apiBead
		if json.Unmarshal(itemsRaw, &beads) == nil {
			mu.Lock()
			for _, b := range beads {
				resp.Hooks = append(resp.Hooks, b.ID)
			}
			mu.Unlock()
		}
	}()

	// Fetch mail (provides message IDs)
	go func() {
		defer wg.Done()
		itemsRaw, err := h.apiGetListRaw("/v0/mail")
		if err != nil {
			log.Printf("warning: handleOptions API: mail: %v", err)
			return
		}
		var msgs []apiMailMessage
		if json.Unmarshal(itemsRaw, &msgs) == nil {
			mu.Lock()
			for _, m := range msgs {
				resp.Messages = append(resp.Messages, m.ID)
			}
			mu.Unlock()
		}
	}()

	wg.Wait()
	return resp
}

// fetchOptionsSubprocess fetches options data by spawning subprocesses.
func (h *APIHandler) fetchOptionsSubprocess(r *http.Request) *OptionsResponse {
	resp := &OptionsResponse{}
	var wg sync.WaitGroup
	var mu sync.Mutex

	// Run all fetches in parallel with shorter timeouts
	wg.Add(6)

	// Fetch rigs
	go func() {
		defer wg.Done()
		if output, err := h.runCommandWithSem(r.Context(), 3*time.Second, "gc", []string{"rig", "list"}, h.cityPath); err == nil {
			mu.Lock()
			resp.Rigs = parseRigListOutput(output)
			mu.Unlock()
		} else {
			log.Printf("warning: handleOptions: rig list: %v", err)
		}
	}()

	// Fetch convoys
	go func() {
		defer wg.Done()
		if output, err := h.runCommandWithSem(r.Context(), 3*time.Second, "bd", []string{"list", "--type=convoy", "--json"}, h.cityPath); err == nil {
			mu.Lock()
			resp.Convoys = parseConvoyListJSON(output)
			mu.Unlock()
		} else {
			log.Printf("warning: handleOptions: convoy list: %v", err)
		}
	}()

	// Fetch hooks
	go func() {
		defer wg.Done()
		if output, err := h.runCommandWithSem(r.Context(), 3*time.Second, "gc", []string{"hooks", "list"}, h.cityPath); err == nil {
			mu.Lock()
			resp.Hooks = parseHooksListOutput(output)
			mu.Unlock()
		} else {
			log.Printf("warning: handleOptions: hooks list: %v", err)
		}
	}()

	// Fetch mail messages
	go func() {
		defer wg.Done()
		if output, err := h.runCommandWithSem(r.Context(), 3*time.Second, "gc", []string{"mail", "inbox"}, h.cityPath); err == nil {
			mu.Lock()
			resp.Messages = parseMailInboxOutput(output)
			mu.Unlock()
		} else {
			log.Printf("warning: handleOptions: mail inbox: %v", err)
		}
	}()

	// Fetch crew members
	go func() {
		defer wg.Done()
		if output, err := h.runCommandWithSem(r.Context(), 3*time.Second, "gc", []string{"agent", "list", "--all"}, h.cityPath); err == nil {
			mu.Lock()
			resp.Crew = parseCrewListOutput(output)
			mu.Unlock()
		} else {
			log.Printf("warning: handleOptions: agent list: %v", err)
		}
	}()

	// Fetch agents - shorter timeout, skip if slow
	go func() {
		defer wg.Done()
		if output, err := h.runCommandWithSem(r.Context(), 5*time.Second, "gc", []string{"status", "--json"}, h.cityPath); err == nil {
			mu.Lock()
			resp.Agents = parseAgentsFromStatus(output)
			mu.Unlock()
		} else {
			log.Printf("warning: handleOptions: status: %v", err)
		}
	}()

	wg.Wait()
	return resp
}

// parseRigListOutput extracts rig names from the text output of "gc rig list".
func parseRigListOutput(output string) []string {
	var rigs []string
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		// Rig names are indented with 2 spaces and no colon
		trimmed := strings.TrimPrefix(line, "  ")
		if trimmed != line && !strings.Contains(trimmed, ":") && strings.TrimSpace(trimmed) != "" {
			name := strings.TrimSpace(trimmed)
			if name != "" && !strings.HasPrefix(name, "Rigs") {
				rigs = append(rigs, name)
			}
		}
	}
	return rigs
}

// parseConvoyListJSON extracts convoy IDs from JSON output of "bd list --type=convoy --json".
func parseConvoyListJSON(jsonStr string) []string {
	var convoys []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &convoys); err != nil {
		log.Printf("warning: parseConvoyListJSON: %v", err)
		return nil
	}
	ids := make([]string, 0, len(convoys))
	for _, c := range convoys {
		if c.ID != "" {
			ids = append(ids, c.ID)
		}
	}
	return ids
}

// parseHooksListOutput extracts bead names from hooks list output.
func parseHooksListOutput(output string) []string {
	var hooks []string
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Skip header lines and empty lines
		if trimmed != "" && !strings.HasPrefix(trimmed, "Hook") && !strings.HasPrefix(trimmed, "No ") && !strings.HasPrefix(trimmed, "BEAD") {
			parts := strings.Fields(trimmed)
			if len(parts) > 0 {
				hooks = append(hooks, parts[0])
			}
		}
	}
	return hooks
}

// parseMailInboxOutput extracts message IDs from mail inbox output.
func parseMailInboxOutput(output string) []string {
	var messages []string
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "Mail") && !strings.HasPrefix(trimmed, "No ") && !strings.HasPrefix(trimmed, "ID") && !strings.HasPrefix(trimmed, "---") {
			parts := strings.Fields(trimmed)
			if len(parts) > 0 {
				messages = append(messages, parts[0])
			}
		}
	}
	return messages
}

// parseCrewListOutput extracts agent names from agent list output.
func parseCrewListOutput(output string) []string {
	var crew []string
	lines := strings.Split(output, "\n")
	currentRig := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Check if this is a rig header (ends with :)
		if strings.HasSuffix(trimmed, ":") && !strings.Contains(trimmed, " ") {
			currentRig = strings.TrimSuffix(trimmed, ":")
			continue
		}
		// Skip non-agent lines
		if strings.HasPrefix(trimmed, "Crew") || strings.HasPrefix(trimmed, "Agents") || strings.HasPrefix(trimmed, "No ") {
			continue
		}
		// This should be an agent name
		if currentRig != "" {
			parts := strings.Fields(trimmed)
			if len(parts) > 0 {
				crew = append(crew, currentRig+"/"+parts[0])
			}
		}
	}
	return crew
}

// parseAgentsFromStatus extracts agents with status from "gc status --json" output.
func parseAgentsFromStatus(jsonStr string) []OptionItem {
	var status struct {
		Agents []struct {
			Name    string `json:"name"`
			Running bool   `json:"running"`
			State   string `json:"state"`
		} `json:"agents"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &status); err != nil {
		return nil
	}

	var agents []OptionItem
	for _, a := range status.Agents {
		state := a.State
		if state == "" {
			if a.Running {
				state = "running"
			} else {
				state = "stopped"
			}
		}
		agents = append(agents, OptionItem{
			Name:    a.Name,
			Status:  state,
			Running: a.Running,
		})
	}
	return agents
}

// ---------- Issue types and handlers ----------

// IssueShowResponse is the response for /api/issues/show.
type IssueShowResponse struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Type        string   `json:"type,omitempty"`
	Status      string   `json:"status,omitempty"`
	Priority    string   `json:"priority,omitempty"`
	Owner       string   `json:"owner,omitempty"`
	Description string   `json:"description,omitempty"`
	Created     string   `json:"created,omitempty"`
	Updated     string   `json:"updated,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"`
	Blocks      []string `json:"blocks,omitempty"`
	RawOutput   string   `json:"raw_output"`
}

// handleIssueShow returns details for a specific issue/bead.
func (h *APIHandler) handleIssueShow(w http.ResponseWriter, r *http.Request) {
	issueID := r.URL.Query().Get("id")
	if issueID == "" {
		h.sendError(w, "Missing issue ID", http.StatusBadRequest)
		return
	}

	showID := extractIssueID(issueID)
	if strings.HasPrefix(issueID, "external:") && showID == issueID {
		h.sendError(w, "Malformed external issue ID (expected external:prefix:id)", http.StatusBadRequest)
		return
	}
	if !isValidID(showID) {
		h.sendError(w, "Invalid issue ID format", http.StatusBadRequest)
		return
	}

	if h.useAPI() {
		body, err := h.apiGet("/v0/bead/" + showID)
		if err != nil {
			h.sendError(w, "Failed to fetch issue: "+err.Error(), http.StatusInternalServerError)
			return
		}
		var bead apiBead
		if err := json.Unmarshal(body, &bead); err != nil {
			h.sendError(w, "Failed to parse issue: "+err.Error(), http.StatusInternalServerError)
			return
		}
		resp := IssueShowResponse{
			ID:          issueID,
			Title:       bead.Title,
			Type:        bead.Type,
			Status:      bead.Status,
			Description: bead.Description,
			Created:     bead.CreatedAt.Format(time.RFC3339),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	output, err := h.runCommandWithSem(r.Context(), 10*time.Second, "bd", []string{"show", showID, "--json"}, h.cityPath)
	if err == nil {
		if resp, ok := parseIssueShowJSON(output); ok {
			resp.ID = issueID
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
	}

	output, err = h.runCommandWithSem(r.Context(), 10*time.Second, "bd", []string{"show", showID}, h.cityPath)
	if err != nil {
		h.sendError(w, "Failed to fetch issue: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := parseIssueShowOutput(output, issueID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// IssueCreateRequest is the request body for creating an issue.
type IssueCreateRequest struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Priority    int    `json:"priority,omitempty"` // 1-4, default 2
}

// IssueCreateResponse is the response from creating an issue.
type IssueCreateResponse struct {
	Success bool   `json:"success"`
	ID      string `json:"id,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// handleIssueCreate creates a new issue.
func (h *APIHandler) handleIssueCreate(w http.ResponseWriter, r *http.Request) {
	var req IssueCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Title == "" {
		h.sendError(w, "Title is required", http.StatusBadRequest)
		return
	}

	const maxTitleLen = 500
	const maxDescriptionLen = 100_000
	if len(req.Title) > maxTitleLen {
		h.sendError(w, fmt.Sprintf("Title too long (max %d bytes)", maxTitleLen), http.StatusBadRequest)
		return
	}
	if len(req.Description) > maxDescriptionLen {
		h.sendError(w, fmt.Sprintf("Description too long (max %d bytes)", maxDescriptionLen), http.StatusBadRequest)
		return
	}
	if strings.ContainsAny(req.Title, "\n\r\x00") {
		h.sendError(w, "Title cannot contain newlines or control characters", http.StatusBadRequest)
		return
	}
	if req.Description != "" && strings.Contains(req.Description, "\x00") {
		h.sendError(w, "Description cannot contain null characters", http.StatusBadRequest)
		return
	}

	if h.useAPI() {
		apiReq := map[string]interface{}{
			"title":       req.Title,
			"description": req.Description,
		}
		body, err := h.apiPost("/v0/beads", apiReq)
		resp := IssueCreateResponse{}
		if err != nil {
			resp.Success = false
			resp.Error = "Failed to create issue: " + err.Error()
		} else {
			resp.Success = true
			resp.Message = "Issue created"
			var bead apiBead
			if json.Unmarshal(body, &bead) == nil {
				resp.ID = bead.ID
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	args := []string{"create"}
	if req.Priority >= 1 && req.Priority <= 4 {
		args = append(args, fmt.Sprintf("--priority=%d", req.Priority))
	}
	if req.Description != "" {
		args = append(args, "--body", req.Description)
	}
	args = append(args, "--", req.Title)

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	output, err := h.runCommandWithSem(ctx, 12*time.Second, "bd", args, h.cityPath)

	resp := IssueCreateResponse{}
	if err != nil {
		resp.Success = false
		resp.Error = "Failed to create issue: " + err.Error()
		if output != "" {
			resp.Message = output
		}
	} else {
		resp.Success = true
		resp.Message = output
		if strings.Contains(output, "Created") {
			parts := strings.Fields(output)
			for i, p := range parts {
				if strings.HasSuffix(p, ":") && i+1 < len(parts) {
					resp.ID = strings.TrimSpace(parts[i+1])
					break
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// IssueCloseRequest is the request body for closing an issue.
type IssueCloseRequest struct {
	ID string `json:"id"`
}

// handleIssueClose closes an issue.
func (h *APIHandler) handleIssueClose(w http.ResponseWriter, r *http.Request) {
	var req IssueCloseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		h.sendError(w, "Issue ID is required", http.StatusBadRequest)
		return
	}
	if !isValidID(req.ID) {
		h.sendError(w, "Invalid issue ID format", http.StatusBadRequest)
		return
	}

	if h.useAPI() {
		_, err := h.apiPost("/v0/bead/"+req.ID+"/close", nil)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "Failed to close issue: " + err.Error(),
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Issue closed",
		})
		return
	}

	output, err := h.runCommandWithSem(r.Context(), 12*time.Second, "bd", []string{"close", req.ID}, h.cityPath)

	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Failed to close issue: " + err.Error(),
			"output":  output,
		})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Issue closed",
		"output":  output,
	})
}

// IssueUpdateRequest is the request body for updating an issue.
type IssueUpdateRequest struct {
	ID       string `json:"id"`
	Status   string `json:"status,omitempty"`   // "open", "in_progress"
	Priority int    `json:"priority,omitempty"` // 1-4
	Assignee string `json:"assignee,omitempty"`
}

// handleIssueUpdate updates issue fields.
func (h *APIHandler) handleIssueUpdate(w http.ResponseWriter, r *http.Request) {
	var req IssueUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		h.sendError(w, "Issue ID is required", http.StatusBadRequest)
		return
	}
	if !isValidID(req.ID) {
		h.sendError(w, "Invalid issue ID format", http.StatusBadRequest)
		return
	}

	hasUpdate := req.Status != "" || (req.Priority >= 1 && req.Priority <= 4) || req.Assignee != ""
	if !hasUpdate {
		h.sendError(w, "No update fields provided", http.StatusBadRequest)
		return
	}

	if req.Status != "" {
		switch req.Status {
		case "open", "in_progress":
		default:
			h.sendError(w, "Invalid status (allowed: open, in_progress)", http.StatusBadRequest)
			return
		}
	}

	if req.Assignee != "" && !isValidID(req.Assignee) {
		h.sendError(w, "Invalid assignee format", http.StatusBadRequest)
		return
	}

	if h.useAPI() {
		apiReq := make(map[string]interface{})
		if req.Assignee != "" {
			apiReq["assignee"] = req.Assignee
		}
		_, err := h.apiPost("/v0/bead/"+req.ID+"/update", apiReq)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "Failed to update issue: " + err.Error(),
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Issue updated",
		})
		return
	}

	args := []string{"update", req.ID}
	if req.Status != "" {
		args = append(args, "--status="+req.Status)
	}
	if req.Priority >= 1 && req.Priority <= 4 {
		args = append(args, fmt.Sprintf("--priority=%d", req.Priority))
	}
	if req.Assignee != "" {
		args = append(args, "--assignee="+req.Assignee)
	}

	output, err := h.runCommandWithSem(r.Context(), 12*time.Second, "bd", args, h.cityPath)

	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Failed to update issue: " + err.Error(),
			"output":  output,
		})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Issue updated",
		"output":  output,
	})
}

// parseIssueShowJSON parses the JSON output from "bd show <id> --json".
// Returns (response, true) on success, or (zero, false) if parsing fails.
func parseIssueShowJSON(output string) (IssueShowResponse, bool) {
	var items []struct {
		ID          string   `json:"id"`
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Status      string   `json:"status"`
		Priority    int      `json:"priority"`
		Type        string   `json:"issue_type"`
		Owner       string   `json:"owner"`
		CreatedAt   string   `json:"created_at"`
		UpdatedAt   string   `json:"updated_at"`
		DependsOn   []string `json:"depends_on,omitempty"`
		Blocks      []string `json:"blocks,omitempty"`
	}
	if err := json.Unmarshal([]byte(output), &items); err != nil || len(items) == 0 {
		return IssueShowResponse{}, false
	}
	item := items[0]

	priority := ""
	if item.Priority > 0 {
		priority = fmt.Sprintf("P%d", item.Priority)
	}

	return IssueShowResponse{
		ID:          item.ID,
		Title:       item.Title,
		Type:        item.Type,
		Status:      item.Status,
		Priority:    priority,
		Owner:       item.Owner,
		Description: item.Description,
		Created:     item.CreatedAt,
		Updated:     item.UpdatedAt,
		DependsOn:   item.DependsOn,
		Blocks:      item.Blocks,
		RawOutput:   output,
	}, true
}

// parseIssueShowOutput parses the text output from "bd show <id>".
// This is the fallback path when --json is unavailable.
func parseIssueShowOutput(output string, issueID string) IssueShowResponse {
	resp := IssueShowResponse{
		ID:        issueID,
		RawOutput: output,
	}

	lines := strings.Split(output, "\n")
	inDescription := false
	parsedFirstLine := false
	var descLines []string
	var dependsOn []string
	var blocks []string

	for _, line := range lines {
		if !parsedFirstLine && len(line) > 0 {
			parsedFirstLine = true
			if bracketIdx := strings.Index(line, "["); bracketIdx > 0 {
				beforeBracket := line[:bracketIdx]
				statusPart := line[bracketIdx:]

				statusPart = strings.Trim(statusPart, "[] ")
				statusParts := strings.Split(statusPart, " ")
				if len(statusParts) >= 1 {
					resp.Priority = strings.TrimSpace(statusParts[0])
				}
				if len(statusParts) >= 2 {
					resp.Status = strings.TrimSpace(statusParts[len(statusParts)-1])
				}

				// Parse title from before the bracket
				parts := strings.SplitN(beforeBracket, " ", 3)
				if len(parts) >= 3 {
					resp.Title = strings.TrimSpace(parts[2])
				} else if len(parts) >= 2 {
					resp.Title = strings.TrimSpace(parts[1])
				}
			}
			continue
		}

		if strings.HasPrefix(line, "Owner:") {
			ownerLine := strings.TrimPrefix(line, "Owner:")
			ownerParts := strings.SplitN(ownerLine, " ", 3)
			resp.Owner = strings.TrimSpace(ownerParts[0])
			if len(ownerParts) >= 3 && strings.Contains(ownerLine, "Type:") {
				idx := strings.Index(ownerLine, "Type:")
				if idx >= 0 {
					resp.Type = strings.TrimSpace(ownerLine[idx+5:])
				}
			}
		} else if strings.HasPrefix(line, "Type:") {
			resp.Type = strings.TrimSpace(strings.TrimPrefix(line, "Type:"))
		} else if strings.HasPrefix(line, "Created:") {
			parts := strings.SplitN(line, " ", 4)
			if len(parts) >= 2 {
				resp.Created = strings.TrimSpace(strings.TrimPrefix(parts[0]+" "+parts[1], "Created:"))
			}
			if strings.Contains(line, "Updated:") {
				idx := strings.Index(line, "Updated:")
				if idx >= 0 {
					resp.Updated = strings.TrimSpace(line[idx+8:])
				}
			}
		} else if line == "DESCRIPTION" {
			inDescription = true
		} else if line == "DEPENDS ON" || line == "BLOCKS" {
			inDescription = false
		} else if inDescription && strings.TrimSpace(line) != "" {
			descLines = append(descLines, line)
		} else if strings.HasPrefix(strings.TrimSpace(line), "->") || strings.Contains(line, "depends") {
			depLine := strings.TrimSpace(line)
			parts := strings.Fields(depLine)
			if len(parts) >= 2 {
				dependsOn = append(dependsOn, parts[len(parts)-1])
			}
		} else if strings.HasPrefix(strings.TrimSpace(line), "<-") || strings.Contains(line, "blocks") {
			blockLine := strings.TrimSpace(line)
			parts := strings.Fields(blockLine)
			if len(parts) >= 2 {
				blocks = append(blocks, parts[len(parts)-1])
			}
		}
	}

	resp.Description = strings.TrimSpace(strings.Join(descLines, "\n"))
	resp.DependsOn = dependsOn
	resp.Blocks = blocks

	return resp
}

// ---------- PR handler ----------

// PRShowResponse is the response for /api/pr/show.
type PRShowResponse struct {
	Number       int      `json:"number"`
	Title        string   `json:"title"`
	State        string   `json:"state"`
	Author       string   `json:"author"`
	URL          string   `json:"url"`
	Body         string   `json:"body"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
	Additions    int      `json:"additions"`
	Deletions    int      `json:"deletions"`
	ChangedFiles int      `json:"changed_files"`
	Mergeable    string   `json:"mergeable"`
	BaseRef      string   `json:"base_ref"`
	HeadRef      string   `json:"head_ref"`
	Labels       []string `json:"labels,omitempty"`
	Checks       []string `json:"checks,omitempty"`
	RawOutput    string   `json:"raw_output,omitempty"`
}

// handlePRShow returns details for a specific PR.
func (h *APIHandler) handlePRShow(w http.ResponseWriter, r *http.Request) {
	// Accept either repo/number or full URL
	repo := r.URL.Query().Get("repo")
	number := r.URL.Query().Get("number")
	prURL := r.URL.Query().Get("url")

	if prURL == "" && (repo == "" || number == "") {
		h.sendError(w, "Missing repo/number or url parameter", http.StatusBadRequest)
		return
	}

	// Validate inputs to prevent argument injection.
	if prURL != "" {
		const maxURLLen = 2000
		if len(prURL) > maxURLLen {
			h.sendError(w, fmt.Sprintf("PR URL too long (max %d bytes)", maxURLLen), http.StatusBadRequest)
			return
		}
		if strings.ContainsAny(prURL, "\x00\n\r") {
			h.sendError(w, "PR URL cannot contain null bytes or newlines", http.StatusBadRequest)
			return
		}
		if !strings.HasPrefix(prURL, "https://") {
			h.sendError(w, "PR URL must start with https://", http.StatusBadRequest)
			return
		}
	} else {
		if !isNumeric(number) {
			h.sendError(w, "Invalid PR number format", http.StatusBadRequest)
			return
		}
		if !isValidRepoRef(repo) {
			h.sendError(w, "Invalid repo format (expected owner/repo)", http.StatusBadRequest)
			return
		}
	}

	var args []string
	if prURL != "" {
		args = []string{"pr", "view", prURL, "--json", "number,title,state,author,url,body,createdAt,updatedAt,additions,deletions,changedFiles,mergeable,baseRefName,headRefName,labels,statusCheckRollup"}
	} else {
		args = []string{"pr", "view", number, "--repo", repo, "--json", "number,title,state,author,url,body,createdAt,updatedAt,additions,deletions,changedFiles,mergeable,baseRefName,headRefName,labels,statusCheckRollup"}
	}

	output, err := h.runGhCommand(r.Context(), 15*time.Second, args)
	if err != nil {
		h.sendError(w, "Failed to fetch PR: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Parse the JSON output
	resp := parsePRShowOutput(output)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// runGhCommand executes a gh command with the given args.
func (h *APIHandler) runGhCommand(ctx context.Context, timeout time.Duration, args []string) (string, error) {
	return h.runCommandWithSem(ctx, timeout, "gh", args, h.cityPath)
}

// parsePRShowOutput parses the JSON output from "gh pr view --json".
func parsePRShowOutput(jsonStr string) PRShowResponse {
	resp := PRShowResponse{
		RawOutput: jsonStr,
	}

	var data struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		State  string `json:"state"`
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
		URL          string `json:"url"`
		Body         string `json:"body"`
		CreatedAt    string `json:"createdAt"`
		UpdatedAt    string `json:"updatedAt"`
		Additions    int    `json:"additions"`
		Deletions    int    `json:"deletions"`
		ChangedFiles int    `json:"changedFiles"`
		Mergeable    string `json:"mergeable"`
		BaseRefName  string `json:"baseRefName"`
		HeadRefName  string `json:"headRefName"`
		Labels       []struct {
			Name string `json:"name"`
		} `json:"labels"`
		StatusCheckRollup []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"statusCheckRollup"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return resp
	}

	resp.Number = data.Number
	resp.Title = data.Title
	resp.State = data.State
	resp.Author = data.Author.Login
	resp.URL = data.URL
	resp.Body = data.Body
	resp.CreatedAt = data.CreatedAt
	resp.UpdatedAt = data.UpdatedAt
	resp.Additions = data.Additions
	resp.Deletions = data.Deletions
	resp.ChangedFiles = data.ChangedFiles
	resp.Mergeable = data.Mergeable
	resp.BaseRef = data.BaseRefName
	resp.HeadRef = data.HeadRefName

	for _, label := range data.Labels {
		resp.Labels = append(resp.Labels, label.Name)
	}

	for _, check := range data.StatusCheckRollup {
		status := check.Name + ": "
		if check.Conclusion != "" {
			status += check.Conclusion
		} else {
			status += check.Status
		}
		resp.Checks = append(resp.Checks, status)
	}

	// Clear raw output if parsing succeeded
	resp.RawOutput = ""

	return resp
}

// ---------- Crew handler ----------

// CrewMember represents a crew member's status for the dashboard.
type CrewMember struct {
	Name       string `json:"name"`
	Rig        string `json:"rig"`
	State      string `json:"state"` // spinning, finished, ready, questions
	Hook       string `json:"hook,omitempty"`
	HookTitle  string `json:"hook_title,omitempty"`
	Session    string `json:"session"` // attached, detached, none
	LastActive string `json:"last_active"`
}

// CrewResponse is the response for /api/crew.
type CrewResponse struct {
	Crew  []CrewMember            `json:"crew"`
	ByRig map[string][]CrewMember `json:"by_rig"`
	Total int                     `json:"total"`
}

// handleCrew returns crew status across all rigs with proper state detection.
func (h *APIHandler) handleCrew(w http.ResponseWriter, r *http.Request) {
	if h.useAPI() {
		h.handleCrewAPI(w)
		return
	}
	h.handleCrewSubprocess(w, r)
}

func (h *APIHandler) handleCrewAPI(w http.ResponseWriter) {
	resp := CrewResponse{
		Crew:  make([]CrewMember, 0),
		ByRig: make(map[string][]CrewMember),
	}

	itemsRaw, err := h.apiGetListRaw("/v0/agents")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	var agents []apiAgentResponse
	if err := json.Unmarshal(itemsRaw, &agents); err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	for _, agent := range agents {
		state := "ready"
		sessionStatus := "none"
		lastActive := ""

		if agent.Running {
			sessionStatus = "detached"
			if agent.Session != nil && agent.Session.LastActivity != nil {
				lastActive = formatTimestamp(*agent.Session.LastActivity)
				activityAge := time.Since(*agent.Session.LastActivity)
				if activityAge < 10*time.Minute {
					state = "spinning"
				} else {
					state = "questions"
				}
			} else {
				state = "spinning"
			}
		} else if agent.ActiveBead != "" {
			state = "finished"
		}

		member := CrewMember{
			Name:       agent.Name,
			Rig:        agent.Rig,
			State:      state,
			Hook:       agent.ActiveBead,
			Session:    sessionStatus,
			LastActive: lastActive,
		}
		resp.Crew = append(resp.Crew, member)
		resp.ByRig[agent.Rig] = append(resp.ByRig[agent.Rig], member)
	}
	resp.Total = len(resp.Crew)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *APIHandler) handleCrewSubprocess(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	output, err := h.runCommandWithSem(ctx, 10*time.Second, "gc", []string{"agent", "list", "--all", "--json"}, h.cityPath)

	resp := CrewResponse{
		Crew:  make([]CrewMember, 0),
		ByRig: make(map[string][]CrewMember),
	}

	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	var crewData []struct {
		Name    string `json:"name"`
		Rig     string `json:"rig"`
		Branch  string `json:"branch"`
		Session string `json:"session,omitempty"`
		Hook    string `json:"hook,omitempty"`
	}

	if err := json.Unmarshal([]byte(output), &crewData); err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	for _, c := range crewData {
		sessionName := c.Rig + "-" + c.Name
		state, lastActive, sessionStatus := h.detectCrewState(ctx, sessionName, c.Hook)

		member := CrewMember{
			Name:       c.Name,
			Rig:        c.Rig,
			State:      state,
			Hook:       c.Hook,
			Session:    sessionStatus,
			LastActive: lastActive,
		}
		resp.Crew = append(resp.Crew, member)
		resp.ByRig[c.Rig] = append(resp.ByRig[c.Rig], member)
	}
	resp.Total = len(resp.Crew)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// detectCrewState determines crew member state from tmux session.
// Returns: state (spinning/finished/questions/ready), lastActive string, session status
func (h *APIHandler) detectCrewState(ctx context.Context, sessionName, hook string) (string, string, string) {
	// Check if tmux session exists and get activity
	cmd := exec.CommandContext(ctx, "tmux", "list-sessions", "-F", "#{session_name}|#{window_activity}|#{session_attached}")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		// tmux not running - agent is ready (no session)
		return "ready", "", "none"
	}

	// Find our session
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	for _, line := range lines {
		parts := strings.Split(line, "|")
		if len(parts) < 3 || parts[0] != sessionName {
			continue
		}

		// Found session
		var activityUnix int64
		if _, err := fmt.Sscanf(parts[1], "%d", &activityUnix); err != nil {
			continue
		}
		attached := parts[2] == "1"

		sessionStatus := "detached"
		if attached {
			sessionStatus = "attached"
		}

		// Calculate activity age
		activityAge := time.Since(time.Unix(activityUnix, 0))
		lastActive := formatTimestamp(time.Unix(activityUnix, 0))

		// Check if Claude is running in the session
		isAgentRunning := h.isAgentRunningInSession(ctx, sessionName)

		// Determine state based on activity and agent status
		state := determineCrewState(activityAge, isAgentRunning, hook)

		// Check for questions if state is potentially finished
		if state == "finished" || (state == "ready" && hook != "") {
			if h.hasQuestionInPane(ctx, sessionName) {
				state = "questions"
			}
		}

		return state, lastActive, sessionStatus
	}

	// Session not found
	return "ready", "", "none"
}

// isAgentRunningInSession checks if an agent is actively running in a tmux session.
func (h *APIHandler) isAgentRunningInSession(ctx context.Context, sessionName string) bool {
	// Target pane 0 explicitly (:0.0) to avoid false positives from
	// user-created split panes running shells or other commands.
	cmd := exec.CommandContext(ctx, "tmux", "display-message", "-t", sessionName+":0.0", "-p", "#{pane_current_command}")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return false
	}

	output := strings.ToLower(strings.TrimSpace(stdout.String()))
	if output == "" {
		return false
	}
	// Check for common agent commands
	return strings.Contains(output, "claude") ||
		strings.Contains(output, "node") ||
		strings.Contains(output, "codex") ||
		strings.Contains(output, "opencode")
}

// hasQuestionInPane checks the last output for question indicators.
func (h *APIHandler) hasQuestionInPane(ctx context.Context, sessionName string) bool {
	cmd := exec.CommandContext(ctx, "tmux", "capture-pane", "-t", sessionName, "-p", "-J")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return false
	}

	// Get last few lines
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	lastLines := ""
	if len(lines) > 10 {
		lastLines = strings.Join(lines[len(lines)-10:], "\n")
	} else {
		lastLines = strings.Join(lines, "\n")
	}
	lastLines = strings.ToLower(lastLines)

	// Look for question indicators
	questionIndicators := []string{
		"?",
		"what do you think",
		"should i",
		"would you like",
		"please confirm",
		"waiting for",
		"need your input",
		"your thoughts",
		"let me know",
	}

	for _, indicator := range questionIndicators {
		if strings.Contains(lastLines, indicator) {
			return true
		}
	}
	return false
}

// determineCrewState determines state from activity and agent status.
func determineCrewState(activityAge time.Duration, isAgentRunning bool, hook string) string {
	if !isAgentRunning {
		if hook != "" {
			return "finished" // Had work, agent stopped = finished
		}
		return "ready" // No work, agent stopped = ready for work
	}

	// Agent is running
	switch {
	case activityAge < 2*time.Minute:
		return "spinning" // Active recently
	case activityAge < 10*time.Minute:
		return "spinning" // Still probably working
	default:
		return "questions" // Running but no activity = likely waiting for input
	}
}

// ---------- Ready handler ----------

// ReadyItem represents a ready work item.
type ReadyItem struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Priority int    `json:"priority"`
	Source   string `json:"source"` // rig name or "city"
	Type     string `json:"type"`   // issue, task, etc.
}

// ReadyResponse is the response for /api/ready.
type ReadyResponse struct {
	Items    []ReadyItem            `json:"items"`
	BySource map[string][]ReadyItem `json:"by_source"`
	Summary  struct {
		Total   int `json:"total"`
		P1Count int `json:"p1_count"`
		P2Count int `json:"p2_count"`
		P3Count int `json:"p3_count"`
	} `json:"summary"`
}

// handleReady returns ready work items across the city.
func (h *APIHandler) handleReady(w http.ResponseWriter, r *http.Request) {
	if h.useAPI() {
		h.handleReadyAPI(w)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	output, err := h.runCommandWithSem(ctx, 12*time.Second, "gc", []string{"ready", "--json"}, h.cityPath)

	resp := ReadyResponse{
		Items:    make([]ReadyItem, 0),
		BySource: make(map[string][]ReadyItem),
	}

	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	// Parse the JSON output from gc ready
	var readyData struct {
		Sources []struct {
			Name   string `json:"name"`
			Issues []struct {
				ID       string `json:"id"`
				Title    string `json:"title"`
				Priority int    `json:"priority"`
				Type     string `json:"type"`
			} `json:"issues"`
		} `json:"sources"`
		Summary struct {
			Total   int `json:"total"`
			P1Count int `json:"p1_count"`
			P2Count int `json:"p2_count"`
			P3Count int `json:"p3_count"`
		} `json:"summary"`
	}

	if err := json.Unmarshal([]byte(output), &readyData); err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	// Convert to ReadyItem format
	for _, src := range readyData.Sources {
		for _, issue := range src.Issues {
			item := ReadyItem{
				ID:       issue.ID,
				Title:    issue.Title,
				Priority: issue.Priority,
				Source:   src.Name,
				Type:     issue.Type,
			}
			resp.Items = append(resp.Items, item)
			resp.BySource[src.Name] = append(resp.BySource[src.Name], item)

			// Count priorities
			switch issue.Priority {
			case 1:
				resp.Summary.P1Count++
			case 2:
				resp.Summary.P2Count++
			case 3:
				resp.Summary.P3Count++
			}
		}
	}
	resp.Summary.Total = len(resp.Items)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *APIHandler) handleReadyAPI(w http.ResponseWriter) {
	resp := ReadyResponse{
		Items:    make([]ReadyItem, 0),
		BySource: make(map[string][]ReadyItem),
	}

	itemsRaw, err := h.apiGetListRaw("/v0/beads/ready")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	var readyBeads []apiBead
	if err := json.Unmarshal(itemsRaw, &readyBeads); err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	for _, b := range readyBeads {
		item := ReadyItem{
			ID:     b.ID,
			Title:  b.Title,
			Source: "city",
			Type:   b.Type,
		}
		resp.Items = append(resp.Items, item)
		resp.BySource["city"] = append(resp.BySource["city"], item)
	}
	resp.Summary.Total = len(resp.Items)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// ---------- Session preview handler ----------

// SessionPreviewResponse is the response for /api/session/preview.
type SessionPreviewResponse struct {
	Session   string `json:"session"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
}

// handleSessionPreview returns the last N lines of output for a session.
func (h *APIHandler) handleSessionPreview(w http.ResponseWriter, r *http.Request) {
	sessionName := r.URL.Query().Get("session")
	if sessionName == "" {
		h.sendError(w, "Missing session parameter", http.StatusBadRequest)
		return
	}

	for _, c := range sessionName {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			h.sendError(w, "Invalid session name: contains invalid characters", http.StatusBadRequest)
			return
		}
	}

	if h.useAPI() {
		body, err := h.apiGet("/v0/agent/" + sessionName + "/peek")
		if err != nil {
			h.sendError(w, "Failed to peek agent: "+err.Error(), http.StatusInternalServerError)
			return
		}
		var peekResp struct {
			Output string `json:"output"`
		}
		if json.Unmarshal(body, &peekResp) == nil {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(SessionPreviewResponse{
				Session:   sessionName,
				Content:   peekResp.Output,
				Timestamp: time.Now().Format(time.RFC3339),
			})
			return
		}
		// Fallback: return raw body as content.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(SessionPreviewResponse{
			Session:   sessionName,
			Content:   string(body),
			Timestamp: time.Now().Format(time.RFC3339),
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tmux", "capture-pane", "-t", sessionName, "-p", "-J", "-S", "-30")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			h.sendError(w, "tmux capture-pane timed out", http.StatusGatewayTimeout)
			return
		}
		h.sendError(w, "Failed to capture pane: "+stderr.String(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(SessionPreviewResponse{
		Session:   sessionName,
		Content:   stdout.String(),
		Timestamp: time.Now().Format(time.RFC3339),
	})
}

// ---------- Command parsing ----------

// parseCommandArgs splits a command string into args, respecting quotes.
func parseCommandArgs(command string) []string {
	var args []string
	var current strings.Builder
	inQuote := false
	quoteChar := rune(0)

	for _, r := range command {
		switch {
		case r == '"' || r == '\'':
			if inQuote && r == quoteChar {
				inQuote = false
				quoteChar = 0
			} else if !inQuote {
				inQuote = true
				quoteChar = r
			} else {
				current.WriteRune(r)
			}
		case r == ' ' && !inQuote:
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}

	if current.Len() > 0 {
		args = append(args, current.String())
	}

	return args
}

// ---------- SSE handler ----------

// handleSSE streams Server-Sent Events to the dashboard client.
func (h *APIHandler) handleSSE(w http.ResponseWriter, r *http.Request) {
	if h.useAPI() {
		h.handleSSEProxy(w, r)
		return
	}
	h.handleSSESubprocess(w, r)
}

// handleSSEProxy proxies the API server's SSE event stream to the browser.
func (h *APIHandler) handleSSEProxy(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// Connect to API event stream.
	sseURL := h.apiURL + "/v0/events/stream"
	if lastID := r.Header.Get("Last-Event-ID"); lastID != "" {
		sseURL += "?after_seq=" + lastID
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, sseURL, nil)
	if err != nil {
		http.Error(w, "SSE request failed", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Accept", "text/event-stream")

	// Use a client without timeout for the long-lived SSE connection.
	sseClient := &http.Client{Timeout: 0}
	resp, err := sseClient.Do(req)
	if err != nil {
		http.Error(w, "SSE connect failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Send initial connection event.
	fmt.Fprintf(w, "event: connected\ndata: ok\n\n")
	flusher.Flush()

	// Proxy the upstream SSE stream line by line.
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			// Rewrite upstream events as dashboard-update events.
			// The client expects "event: dashboard-update" to trigger re-render.
			chunk := string(buf[:n])
			if strings.Contains(chunk, "data:") {
				fmt.Fprintf(w, "event: dashboard-update\ndata: event\n\n")
			} else {
				_, _ = w.Write(buf[:n])
			}
			flusher.Flush()
		}
		if err != nil {
			return
		}
	}
}

// handleSSESubprocess polls gc status and compares hashes to detect changes.
func (h *APIHandler) handleSSESubprocess(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ctx := r.Context()

	fmt.Fprintf(w, "event: connected\ndata: ok\n\n")
	flusher.Flush()

	var lastHash string
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-keepalive.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case <-ticker.C:
			hash := h.computeDashboardHash(ctx)
			if hash != "" && hash != lastHash {
				lastHash = hash
				fmt.Fprintf(w, "event: dashboard-update\ndata: %s\n\n", hash)
				flusher.Flush()
			}
		}
	}
}

// computeDashboardHash generates a lightweight hash of key dashboard state.
// It runs quick commands in parallel and hashes their output to detect changes.
func (h *APIHandler) computeDashboardHash(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var mu sync.Mutex
	var parts []string

	var wg sync.WaitGroup
	wg.Add(3)

	// Check status
	go func() {
		defer wg.Done()
		if out, err := h.runCommandWithSem(ctx, 3*time.Second, "gc", []string{"status", "--json"}, h.cityPath); err == nil {
			mu.Lock()
			parts = append(parts, "status:"+out)
			mu.Unlock()
		}
	}()

	// Check hooks state
	go func() {
		defer wg.Done()
		if out, err := h.runCommandWithSem(ctx, 3*time.Second, "gc", []string{"hooks", "list"}, h.cityPath); err == nil {
			mu.Lock()
			parts = append(parts, "hooks:"+out)
			mu.Unlock()
		}
	}()

	// Check mail count
	go func() {
		defer wg.Done()
		if out, err := h.runCommandWithSem(ctx, 3*time.Second, "gc", []string{"mail", "inbox"}, h.cityPath); err == nil {
			mu.Lock()
			parts = append(parts, "mail:"+out)
			mu.Unlock()
		}
	}()

	wg.Wait()

	if len(parts) == 0 {
		return ""
	}

	h256 := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return fmt.Sprintf("%x", h256[:8])
}
