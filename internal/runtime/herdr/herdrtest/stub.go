// Package herdrtest is a stub herdr for tests in other packages: a `herdr`
// executable and a unix socket that answer the way herdr 0.9.0 does, so a test
// can drive the real [herdr.Provider] — its CLI calls and its socket activity
// tracker — without a herdr installed.
//
// It lives outside _test.go files, like sessiontest and workertest, because a
// test in another package (cmd/gc's nudge poller tests) needs it, and the herdr
// package's own fakes are unexported test helpers.
package herdrtest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"

	"github.com/gastownhall/gascity/internal/runtime/herdr"
)

// Stub is a stand-in herdr: a `herdr`
// executable for the CLI and a unix socket for the activity tracker's
// agent.list, both answering in the herdr 0.9.0 shape read live on 2026-10-04:
//
//	{"agent":"claude","name":"<session>","agent_status":"done",...}
//
// The agent's NAME is under "name"; "agent" carries the agent KIND. Errors go
// to stderr as an envelope with exit 1, as the real CLI writes them.
//
// One registered agent, whose status the test sets with setStatus. Two extra
// words drive the cases herdr has no status for: "absent" (no registered
// agent) and "down" (herdr unreachable: every CLI call fails and the socket
// refuses).
type Stub struct {
	t      testing.TB
	name   string // the agent's herdr name
	bin    string
	sock   string
	status string // file holding the current status word
	log    string // one line per CLI invocation, argv minus --session <name>
	sends  string // one line per `agent prompt`: the target
	ln     net.Listener
	mu     sync.Mutex
}

// Pane is the pane id the stub's one agent lives in.
const Pane = "w0:p1"

// New starts a stub herdr with one registered agent called name in the given
// status. It skips the test on Windows: the CLI is a POSIX shell script and the
// socket is a unix socket. Everything it creates is removed with the test.
func New(t testing.TB, name, status string) *Stub {
	t.Helper()
	if goruntime.GOOS == "windows" {
		t.Skip("stub herdr CLI is a POSIX shell script and a unix socket")
	}
	dir := t.TempDir()
	// Unix socket paths have a ~104-byte limit on darwin; t.TempDir is too deep.
	sockDir, err := os.MkdirTemp("", "gcnh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	s := &Stub{
		t:      t,
		name:   name,
		bin:    filepath.Join(dir, "herdr"),
		sock:   filepath.Join(sockDir, "h.sock"),
		status: filepath.Join(dir, "status"),
		log:    filepath.Join(dir, "invocations.log"),
		sends:  filepath.Join(dir, "sends.log"),
	}
	s.SetStatus(status)
	script := `#!/bin/sh
shift 2 # drop --session <name>
printf '%s\n' "$*" >> '` + s.log + `'
status=$(cat '` + s.status + `')
fail() { # <code> <message> <id>: herdr's error envelope, on stderr, exit 1
	printf '{"error":{"code":"%s","message":"%s"},"id":"%s"}\n' "$1" "$2" "$3" >&2
	exit 1
}
if [ "$status" = "down" ]; then
	printf 'Error: failed to connect to herdr session socket: Connection refused (os error 111)\n' >&2
	exit 1
fi
agent() {
	printf '{"agent":"claude","agent_session":"stub","agent_status":"%s","cwd":"/","focused":false,"foreground_cwd":"/","interactive_ready":true,"name":"` + name + `","pane_id":"` + Pane + `","revision":0,"state_change_seq":1,"tab_id":"w0:t1","terminal_id":"term1","terminal_title":"","terminal_title_stripped":"","workspace_id":"w0"}' "$status"
}
case "$1 $2" in
"agent get")
	if [ "$status" != "absent" ] && { [ "$3" = '` + name + `' ] || [ "$3" = '` + Pane + `' ]; }; then
		printf '{"id":"cli:agent:get","result":{"agent":'; agent; printf ',"type":"agent_info"}}\n'
	else
		fail agent_not_found "agent target $3 not found" cli:agent:get
	fi ;;
"agent list")
	if [ "$status" = "absent" ]; then
		printf '{"id":"cli:agent:list","result":{"agents":[],"type":"agent_list"}}\n'
	else
		printf '{"id":"cli:agent:list","result":{"agents":['; agent; printf '],"type":"agent_list"}}\n'
	fi ;;
"agent wait")
	# agent wait <name> --until idle --timeout <ms>
	if [ "$status" = "absent" ]; then
		fail agent_not_found "agent target $3 not found" cli:agent:wait
	fi
	if [ "$status" = "idle" ]; then
		printf '{"id":"cli:agent:wait","result":{"type":"agent_status"}}\n'
		exit 0
	fi
	sleep "$(awk "BEGIN { print $7 / 1000 }")"
	fail timeout "timed out waiting for agent status" cli:agent:wait ;;
"agent prompt")
	printf '%s\n' "$3" >> '` + s.sends + `'
	printf '{"id":"cli:agent:prompt","result":{"type":"ok"}}\n' ;;
*) printf '{"result":{}}\n' ;;
esac
`
	if err := os.WriteFile(s.bin, []byte(script), 0o755); err != nil {
		t.Fatalf("writing stub herdr: %v", err)
	}
	ln, err := net.Listen("unix", s.sock)
	if err != nil {
		t.Fatalf("listening on the stub herdr socket: %v", err)
	}
	s.ln = ln
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return s
}

