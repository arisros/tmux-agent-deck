// Package tmux is the deck's only way to talk to tmux: a thin exec wrapper,
// the format strings it depends on, and command batching.
package tmux

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// MaxBatchBytes keeps every tmux invocation well under the ~16 KB client
// message limit; one batch per pane of a large server blows past it and tmux
// answers "command too long".
const MaxBatchBytes = 8000

// Client runs tmux commands against one server.
type Client struct {
	// Socket selects a server: a path is passed as -S, a bare name as -L.
	// Empty uses $TMUX, which tmux sets for hooks and panes alike.
	Socket string
}

// FromEnv targets the server named by $DECK_TMUX_SOCKET, else the one the
// calling process runs in.
func FromEnv() Client { return Client{Socket: os.Getenv("DECK_TMUX_SOCKET")} }

func (c Client) command(args ...string) *exec.Cmd {
	return exec.Command("tmux", append(c.Flags(), args...)...)
}

// Flags are the server-selection arguments for a tmux command line.
func (c Client) Flags() []string {
	switch {
	case c.Socket == "":
		return nil
	case strings.Contains(c.Socket, "/"):
		return []string{"-S", c.Socket}
	}
	return []string{"-L", c.Socket}
}

// Run runs one tmux command and returns its stdout.
func (c Client) Run(args ...string) (string, error) {
	var out, errb bytes.Buffer
	cmd := c.command(args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// Batch runs commands joined with ";", in as few invocations as the size
// limit allows.
func (c Client) Batch(cmds [][]string) error {
	for _, chunk := range Chunk(cmds, MaxBatchBytes) {
		if _, err := c.Run(chunk...); err != nil {
			return err
		}
	}
	return nil
}

// Chunk joins commands with ";" separators into argument lists whose encoded
// size stays under limit. A single oversized command still gets its own chunk.
func Chunk(cmds [][]string, limit int) [][]string {
	var out [][]string
	var cur []string
	size := 0
	for _, c := range cmds {
		n := 0
		for _, a := range c {
			n += len(a) + 1
		}
		if len(cur) > 0 && size+n+2 > limit {
			out = append(out, cur)
			cur, size = nil, 0
		}
		if len(cur) > 0 {
			cur = append(cur, ";")
			size += 2
		}
		cur = append(cur, c...)
		size += n
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// Capture returns the visible screen of a pane.
func (c Client) Capture(pane string) (string, error) {
	return c.Run("capture-pane", "-p", "-t", pane)
}

// PaneOption reads one pane option; empty when unset.
func (c Client) PaneOption(pane, name string) (string, error) {
	out, err := c.Run("display-message", "-p", "-t", pane, "#{"+name+"}")
	return strings.TrimSpace(out), err
}

// Formats shared by the Go code and the tmux configuration.
const (
	// VisibleFormat is true when a pane is on screen: active in the active
	// window of an attached session.
	VisibleFormat = "#{&&:#{pane_active},#{&&:#{window_active},#{session_attached}}}"

	// IsClaudeFormat matches a pane whose foreground process is Claude Code,
	// which renames itself to its version number ("2.1.284").
	IsClaudeFormat = `#{||:#{m/r:^[0-9]+\.[0-9]+\.[0-9]+$,#{pane_current_command}},#{m:claude*,#{pane_current_command}}}`

	// AliveFormat is true while a pane still runs the agent the deck heard
	// from: its foreground command is the one remembered in @deck_cmd when a
	// hook last fired there. An agent started through a wrapper shows as the
	// wrapper ("node"), so no list of process names can tell. Claude Code is
	// also matched by name, since it renames itself after it starts.
	AliveFormat = `#{||:#{==:#{pane_current_command},#{@deck_cmd}},` + IsClaudeFormat + `}`
)

// Pane is one row of list-panes.
type Pane struct {
	ID, Session, SessionID, Window, WindowID, Index string
	WindowName, Command, Title, Path                string
	State, SID, Sidebar                             string
	// Reason is why a waiting agent waits, "permission Bash" or "question".
	Reason string
	// Cmd is the pane's foreground command when a hook last fired there.
	Cmd                                string
	Since                              int64
	PaneActive, WindowActive, Attached bool
	WindowPanes                        int
}

var paneFields = []string{
	"#{pane_id}", "#{session_name}", "#{session_id}", "#{window_index}", "#{window_id}", "#{pane_index}",
	"#{window_name}", "#{pane_current_command}", "#{pane_title}", "#{pane_current_path}",
	"#{@deck_state}", "#{@deck_sid}", "#{@deck_sidebar}", "#{@deck_since}",
	"#{pane_active}", "#{window_active}", "#{session_attached}", "#{window_panes}",
	"#{@deck_reason}", "#{@deck_cmd}",
}

// sep must survive tmux's output escaping: tmux prints control characters
// such as \x1f as octal text ("\037"), but passes a tab through.
const sep = "\t"

// ListPanes lists every pane on the server in one call.
func (c Client) ListPanes() ([]Pane, error) {
	out, err := c.Run("list-panes", "-a", "-F", strings.Join(paneFields, sep))
	if err != nil {
		return nil, err
	}
	return ParsePanes(out), nil
}

// ParsePanes parses ListPanes output.
func ParsePanes(out string) []Pane {
	var panes []Pane
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		f := strings.Split(line, sep)
		if len(f) != len(paneFields) {
			continue
		}
		since, _ := strconv.ParseInt(f[13], 10, 64)
		attached, _ := strconv.Atoi(f[16])
		windowPanes, _ := strconv.Atoi(f[17])
		panes = append(panes, Pane{
			ID: f[0], Session: f[1], SessionID: f[2], Window: f[3], WindowID: f[4], Index: f[5],
			WindowName: f[6], Command: f[7], Title: f[8], Path: f[9],
			State: f[10], SID: f[11], Sidebar: f[12], Since: since,
			PaneActive: f[14] == "1", WindowActive: f[15] == "1", Attached: attached > 0,
			WindowPanes: windowPanes, Reason: f[18], Cmd: f[19],
		})
	}
	return panes
}

// Alive matches the same panes as AliveFormat.
func (p Pane) Alive() bool {
	return (p.Cmd != "" && p.Command == p.Cmd) || IsClaude(p.Command)
}

// IsClaude matches the same processes as IsClaudeFormat.
func IsClaude(command string) bool {
	if strings.HasPrefix(command, "claude") {
		return true
	}
	parts := strings.Split(command, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if _, err := strconv.Atoi(p); err != nil || p == "" {
			return false
		}
	}
	return true
}
