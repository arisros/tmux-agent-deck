// Package integration drives the real deck binary against a private tmux
// server. Every tmux command here names that server explicitly (-L), so the
// tests never reach the tmux server the developer works in.
package integration

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type harness struct {
	t      *testing.T
	socket string // -L name
	bin    string // deck binary
	fake   string // fake Claude: a sleep binary named like a Claude version
	state  string // DECK_STATE_DIR
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	dir := t.TempDir()
	h := &harness{
		t:      t,
		socket: fmt.Sprintf("deck-test-%d-%d", os.Getpid(), rand.Int()),
		bin:    filepath.Join(dir, "tmux-agent-deck", "bin", "deck"),
		fake:   filepath.Join(dir, "2.1.999"),
		state:  filepath.Join(dir, "state"),
	}
	build := exec.Command("go", "build", "-o", h.bin, "../../cmd/deck")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	if out, err := exec.Command("go", "build", "-o", h.fake, "./testdata/fakeclaude").CombinedOutput(); err != nil {
		t.Fatalf("build fake claude: %v\n%s", err, out)
	}

	h.tmux("-f", "/dev/null", "new-session", "-d", "-s", "alpha", "-x", "240", "-y", "70", "sleep 100000")
	sock := h.tmux("display-message", "-p", "#{socket_path}")
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", h.socket, "kill-server").Run()
		_ = os.Remove(sock)
	})
	// Jobs started by this server's hooks inherit its global environment.
	h.tmux("set-environment", "-g", "DECK_STATE_DIR", h.state)
	h.tmux("set-option", "-g", "focus-events", "on")
	h.tmux("set-option", "-g", "@deck-sound", "off")
	h.deck("", "tmux-init")
	return h
}

func (h *harness) env(pane string) []string {
	var env []string
	for _, e := range os.Environ() {
		// Inherited from the developer's own tmux; must never leak in.
		if !strings.HasPrefix(e, "TMUX=") && !strings.HasPrefix(e, "TMUX_PANE=") {
			env = append(env, e)
		}
	}
	env = append(env, "DECK_TMUX_SOCKET="+h.socket, "DECK_STATE_DIR="+h.state)
	if pane != "" {
		env = append(env, "TMUX_PANE="+pane)
	}
	return env
}

func (h *harness) tmux(args ...string) string {
	h.t.Helper()
	cmd := exec.Command("tmux", append([]string{"-L", h.socket}, args...)...)
	cmd.Env = h.env("")
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("tmux %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (h *harness) deck(stdin string, args ...string) string {
	h.t.Helper()
	cmd := exec.Command(h.bin, args...)
	cmd.Env = h.env("")
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("deck %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// hook runs `deck hook` the way Claude does: payload on stdin, TMUX_PANE set.
func (h *harness) hook(pane, event, extra string) time.Duration {
	h.t.Helper()
	payload := fmt.Sprintf(`{"hook_event_name":%q,"session_id":"sess-%s"%s}`, event, strings.TrimPrefix(pane, "%"), extra)
	cmd := exec.Command(h.bin, "hook")
	cmd.Env = h.env(pane)
	cmd.Stdin = strings.NewReader(payload)
	start := time.Now()
	out, err := cmd.CombinedOutput()
	took := time.Since(start)
	if err != nil || len(out) > 0 {
		h.t.Fatalf("hook %s: %v %s", event, err, out)
	}
	return took
}

// agent starts a fake Claude in a new pane of target and returns its id.
func (h *harness) agent(target string) string {
	h.t.Helper()
	pane := h.tmux("split-window", "-d", "-t", target, "-P", "-F", "#{pane_id}", h.fake)
	h.eventually(func() bool { return h.opt(pane, "pane_current_command") == "2.1.999" }, "fake claude running in "+pane)
	return pane
}

func (h *harness) opt(target, name string) string {
	return h.tmux("display-message", "-p", "-t", target, "#{"+name+"}")
}

func (h *harness) eventually(ok func() bool, what string) {
	h.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if ok() {
			return
		}
	}
	h.t.Fatalf("timed out waiting for %s", what)
}