// serve answers one socket request per connection, as herdr does. Only
// agent.list is served; anything else (events.subscribe) gets an error
// envelope, which leaves the tracker on its ticker.
func (s *Stub) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var req struct {
		ID     string `json:"id"`
		Method string `json:"method"`
	}
	if err := json.Unmarshal(line, &req); err != nil {
		return
	}
	status := s.currentStatus()
	if status == "down" {
		return // closed without a reply: the poll fails
	}
	if req.Method != "agent.list" {
		fmt.Fprintf(conn, `{"id":%q,"error":{"code":"unsupported","message":"stub serves agent.list only"}}`+"\n", req.ID) //nolint:errcheck
		return
	}
	agents := "[]"
	if status != "absent" {
		agents = fmt.Sprintf(`[{"agent":"claude","agent_session":"stub","agent_status":%q,"cwd":"/","focused":false,"interactive_ready":true,"name":%q,"pane_id":%q,"revision":0,"state_change_seq":1,"tab_id":"w0:t1","terminal_id":"term1","workspace_id":"w0"}]`,
			status, s.name, Pane)
	}
	fmt.Fprintf(conn, `{"id":%q,"result":{"agents":%s,"type":"agent_list"}}`+"\n", req.ID, agents) //nolint:errcheck
}

// SetStatus changes the agent's status word: a herdr agent_status, or "absent"
// or "down".
func (s *Stub) SetStatus(status string) {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp := s.status + ".tmp"
	if err := os.WriteFile(tmp, []byte(status), 0o644); err != nil {
		s.t.Fatalf("writing stub herdr status: %v", err)
	}
	if err := os.Rename(tmp, s.status); err != nil {
		s.t.Fatalf("writing stub herdr status: %v", err)
	}
}

func (s *Stub) currentStatus() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := os.ReadFile(s.status)
	return strings.TrimSpace(string(b))
}

func (s *Stub) lines(path string) []string {
	s.t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		s.t.Fatalf("reading %s: %v", path, err)
	}
	text := strings.TrimRight(string(b), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// SendCount is how many `agent prompt` calls reached the stub: each is one
// nudge pasted into the pane.
func (s *Stub) SendCount() int { return len(s.lines(s.sends)) }

// Calls returns the CLI invocations whose argv starts with prefix.
func (s *Stub) Calls(prefix string) []string {
	var out []string
	for _, l := range s.lines(s.log) {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return out
}

// Provider builds the real herdr Provider over the stub. metaDir is the
// provider's sidecar metadata directory and cityRoot its city root; both are
// the caller's temp dirs.
func (s *Stub) Provider(metaDir, cityRoot string) *herdr.Provider {
	return herdr.NewWithEndpoints("gcstub", metaDir, cityRoot, s.bin, s.sock, 0, 0)
}
