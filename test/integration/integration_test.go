package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLifecycleAndFormats(t *testing.T) {
	h := newHarness(t)
	a := h.agent("alpha")

	steps := []struct {
		event, extra, state, icon string
	}{
		{"SessionStart", `,"source":"startup"`, "idle", "○"},
		{"UserPromptSubmit", "", "running", "●"},
		{"PreToolUse", "", "running", "●"},
		{"PermissionRequest", "", "waiting", "◆"},
		{"Notification", `,"notification_type":"permission_prompt"`, "waiting", "◆"},
		{"PostToolUse", "", "running", "●"},
		{"Stop", `,"background_tasks":[]`, "done", "✔"},
	}
	for _, s := range steps {
		h.hook(a, s.event, s.extra)
		if got := h.opt(a, "@deck_state"); got != s.state {
			t.Fatalf("after %s: state %q, want %q", s.event, got, s.state)
		}
		if icon := h.opt(a, "E:@deck_pane_icon"); !strings.Contains(icon, s.icon) {
			t.Errorf("after %s: pane icon %q lacks %s", s.event, icon, s.icon)
		}
	}
	if icon := h.opt(a, "E:@deck_window_icon"); !strings.Contains(icon, "✔") {
		t.Errorf("window icon %q lacks ✔", icon)
	}

	// Selecting the pane runs the tmux hook, which runs `deck focus`.
	h.tmux("select-pane", "-t", a)
	h.eventually(func() bool { return h.opt(a, "@deck_state") == "idle" }, "focus to clear done")

	h.hook(a, "SessionEnd", `,"reason":"prompt_input_exit"`)
	if got := h.opt(a, "@deck_state"); got != "" {
		t.Errorf("state after SessionEnd = %q", got)
	}
}

