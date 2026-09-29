package integration

import (
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
	time.Sleep(300 * time.Millisecond)
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