func TestReasonAndEventLog(t *testing.T) {
	h := newHarness(t)
	a := h.agent("alpha")
	h.hook(a, "SessionStart", `,"source":"startup"`)
	h.hook(a, "UserPromptSubmit", "")
	h.hook(a, "PermissionRequest", `,"tool_name":"Bash","tool_input":{"command":"kubectl delete ns prod"}`)
	if got := h.opt(a, "@deck_reason"); got != "permission Bash" {
		t.Fatalf("reason = %q, want permission Bash", got)
	}
	if out := h.deck("", "list"); !strings.Contains(out, "waiting (permission Bash)") {
		t.Errorf("list does not say why:\n%s", out)
	}
	h.hook(a, "PostToolUse", "")
	if got := h.opt(a, "@deck_reason"); got != "" {
		t.Errorf("reason = %q after the approval, want none", got)
	}
	h.hook(a, "PermissionRequest", `,"tool_name":"AskUserQuestion"`)
	if got := h.opt(a, "@deck_reason"); got != "question" {
		t.Errorf("reason = %q, want question", got)
	}
	h.hook(a, "SessionEnd", "")

	out := h.deck("", "events", "--pane", a)
	for _, want := range []string{
		"idle -> idle", "idle -> running", "running -> waiting", "permission Bash",
		"waiting -> running", "question", "End",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("events lack %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "kubectl") {
		t.Errorf("a tool argument reached the event log:\n%s", out)
	}
	if lines := strings.Count(strings.TrimSpace(h.deck("", "events", "--pane", a, "--json")), "\n") + 1; lines != 6 {
		t.Errorf("%d JSON events, want 6", lines)
	}
}

func TestWindowIconPrefersMostUrgent(t *testing.T) {
	h := newHarness(t)
	a, b := h.agent("alpha"), h.agent("alpha")
	h.hook(a, "UserPromptSubmit", "")
	h.hook(b, "UserPromptSubmit", "")
	h.hook(b, "PermissionRequest", "")
	if icon := h.opt(a, "E:@deck_window_icon"); !strings.Contains(icon, "◆") || strings.Contains(icon, "●") {
		t.Errorf("window icon %q, want only the waiting glyph", icon)
	}
}

func TestDeadAgentHidden(t *testing.T) {
	h := newHarness(t)
	a := h.agent("alpha")
	h.hook(a, "UserPromptSubmit", "")
	h.tmux("respawn-pane", "-k", "-t", a, "sleep 100000")
	h.eventually(func() bool { return h.opt(a, "pane_current_command") == "sleep" }, "claude replaced")
	if icon := h.opt(a, "E:@deck_pane_icon"); icon != "" {
		t.Errorf("dead agent still shows %q", icon)
	}
	if out := h.deck("", "list"); strings.Contains(out, "running") {
		t.Errorf("dead agent listed:\n%s", out)
	}
	// Listing swept it: nothing of the agent is left on the pane.
	for _, o := range []string{"@deck_state", "@deck_sid", "@deck_cmd"} {
		if got := h.opt(a, o); got != "" {
			t.Errorf("%s = %q after the agent exited", o, got)
		}
	}
	if out := h.deck("", "events", "--pane", a); !strings.Contains(out, "Exit") {
		t.Errorf("the exit is not in the event log:\n%s", out)
	}
}

// An agent started through a wrapper has a process name the deck cannot
// know. It is tracked by the command its pane ran when its hooks fired.
func TestAgentBehindAWrapperIsTracked(t *testing.T) {
	h := newHarness(t)
	a := h.tmux("split-window", "-d", "-t", "alpha", "-P", "-F", "#{pane_id}", "sleep 100000")
	h.eventually(func() bool { return h.opt(a, "pane_current_command") == "sleep" }, "wrapper running")
	h.hook(a, "UserPromptSubmit", "")
	if got := h.opt(a, "@deck_cmd"); got != "sleep" {
		t.Fatalf("@deck_cmd = %q, want the pane's command", got)
	}
	if icon := h.opt(a, "E:@deck_pane_icon"); !strings.Contains(icon, "●") {
		t.Errorf("pane icon %q lacks the running dot", icon)
	}
	if out := h.deck("", "list"); !strings.Contains(out, "running") {
		t.Errorf("wrapped agent not listed:\n%s", out)
	}
	h.tmux("respawn-pane", "-k", "-t", a, "cat")
	h.eventually(func() bool { return h.opt(a, "pane_current_command") == "cat" }, "wrapper replaced")
	if icon := h.opt(a, "E:@deck_pane_icon"); icon != "" {
		t.Errorf("exited agent still shows %q", icon)
	}
	if out := h.deck("", "list"); strings.Contains(out, "running") {
		t.Errorf("exited agent listed:\n%s", out)
	}
}

func TestReconcileFromScreen(t *testing.T) {
	h := newHarness(t)
	// An agent whose screen ends in Claude's "Interrupted" marker, as after Esc.
	idle := h.tmux("split-window", "-d", "-t", "alpha", "-P", "-F", "#{pane_id}",
		`printf '  ⎿  Interrupted · What should Claude do instead?\n──────────────────\n❯ \n──────────────────\n  ⏸ manual mode on · ? for shortcuts\n'; exec `+h.fake)
	h.eventually(func() bool { return h.opt(idle, "pane_current_command") == "2.1.999" }, "fake claude")
	h.hook(idle, "UserPromptSubmit", "")
	h.hook(idle, "PermissionRequest", "")
	h.deck("", "reconcile", idle)
	if got := h.opt(idle, "@deck_state"); got != "idle" {
		t.Errorf("state = %q, want idle after the screen shows the prompt", got)
	}

	// A blank screen is not evidence of anything: leave the state alone.
	blank := h.agent("alpha")
	h.hook(blank, "UserPromptSubmit", "")
	h.deck("", "reconcile", blank)
	if got := h.opt(blank, "@deck_state"); got != "running" {
		t.Errorf("state = %q, want running kept", got)
	}
}

func TestSidebarFollowsWindows(t *testing.T) {
	h := newHarness(t)
	a := h.agent("alpha")
	h.hook(a, "UserPromptSubmit", "")
	sess := h.opt("alpha", "session_id")
	w1 := h.opt("alpha", "window_id")

	h.deck("", "sidebar", "toggle", "--session", sess, "--window", w1)
	sb := h.tmux("show-options", "-qv", "-t", "alpha", "@deck_sidebar_pane")
	if sb == "" {
		t.Fatal("no sidebar pane recorded")
	}
	h.eventually(func() bool {
		return strings.Contains(h.tmux("capture-pane", "-p", "-t", sb), "agents")
	}, "sidebar to draw")

	w2 := h.tmux("new-window", "-d", "-t", "alpha", "-P", "-F", "#{window_id}", "sleep 100000")
	h.tmux("select-window", "-t", w2)
	h.eventually(func() bool { return h.opt(sb, "window_id") == w2 }, "sidebar to follow to the new window")
	if got := h.opt(sb, "pane_at_left"); got != "1" {
		t.Errorf("sidebar not leftmost after follow")
	}
	// And back again: the follow must record where the sidebar went.
	h.tmux("select-window", "-t", w1)
	h.eventually(func() bool { return h.opt(sb, "window_id") == w1 }, "sidebar to follow back to the first window")
	h.tmux("select-window", "-t", w2)
	h.eventually(func() bool { return h.opt(sb, "window_id") == w2 }, "sidebar to follow a second time")

	h.deck("", "sidebar", "toggle", "--session", sess)
	h.eventually(func() bool { return h.tmux("show-options", "-qv", "-t", "alpha", "@deck_sidebar_pane") == "" }, "sidebar closed")
}

func TestPopupListsAndJumps(t *testing.T) {
	h := newHarness(t)
	first := h.opt("alpha", "pane_id")
	a := h.agent("alpha")
	h.hook(a, "UserPromptSubmit", "")
	h.hook(a, "PermissionRequest", "")
	h.tmux("select-pane", "-t", first)

	h.tmux("set-environment", "-g", "DECK_TMUX_SOCKET", h.socket)
	popup := h.tmux("new-window", "-d", "-P", "-F", "#{pane_id}", h.bin+" popup")
	var screen string
	for i := 0; i < 100; i++ {
		screen = h.tmux("capture-pane", "-p", "-t", popup)
		if strings.Contains(screen, "waiting") && strings.Contains(screen, "alpha:0") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(screen, "waiting") {
		t.Fatalf("popup does not list the waiting agent:\n%s", screen)
	}

	h.tmux("send-keys", "-t", popup, "Enter")
	h.eventually(func() bool { return h.opt("alpha", "pane_id") == a }, "jump to the agent")
}

// A sticky pane (dotfiles' sticky-pane.sh) and the sidebar both join the left
// edge when the window changes. The sticky hook sits in slot 0 and runs
// first; the sidebar's follow runs after it and must end up leftmost, with the
// sticky pane still in the window.
func TestSidebarCoexistsWithStickyPane(t *testing.T) {
	h := newHarness(t)
	a := h.agent("alpha")
	h.hook(a, "UserPromptSubmit", "")
	sess, w1 := h.opt("alpha", "session_id"), h.opt("alpha", "window_id")
	sticky := h.tmux("split-window", "-d", "-t", w1, "-P", "-F", "#{pane_id}", "sleep 100000")
	h.tmux("set-hook", "-g", "session-window-changed[0]",
		`run-shell "tmux -L `+h.socket+` join-pane -d -f -h -b -l 20 -s `+sticky+` -t #{window_id}"`)

	h.deck("", "sidebar", "toggle", "--session", sess, "--window", w1)
	sb := h.tmux("show-options", "-qv", "-t", "alpha", "@deck_sidebar_pane")
	w2 := h.tmux("new-window", "-d", "-t", "alpha", "-P", "-F", "#{window_id}", "sleep 100000")
	h.tmux("select-window", "-t", w2)

	h.eventually(func() bool {
		return h.opt(sb, "window_id") == w2 && h.opt(sticky, "window_id") == w2
	}, "sidebar and sticky pane to follow")
	h.eventually(func() bool { return h.opt(sb, "pane_at_left") == "1" }, "sidebar leftmost")
	if h.opt(sticky, "pane_at_left") == "1" {
		t.Error("sticky pane took the left edge from the sidebar")
	}
}

// Following the sidebar back and forth must not shift width between the
// other panes: each round trip used to move columns from the rightmost pane
// to the leftmost one until the right ones were a few columns wide.
func TestSidebarFollowKeepsPaneWidths(t *testing.T) {
	h := newHarness(t)
	a := h.agent("alpha")
	h.hook(a, "UserPromptSubmit", "")
	sess, w1 := h.opt("alpha", "session_id"), h.opt("alpha", "window_id")
	w2 := h.tmux("new-window", "-d", "-t", "alpha", "-P", "-F", "#{window_id}", "sleep 100000")
	h.tmux("split-window", "-d", "-h", "-t", w2, "sleep 100000")

	h.deck("", "sidebar", "toggle", "--session", sess, "--window", w1)
	sb := h.tmux("show-options", "-qv", "-t", "alpha", "@deck_sidebar_pane")
	widths := func(w string) string {
		return h.tmux("list-panes", "-t", w, "-F", "#{?#{==:#{pane_id},"+sb+"},sb,#{pane_width}}")
	}
	follow := func(w string) {
		h.tmux("select-window", "-t", w)
		h.eventually(func() bool {
			return h.tmux("show-options", "-qv", "-t", "alpha", "@deck_sidebar_window") == w
		}, "sidebar to follow to "+w)
	}

	follow(w2)
	want2 := widths(w2)
	follow(w1)
	want1 := widths(w1)
	// The sidebar's own resize handler can still move panes for a moment
	// after the follow lands, so wait for the layout to settle: real drift
	// never settles back to the widths it had.
	settled := func(w, want string) {
		t.Helper()
		got := ""
		h.eventually(func() bool { got = widths(w); return got == want }, w+" widths to settle")
		if got != want {
			t.Fatalf("%s widths drifted:\n got %q\nwant %q", w, got, want)
		}
	}
	for range 4 {
		follow(w2)
		settled(w2, want2)
		follow(w1)
		settled(w1, want1)
	}
}

// An agent that was idle when the deck was installed has fired no hook, yet
// must be listed.
func TestAgentsWithoutHooksAreDiscovered(t *testing.T) {
	h := newHarness(t)
	quiet := h.tmux("split-window", "-d", "-t", "alpha", "-P", "-F", "#{pane_id}",
		`printf '❯ \n  ⏸ manual mode on · ? for shortcuts\n'; exec `+h.fake)
	h.eventually(func() bool { return h.opt(quiet, "pane_current_command") == "2.1.999" }, "fake claude")
	if out := h.deck("", "list"); !strings.Contains(out, "idle") {
		t.Fatalf("quiet agent not listed:\n%s", out)
	}
	if got := h.opt(quiet, "@deck_state"); got != "idle" {
		t.Errorf("state = %q, want idle published for the tab and border", got)
	}
}

// When every other pane in its window closes, the sidebar must not keep the
// window open: it moves to the session's other window, or closes with the
// session's last window.
func TestSidebarLeavesAnEmptyWindow(t *testing.T) {
	h := newHarness(t)
	sess, w1 := h.opt("alpha", "session_id"), h.opt("alpha", "window_id")
	w2 := h.tmux("new-window", "-d", "-t", "alpha", "-P", "-F", "#{window_id}", "sleep 100000")
	h.tmux("select-window", "-t", w2)
	work := h.opt(w2, "pane_id")

	h.deck("", "sidebar", "toggle", "--session", sess, "--window", w2)
	sb := h.tmux("show-options", "-qv", "-t", "alpha", "@deck_sidebar_pane")
	h.eventually(func() bool { return strings.Contains(h.tmux("capture-pane", "-p", "-t", sb), "agents") }, "sidebar to draw")

	h.tmux("kill-pane", "-t", work)
	h.eventually(func() bool { return h.opt(sb, "window_id") == w1 }, "sidebar to move to the remaining window")
	h.eventually(func() bool { return !strings.Contains(h.tmux("list-windows", "-t", "alpha", "-F", "#{window_id}"), w2) }, "empty window to close")

	// Now the session's last window: closing its work pane closes everything.
	last := h.opt(w1, "pane_id")
	if last == sb {
		t.Fatal("expected the work pane to be active")
	}
	h.tmux("new-session", "-d", "-s", "keepalive", "sleep 100000") // keep the server up
	for _, p := range strings.Split(h.tmux("list-panes", "-t", w1, "-F", "#{pane_id}"), "\n") {
		if p != sb {
			h.tmux("kill-pane", "-t", p)
		}
	}
	h.eventually(func() bool {
		return !strings.Contains(h.tmux("list-sessions", "-F", "#{session_name}"), "alpha")
	}, "the sidebar to close with the session's last window")
}

// Claude runs the deck's statusLine; its usage and plan limits reach the views.
func TestStatusLineFeedsTheViews(t *testing.T) {
	h := newHarness(t)
	a := h.agent("alpha")
	h.hook(a, "UserPromptSubmit", "")
	sid := "sess-" + strings.TrimPrefix(a, "%")
	line := h.deck(`{"session_id":"`+sid+`","model":{"display_name":"Opus 5.5"},
		"cost":{"total_cost_usd":1.25},
		"context_window":{"total_input_tokens":120000,"total_output_tokens":8000,"used_percentage":42},
		"rate_limits":{"five_hour":{"used_percentage":31,"resets_at":4102444800},"seven_day":{"used_percentage":12,"resets_at":4102444800}}}`,
		"statusline")
	if strings.TrimSpace(line) != "Opus 5.5 · ctx 42% · 5h 31% · 7d 12%" {
		t.Errorf("status line = %q", line)
	}
	if out := h.deck("", "list"); !strings.Contains(out, "plan 5h 31%   7d 12%") {
		t.Errorf("list lacks the plan:\n%s", out)
	}
	h.tmux("set-environment", "-g", "DECK_TMUX_SOCKET", h.socket)
	popup := h.tmux("new-window", "-d", "-P", "-F", "#{pane_id}", h.bin+" popup")
	var screen string
	for i := 0; i < 100; i++ {
		screen = h.tmux("capture-pane", "-p", "-t", popup)
		if strings.Contains(screen, "$1.25") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, want := range []string{"plan", "31%", "42%", "128.0k", "$1.25"} {
		if !strings.Contains(screen, want) {
			t.Errorf("popup lacks %q:\n%s", want, screen)
		}
	}
	if out := h.deck("not json", "statusline"); strings.TrimSpace(out) != "" {
		t.Errorf("garbage input printed %q", out)
	}
}

// A sidebar squeezed by layout changes restores its width.
func TestSidebarRestoresASqueezedWidth(t *testing.T) {
	h := newHarness(t)
	sess, w1 := h.opt("alpha", "session_id"), h.opt("alpha", "window_id")
	h.deck("", "sidebar", "toggle", "--session", sess, "--window", w1)
	sb := h.tmux("show-options", "-qv", "-t", "alpha", "@deck_sidebar_pane")
	h.eventually(func() bool { return h.opt(sb, "pane_width") == "34" }, "sidebar at 34 columns")
	h.tmux("resize-pane", "-t", sb, "-x", "8")
	h.eventually(func() bool { return h.opt(sb, "pane_width") == "34" }, "sidebar to restore its width")
	h.tmux("resize-pane", "-t", sb, "-x", "40")
	// A negative check: give the sidebar several redraws to (wrongly) react.
	time.Sleep(1200 * time.Millisecond)
	if got := h.opt(sb, "pane_width"); got != "40" {
		t.Errorf("a deliberate resize to 40 was undone: %s", got)
	}
}

// Opening a sidebar must not take focus from the pane the user works in:
// losing focus runs the screen check on that pane.
func TestSidebarDoesNotStealFocus(t *testing.T) {
	h := newHarness(t)
	sess, w1 := h.opt("alpha", "session_id"), h.opt("alpha", "window_id")
	before := h.opt(w1, "pane_id")
	h.deck("", "sidebar", "toggle", "--session", sess, "--window", w1)
	sb := h.tmux("show-options", "-qv", "-t", "alpha", "@deck_sidebar_pane")
	h.eventually(func() bool { return h.opt(sb, "pane_title") == "agents" }, "sidebar to title itself")
	if got := h.opt(w1, "pane_id"); got != before {
		t.Errorf("active pane moved from %s to %s", before, got)
	}
}

// pinWait outlasts the pin interval, so a second pin would have happened.
const pinWait = 2500 * time.Millisecond

// Swapping, rotating or re-laying out panes moves the sidebar like any pane;
// it must come back as the full-height left column.
func TestSidebarSurvivesSwapsAndLayouts(t *testing.T) {
	h := newHarness(t)
	sess, w1 := h.opt("alpha", "session_id"), h.opt("alpha", "window_id")
	h.tmux("split-window", "-d", "-t", w1, "sleep 100000")
	h.tmux("split-window", "-d", "-v", "-t", w1, "sleep 100000")
	h.deck("", "sidebar", "toggle", "--session", sess, "--window", w1)
	sb := h.tmux("show-options", "-qv", "-t", "alpha", "@deck_sidebar_pane")
	pinned := func() bool {
		return h.opt(sb, "window_id") == w1 && h.opt(sb, "pane_at_left") == "1" &&
			h.opt(sb, "pane_at_top") == "1" && h.opt(sb, "pane_at_bottom") == "1"
	}
	h.eventually(pinned, "sidebar pinned at start")
	other := ""
	for _, p := range strings.Split(h.tmux("list-panes", "-t", w1, "-F", "#{pane_id}"), "\n") {
		if p != sb {
			other = p
		}
	}
	steps := [][]string{
		{"swap-pane", "-s", sb, "-t", other},
		{"rotate-window", "-t", w1},
		{"select-layout", "-t", w1, "tiled"},
		{"select-layout", "-t", w1, "even-vertical"},
	}
	for _, step := range steps {
		before := h.pins()
		h.tmux(step...)
		h.eventually(pinned, "sidebar pinned after "+step[0])
		// It must settle: one pin per change at most, never a loop.
		time.Sleep(pinWait)
		if n := h.pins() - before; n > 1 {
			b, _ := os.ReadFile(filepath.Join(filepath.Dir(h.state), "views.log"))
			t.Fatalf("%s caused %d pins; the sidebar is fighting the layout\n%s", step[0], n, b)
		}
	}
	if got := len(strings.Split(h.tmux("list-panes", "-t", w1, "-F", "#{pane_id}"), "\n")); got != 4 {
		t.Errorf("window has %d panes, want the 3 work panes and the sidebar", got)
	}
}

func TestTmuxInitTurnsOnFocusEvents(t *testing.T) {
	h := newHarness(t)
	if got := h.tmux("show-options", "-gv", "focus-events"); got != "on" {
		t.Errorf("focus-events = %q, want on (the harness starts with it off)", got)
	}
}

func TestTickNeverShowsIdlesGlyph(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 3; i++ {
		switch got := h.deck("", "tick"); got {
		case "●", "◉", "◎":
		default:
			t.Fatalf("tick printed %q", got)
		}
	}
}

// Install and uninstall against a real file: the round trip restores it, and
// every command carries the marker uninstall looks for.
func TestInstallRoundTrip(t *testing.T) {
	h := newHarness(t)
	settings := filepath.Join(t.TempDir(), "settings.json")
	original := "{\n  \"model\": \"opus\"\n}\n"
	if err := os.WriteFile(settings, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := h.deck("", "install", "--claude", "--settings", settings); !strings.Contains(out, "Preview only") {
		t.Fatalf("install without --apply did not preview:\n%s", out)
	}
	if b, _ := os.ReadFile(settings); string(b) != original {
		t.Fatal("preview changed the file")
	}
	h.deck("", "install", "--claude", "--apply", "--settings", settings)
	b, _ := os.ReadFile(settings)
	for _, want := range []string{`"statusLine"`, `"PreToolUse"`, "# tmux-agent-deck", " statusline;"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("installed file lacks %q", want)
		}
	}
	h.deck("", "uninstall", "--claude", "--apply", "--settings", settings)
	if b, _ := os.ReadFile(settings); string(b) != original {
		t.Errorf("uninstall did not restore the file:\n%s", b)
	}
}

func TestDoctorReportsAHealthySetup(t *testing.T) {
	h := newHarness(t)
	claude := t.TempDir()
	settings := filepath.Join(claude, "settings.json")
	h.deck("", "install", "--claude", "--apply", "--settings", settings)
	cmd := exec.Command(h.bin, "doctor")
	cmd.Env = append(h.env(""), "CLAUDE_CONFIG_DIR="+claude)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("doctor failed: %v\n%s", err, out)
	}
	for _, want := range []string{"✔ tmux 3.3 or newer", "✔ focus-events on", "✔ Claude hooks", "✔ statusLine"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("doctor output lacks %q:\n%s", want, out)
		}
	}
}

// tmux ends a killed pane with SIGHUP; the sidebar must still clean up, or
// the follow hook keeps trying to move a pane that no longer exists.
func TestKilledSidebarCleansUp(t *testing.T) {
	h := newHarness(t)
	sess, w1 := h.opt("alpha", "session_id"), h.opt("alpha", "window_id")
	h.deck("", "sidebar", "toggle", "--session", sess, "--window", w1)
	sb := h.tmux("show-options", "-qv", "-t", "alpha", "@deck_sidebar_pane")
	h.eventually(func() bool { return strings.Contains(h.tmux("capture-pane", "-p", "-t", sb), "agents") }, "sidebar to draw")
	h.tmux("kill-pane", "-t", sb)
	h.eventually(func() bool {
		return h.tmux("show-options", "-qv", "-t", "alpha", "@deck_sidebar_pane") == "" &&
			h.tmux("show-options", "-qv", "-t", "alpha", "@deck_sidebar_window") == ""
	}, "the killed sidebar to clear its session options")
}

// Leaving a pane whose turn ended silently repairs it through the tmux hook,
// not only through a direct `deck reconcile`.
func TestFocusOutRepairsThroughTheHook(t *testing.T) {
	h := newHarness(t)
	first := h.opt("alpha", "pane_id")
	a := h.tmux("split-window", "-t", "alpha", "-P", "-F", "#{pane_id}",
		`printf '  ⎿  Interrupted · What should Claude do instead?\n──────────────────\n❯ \n──────────────────\n  ⏸ manual mode on · ? for shortcuts\n'; exec `+h.fake)
	h.eventually(func() bool { return h.opt(a, "pane_current_command") == "2.1.999" }, "fake claude")
	h.hook(a, "UserPromptSubmit", "")
	h.hook(a, "PermissionRequest", "")
	h.attach("alpha")
	h.tmux("select-pane", "-t", a)
	h.tmux("select-pane", "-t", first) // a loses focus
	h.eventually(func() bool { return h.opt(a, "@deck_state") == "idle" }, "the focus-out hook to repair the silent ending")
}

// Two layout changes within the pin interval: the second pin is deferred,
// not dropped, so the sidebar still ends up as the left column.
func TestPinTooSoonIsDeferredNotDropped(t *testing.T) {
	h := newHarness(t)
	sess, w1 := h.opt("alpha", "session_id"), h.opt("alpha", "window_id")
	h.tmux("split-window", "-d", "-t", w1, "sleep 100000")
	h.deck("", "sidebar", "toggle", "--session", sess, "--window", w1)
	sb := h.tmux("show-options", "-qv", "-t", "alpha", "@deck_sidebar_pane")
	pinned := func() bool {
		return h.opt(sb, "pane_at_left") == "1" && h.opt(sb, "pane_at_top") == "1" && h.opt(sb, "pane_at_bottom") == "1"
	}
	h.eventually(pinned, "sidebar pinned at start")
	// A pin just happened, as far as the rate limit knows.
	h.tmux("set-option", "-p", "-t", sb, "@deck_pinned_at", fmt.Sprint(time.Now().UnixMilli()))
	h.tmux("select-layout", "-t", w1, "even-vertical")
	if pinned() {
		t.Fatal("even-vertical left the sidebar in place; the test proves nothing")
	}
	h.eventually(pinned, "the deferred pin to put the sidebar back")
}
