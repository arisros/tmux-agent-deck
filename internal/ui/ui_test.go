package ui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/arisros/tmux-agent-deck/internal/tmux"
	"github.com/arisros/tmux-agent-deck/internal/usage"
)

var now = time.Unix(10_000, 0)

func pane(id, session, win, state, cmd, title string, since int64) tmux.Pane {
	return tmux.Pane{ID: id, Session: session, SessionID: "$" + session, Window: win, Index: "0",
		State: state, Command: cmd, Title: title, Path: "/work/" + id, Since: since}
}

func TestAgentsFilterAndOrder(t *testing.T) {
	panes := []tmux.Pane{
		pane("%1", "b", "1", "idle", "2.1.284", "✳ quiet one", 9_000),
		pane("%2", "a", "2", "running", "2.1.284", "✳ busy", 9_990),
		pane("%3", "a", "1", "waiting", "2.1.284", "✳ needs you", 9_950),
		pane("%4", "a", "1", "done", "2.1.284", "✳ finished", 9_900),
		pane("%5", "a", "3", "running", "zsh", "✳ stale title", 9_000), // claude exited
		pane("%6", "a", "4", "", "2.1.284", "✳ unknown", 0),            // no deck state yet
		{ID: "%7", State: "running", Command: "2.1.284", Sidebar: "1"},
	}
	rows := Agents(panes, now)
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	if got := strings.Join(ids, ","); got != "%3,%4,%2,%1" {
		t.Errorf("order = %s, want waiting, done, running, idle", got)
	}
	if rows[0].Name != "needs you" || rows[0].Age != 50*time.Second {
		t.Errorf("row = %+v", rows[0])
	}
}

func TestNameFallsBackToFolder(t *testing.T) {
	p := pane("%1", "a", "1", "idle", "2.1.284", "MacBook-Pro.local", 0)
	if got := Agents([]tmux.Pane{p}, now)[0].Name; got != "%1" {
		t.Errorf("name = %q, want the folder", got)
	}
}

func TestFit(t *testing.T) {
	cases := []struct {
		in   string
		w    int
		want string
	}{
		{"abc", 5, "abc  "},
		{"abcdef", 4, "abc…"},
		{"日本語", 4, "日… "}, // a wide rune cannot be split, so one cell of padding
		{"\x1b[31mred\x1b[0m", 5, "\x1b[31mred\x1b[0m  "},
		{"x", 0, ""},
	}
	for _, c := range cases {
		if got := Fit(c.in, c.w); got != c.want {
			t.Errorf("Fit(%q, %d) = %q, want %q", c.in, c.w, got, c.want)
		}
		if c.w > 0 && Width(Fit(c.in, c.w)) != c.w {
			t.Errorf("Fit(%q, %d) is %d cells", c.in, c.w, Width(Fit(c.in, c.w)))
		}
	}
}

func TestAge(t *testing.T) {
	for d, want := range map[time.Duration]string{
		42 * time.Second: "42s", 7 * time.Minute: "7m", 3 * time.Hour: "3h", 50 * time.Hour: "2d",
	} {
		if got := Age(d); got != want {
			t.Errorf("Age(%s) = %s, want %s", d, got, want)
		}
	}
}

func TestDecode(t *testing.T) {
	cases := map[string]Key{
		"\x1b": {Name: "esc"}, "\x1b[A": {Name: "up"}, "\x1b[B": {Name: "down"},
		"\r": {Name: "enter"}, "\x03": {Name: "ctrl-c"}, "\x7f": {Name: "backspace"}, "j": {Rune: 'j'},
	}
	for in, want := range cases {
		got := decode([]byte(in))
		if len(got) != 1 || got[0] != want {
			t.Errorf("decode(%q) = %v, want %v", in, got, want)
		}
	}
	if got := decode([]byte("\x1b[1;5C")); len(got) != 0 {
		t.Errorf("unknown escape decoded to %v", got)
	}
}

func TestListNavigationAndFilter(t *testing.T) {
	l := &List{All: Agents([]tmux.Pane{
		pane("%1", "a", "1", "waiting", "2.1.284", "✳ alpha task", 0),
		pane("%2", "a", "2", "running", "2.1.284", "✳ beta task", 0),
		pane("%3", "b", "1", "idle", "2.1.284", "✳ gamma", 0),
	}, now)}
	l.Handle(Key{Rune: 'j'})
	l.Handle(Key{Rune: 'j'})
	l.Handle(Key{Rune: 'j'})
	if r, _ := l.Selected(); r.ID != "%3" {
		t.Errorf("cursor past the end: %s", r.ID)
	}
	l.Handle(Key{Rune: '/'})
	for _, r := range "beta" {
		l.Handle(Key{Rune: r})
	}
	if v := l.Visible(); len(v) != 1 || v[0].ID != "%2" {
		t.Errorf("filter kept %v", v)
	}
	if l.Handle(Key{Name: "enter"}) != Jump {
		t.Error("enter in the filter should jump to the match")
	}
	if l.Handle(Key{Rune: 'q'}) != Quit {
		t.Error("q should quit")
	}
}

func TestViewsFitTheScreen(t *testing.T) {
	var panes []tmux.Pane
	for i := 0; i < 40; i++ {
		panes = append(panes, pane("%"+string(rune('a'+i%26)), "s", "1", "running", "2.1.284", "✳ a rather long session title that will not fit", 0))
	}
	l := &List{All: Agents(panes, now)}
	for _, lines := range [][]string{Popup(l, 100, 20), Sidebar(l, l.All[:3], "s", true, 34, 20)} {
		if len(lines) != 20 {
			t.Errorf("rendered %d lines for a 20-line screen", len(lines))
		}
		for _, line := range lines {
			if w := Width(line); w > 100 {
				t.Errorf("line of %d cells: %q", w, line)
			}
		}
	}
}

func TestKillNeedsConfirmation(t *testing.T) {
	l := &List{All: Agents([]tmux.Pane{pane("%1", "a", "1", "idle", "2.1.284", "✳ old one", 0)}, now)}
	if l.Handle(Key{Rune: 'x'}) != Stay || !l.Confirming {
		t.Fatal("x should ask first")
	}
	if !strings.Contains(Popup(l, 80, 10)[9], "kill old one? y/n") {
		t.Errorf("footer does not ask: %q", Popup(l, 80, 10)[9])
	}
	if l.Handle(Key{Rune: 'n'}) != Stay || l.Confirming {
		t.Error("n should cancel")
	}
	l.Handle(Key{Rune: 'x'})
	if l.Handle(Key{Rune: 'y'}) != Kill {
		t.Error("x then y should kill")
	}
}

func TestCurrentPaneIsMarked(t *testing.T) {
	l := &List{All: Agents([]tmux.Pane{
		pane("%1", "a", "1", "waiting", "2.1.284", "✳ first", 0),
		pane("%2", "a", "2", "idle", "2.1.284", "✳ here", 0),
	}, now), Current: "%2"}
	popup := Popup(l, 90, 8)
	if !strings.HasPrefix(popup[3], hereBar) || !strings.Contains(popup[3], hereBg) {
		t.Errorf("current row not marked: %q", popup[3])
	}
	if strings.HasPrefix(popup[2], hereBar) {
		t.Errorf("cursor row marked as current: %q", popup[2])
	}
	side := Sidebar(l, nil, "a", false, 34, 12)
	if !strings.HasPrefix(side[4], hereBar) || !strings.HasPrefix(side[5], hereBar) {
		t.Errorf("sidebar current rows not marked:\n%q\n%q", side[4], side[5])
	}
	for _, line := range append(popup, side...) {
		if Width(line) > 90 {
			t.Errorf("line of %d cells", Width(line))
		}
	}
}

func TestRunningPulsesOthersDoNot(t *testing.T) {
	defer func() { Pulse = 0 }()
	seen := map[string]bool{}
	for Pulse = 0; Pulse < 4; Pulse++ {
		seen[StyleOf("running").Glyph] = true
		if StyleOf("waiting") != Styles["waiting"] {
			t.Error("waiting must not animate")
		}
	}
	if len(seen) < 3 {
		t.Errorf("running pulse has %d distinct frames", len(seen))
	}
	if seen[Styles["idle"].Glyph] {
		t.Error("a running frame uses idle's glyph")
	}
	idle := Agents([]tmux.Pane{pane("%1", "a", "1", "idle", "2.1.284", "✳ x", 0)}, now)
	busy := Agents([]tmux.Pane{pane("%1", "a", "1", "running", "2.1.284", "✳ x", 0)}, now)
	if Animated(idle) || !Animated(busy) {
		t.Error("animation must run only while an agent is running")
	}
}

func TestSidebarScrollsWithCursor(t *testing.T) {
	var panes []tmux.Pane
	for i := 0; i < 20; i++ {
		panes = append(panes, pane(fmt.Sprintf("%%%d", i), "s", "1", "idle", "2.1.284", fmt.Sprintf("✳ agent %02d", i), 0))
	}
	l := &List{All: Agents(panes, now)}
	const h = 16 // room for (16-5-2)/2 = 4 agents plus the ↑/↓ lines
	view := strings.Join(Sidebar(l, nil, "s", true, 34, h), "\n")
	if !strings.Contains(view, "agent 00") || strings.Contains(view, "agent 05") || !strings.Contains(view, "↓ 16 more") {
		t.Fatalf("top of list:\n%s", view)
	}
	for i := 0; i < 10; i++ {
		l.Handle(Key{Rune: 'j'})
	}
	view = strings.Join(Sidebar(l, nil, "s", true, 34, h), "\n")
	if !strings.Contains(view, "agent 10") || !strings.Contains(view, "↑ 7 more") || strings.Contains(view, "agent 00") {
		t.Fatalf("after scrolling to 10:\n%s", view)
	}
	for _, line := range Sidebar(l, nil, "s", true, 34, h) {
		if Width(line) > 34 {
			t.Errorf("line of %d cells", Width(line))
		}
	}
	if n := len(Sidebar(l, nil, "s", true, 34, h)); n != h {
		t.Errorf("%d lines for a %d-line pane", n, h)
	}
}

func TestMouseWheelScrolls(t *testing.T) {
	if got := decode([]byte("\x1b[<65;10;5M\x1b[<65;10;5M")); len(got) != 2 || got[0].Name != "wheeldown" {
		t.Errorf("wheel down decoded to %v", got)
	}
	if got := decode([]byte("\x1b[<64;1;1M")); len(got) != 1 || got[0].Name != "wheelup" {
		t.Errorf("wheel up decoded to %v", got)
	}
	if got := decode([]byte("\x1b[<0;3;4M")); len(got) != 0 {
		t.Errorf("click decoded to %v", got)
	}
}

// The wheel reaches an unfocused sidebar; its view must stay where the wheel
// left it instead of snapping back to the current pane.
func TestWheelScrollsUnfocusedSidebar(t *testing.T) {
	var panes []tmux.Pane
	for i := 0; i < 20; i++ {
		panes = append(panes, pane(fmt.Sprintf("%%%d", i), "s", "1", "idle", "2.1.284", fmt.Sprintf("✳ agent %02d", i), 0))
	}
	l := &List{All: Agents(panes, now), Current: "%0"}
	Sidebar(l, nil, "s", false, 34, 16)
	for i := 0; i < 8; i++ {
		l.Handle(Key{Name: "wheeldown"})
	}
	view := strings.Join(Sidebar(l, nil, "s", false, 34, 16), "\n")
	if strings.Contains(view, "agent 00") || !strings.Contains(view, "agent 08") {
		t.Fatalf("wheel did not scroll the unfocused sidebar:\n%s", view)
	}
	l.Current = "%1" // switching panes brings "you are here" back into view
	view = strings.Join(Sidebar(l, nil, "s", false, 34, 16), "\n")
	if !strings.Contains(view, "agent 01") {
		t.Fatalf("pane switch did not bring the current pane back:\n%s", view)
	}
}

func TestUsageColumnsFit(t *testing.T) {
	ctx := 42.0
	rows := Agents([]tmux.Pane{
		pane("%1", "a", "1", "running", "2.1.284", "✳ with usage", 0),
		pane("%2", "a", "2", "idle", "2.1.284", "✳ without", 0),
	}, now)
	rows[0].Usage = &usage.Session{CostUSD: 12.5, InputTokens: 1_200_000, OutputTokens: 30_000, ContextUsed: &ctx}
	l := &List{All: rows, Now: now, Limits: &usage.Limits{
		FiveHour:      &usage.Window{UsedPercentage: 85, ResetsAt: usage.ResetTime{Time: now.Add(2 * time.Hour)}},
		SevenDay:      &usage.Window{UsedPercentage: 12},
		UpdatedAtUnix: now.Unix(),
	}}
	for _, w := range []int{80, 120, 200} {
		popup := Popup(l, w, 12)
		for _, line := range popup {
			if Width(line) > w {
				t.Errorf("width %d: line of %d cells: %q", w, Width(line), line)
			}
		}
		joined := strings.Join(popup, "\n")
		for _, want := range []string{"5h", "85%", "\x1b[31m", "42%", "1.2M", "$12.50"} {
			if !strings.Contains(joined, want) {
				t.Errorf("width %d: popup lacks %q", w, want)
			}
		}
	}
	side := strings.Join(Sidebar(l, nil, "a", false, 34, 14), "\n")
	plain := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(side, "")
	if !strings.Contains(plain, "5h ▰▰▰▰▰▰▰▱  85% ↻") || !strings.Contains(plain, "1.0 · ▰▰▱▱▱ 42%") ||
		strings.Contains(plain, "ctx") || !strings.Contains(plain, "other sessions: none") {
		t.Errorf("sidebar lacks plan or context:\n%s", side)
	}
}

func TestAttachByPaneWhenNoSession(t *testing.T) {
	hooked := pane("%2", "a", "2", "running", "2.1.284", "✳ hooked", 0)
	hooked.SID = "s2"
	rows := Attach(Agents([]tmux.Pane{pane("%1", "a", "1", "idle", "2.1.284", "✳ discovered", 0), hooked}, now),
		map[string]usage.Session{
			"old": {Pane: "%1", CostUSD: 1, UpdatedAtUnix: 10},
			"new": {Pane: "%1", CostUSD: 2, UpdatedAtUnix: 20},
			"s2":  {CostUSD: 3},
		})
	byID := map[string]Row{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	if u := byID["%1"].Usage; u == nil || u.CostUSD != 2 {
		t.Errorf("discovered row usage = %+v, want the newest report from its pane", u)
	}
	if u := byID["%2"].Usage; u == nil || u.CostUSD != 3 {
		t.Errorf("hooked row usage = %+v", u)
	}
}

func TestDecodeMixedRead(t *testing.T) {
	got := decode([]byte("j\x1b[Ak\x1b[<65;1;1Mx\x1b[1;5C/"))
	want := []Key{{Rune: 'j'}, {Name: "up"}, {Rune: 'k'}, {Name: "wheeldown"}, {Rune: 'x'}, {Rune: '/'}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("key %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestWaitingRowSaysWhy(t *testing.T) {
	ask := pane("%1", "a", "1", "waiting", "2.1.284", "✳ deploy", 0)
	ask.Reason = "permission Bash"
	question := pane("%2", "a", "2", "waiting", "2.1.284", "✳ schema", 0)
	question.Reason = "question"
	unknown := pane("%3", "a", "3", "waiting", "2.1.284", "✳ discovered", 0)
	l := &List{All: Agents([]tmux.Pane{ask, question, unknown}, now)}

	popup := Popup(l, 110, 10)
	for i, want := range []string{"permission", "question", "waiting"} {
		if line := popup[2+i]; !strings.Contains(line, want) {
			t.Errorf("popup row %d lacks %q: %q", i, want, line)
		}
	}
	if !strings.Contains(popup[2], "Bash · ") || strings.Contains(popup[3], " · ") {
		t.Errorf("the tool belongs on the permission row only:\n%q\n%q", popup[2], popup[3])
	}
	for _, line := range popup {
		if Width(line) > 110 {
			t.Errorf("popup line is %d cells wide: %q", Width(line), line)
		}
	}

	side := strings.Join(Sidebar(l, nil, "a", false, 34, 14), "\n")
	if !strings.Contains(side, "1.0 · permission Bash · ") || !strings.Contains(side, "2.0 · question · ") {
		t.Errorf("sidebar does not say why:\n%s", side)
	}

	l.Filter = "question"
	if v := l.Visible(); len(v) != 1 || v[0].ID != "%2" {
		t.Errorf("filtering by reason kept %v", v)
	}
}

func typeKeys(l *List, s string) Outcome {
	var o Outcome
	for _, r := range s {
		o = l.Handle(Key{Rune: r})
	}
	return o
}

func TestPopupShowsTheSelectedAgentsScreen(t *testing.T) {
	ask := pane("%1", "a", "1", "waiting", "2.1.284", "✳ deploy", 0)
	ask.Reason = "permission Bash"
	l := &List{All: Agents([]tmux.Pane{ask, pane("%2", "a", "2", "running", "2.1.284", "✳ other", 0)}, now)}
	l.SetPreview("%1", "Bash command\n  rm -rf build\nDo you want to proceed?\n❯ 1. Yes\n  2. No\n\n\n")

	lines := Popup(l, 100, 30)
	if len(lines) != 30 {
		t.Fatalf("rendered %d lines for a 30-line screen", len(lines))
	}
	view := strings.Join(lines, "\n")
	for _, want := range []string{"── deploy · permission Bash ──", "rm -rf build", "❯ 1. Yes"} {
		if !strings.Contains(view, want) {
			t.Errorf("popup lacks %q:\n%s", want, view)
		}
	}
	for _, line := range lines {
		if Width(line) > 100 {
			t.Errorf("line is %d cells wide: %q", Width(line), line)
		}
	}
	// Moving the cursor away hides a screen that is no longer the selected one.
	l.Handle(Key{Rune: 'j'})
	if view := strings.Join(Popup(l, 100, 30), "\n"); strings.Contains(view, "rm -rf build") {
		t.Errorf("the previous agent's screen is still shown:\n%s", view)
	}
	// A short popup keeps the list and drops the screen.
	if view := strings.Join(Popup(l, 100, 10), "\n"); strings.Contains(view, "──  ") || len(Popup(l, 100, 10)) != 10 {
		t.Errorf("short popup:\n%s", view)
	}
}

func TestAnswerOnlyAWaitingAgentWhoseScreenIsShown(t *testing.T) {
	ask := pane("%1", "a", "1", "waiting", "2.1.284", "✳ deploy", 0)
	busy := pane("%2", "a", "2", "running", "2.1.284", "✳ other", 0)
	l := &List{All: Agents([]tmux.Pane{ask, busy}, now)}
	if o := l.Handle(Key{Rune: '1'}); o != Stay {
		t.Errorf("answered a dialog nobody can read: %v", o)
	}
	l.SetPreview("%1", "Do you want to proceed?")
	if o := l.Handle(Key{Rune: '2'}); o != Answer || l.Reply != "2" {
		t.Errorf("got %v %q, want Answer 2", o, l.Reply)
	}
	l.Handle(Key{Rune: 'j'})
	l.SetPreview("%2", "working")
	if o := l.Handle(Key{Rune: '1'}); o != Stay {
		t.Errorf("answered an agent that is not waiting: %v", o)
	}
}

func TestComposeSendsOnEnterAndEscDrops(t *testing.T) {
	l := &List{All: Agents([]tmux.Pane{pane("%1", "a", "1", "idle", "2.1.284", "✳ api", 0)}, now)}
	l.Handle(Key{Rune: 'p'})
	// Keys that are commands elsewhere are text here.
	typeKeys(l, "quit x 1/")
	if foot := Popup(l, 80, 10)[9]; !strings.Contains(foot, "to api > ") || !strings.Contains(foot, "quit x 1/▏") {
		t.Errorf("footer does not show the draft: %q", Popup(l, 80, 10)[9])
	}
	l.Handle(Key{Name: "backspace"})
	if o := l.Handle(Key{Name: "enter"}); o != Send || l.Reply != "quit x 1" || l.Composing || l.Draft != "" {
		t.Errorf("got %v reply %q composing %v", o, l.Reply, l.Composing)
	}
	l.Handle(Key{Rune: 'p'})
	typeKeys(l, "never mind")
	if o := l.Handle(Key{Name: "esc"}); o != Stay || l.Composing || l.Draft != "" {
		t.Errorf("esc did not drop the draft: %v %q", o, l.Draft)
	}
	l.Handle(Key{Rune: 'p'})
	if o := l.Handle(Key{Name: "enter"}); o != Stay {
		t.Errorf("an empty draft was sent: %v", o)
	}
}

func TestInterruptNeedsConfirmationAndSeenNeedsDone(t *testing.T) {
	l := &List{All: Agents([]tmux.Pane{
		pane("%1", "a", "1", "done", "2.1.284", "✳ finished", 0),
		pane("%2", "a", "2", "running", "2.1.284", "✳ busy", 0),
		pane("%3", "a", "3", "idle", "2.1.284", "✳ quiet", 0),
	}, now)}
	if o := l.Handle(Key{Rune: 'i'}); o != Stay || l.Confirming {
		t.Error("a finished agent has nothing to interrupt")
	}
	if o := l.Handle(Key{Rune: 's'}); o != Seen {
		t.Errorf("s on a done agent = %v, want Seen", o)
	}
	l.Handle(Key{Rune: 'j'})
	if o := l.Handle(Key{Rune: 's'}); o != Stay {
		t.Errorf("s on a running agent = %v", o)
	}
	l.Handle(Key{Rune: 'i'})
	if !strings.Contains(Popup(l, 80, 10)[9], "interrupt busy? y/n") {
		t.Errorf("footer does not ask: %q", Popup(l, 80, 10)[9])
	}
	if o := l.Handle(Key{Rune: 'n'}); o != Stay {
		t.Errorf("n interrupted: %v", o)
	}
	l.Handle(Key{Rune: 'i'})
	if o := l.Handle(Key{Rune: 'y'}); o != Interrupt {
		t.Errorf("y = %v, want Interrupt", o)
	}
}

func TestLongestWaitComesFirst(t *testing.T) {
	rows := Agents([]tmux.Pane{
		pane("%1", "a", "1", "waiting", "2.1.284", "✳ asked just now", 9_990),
		pane("%2", "z", "9", "waiting", "2.1.284", "✳ asked long ago", 9_000),
		pane("%3", "a", "2", "done", "2.1.284", "✳ finished recently", 9_950),
		pane("%4", "b", "1", "done", "2.1.284", "✳ finished first", 9_100),
		// Working agents keep their place: session, then window.
		pane("%5", "b", "1", "running", "2.1.284", "✳ started first", 9_000),
		pane("%6", "a", "3", "running", "2.1.284", "✳ started later", 9_900),
	}, now)
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	if got := strings.Join(ids, ","); got != "%2,%1,%4,%3,%6,%5" {
		t.Errorf("order = %s", got)
	}
}

func TestFilterByFieldAndAttention(t *testing.T) {
	ask := pane("%1", "work", "1", "waiting", "2.1.284", "✳ deploy api", 0)
	ask.Reason = "permission Bash"
	codex := pane("%2", "work", "2", "running", "node", "✳ running tests", 0)
	codex.Agent, codex.Cmd = "codex", "node"
	done := pane("%3", "play", "1", "done", "2.1.284", "✳ state machine notes", 0)
	rows := Branches(Agents([]tmux.Pane{ask, codex, done}, now), func(dir string) string {
		return map[string]string{"/work/%1": "main", "/work/%2": "feat/oauth"}[dir]
	})
	cases := map[string]string{
		"":                              "%1,%3,%2",
		"state:waiting":                 "%1",
		"state:done":                    "%3",
		"state":                         "%3", // a bare word is searched everywhere, here in a name
		"reason:permission":             "%1",
		"reason:bash":                   "%1",
		"agent:codex":                   "%2",
		"agent:claude":                  "%1,%3",
		"session:work":                  "%1,%2",
		"branch:feat":                   "%2",
		"branch:main state:waiting":     "%1",
		"session:work running":          "%2",
		"SESSION:WORK State:Running":    "%2",
		"work:1.0":                      "%1", // a tmux target, not a field
		"session:work state:done":       "",
		"path:/work/%3 pane:%3 name:st": "%3",
	}
	for filter, want := range cases {
		var ids []string
		for _, r := range Filter(rows, filter, false) {
			ids = append(ids, r.ID)
		}
		if got := strings.Join(ids, ","); got != want {
			t.Errorf("filter %q kept %q, want %q", filter, got, want)
		}
	}

	l := &List{All: rows, Cursor: 2}
	l.Handle(Key{Rune: 'a'})
	if v := l.Visible(); len(v) != 2 || v[0].ID != "%1" || v[1].ID != "%3" || l.Cursor != 0 {
		t.Errorf("attention kept %v, cursor %d", v, l.Cursor)
	}
	if foot := Popup(l, 160, 12)[11]; !strings.Contains(foot, "needing you only") {
		t.Errorf("footer does not say the list is narrowed: %q", foot)
	}
	l.Handle(Key{Rune: 'a'})
	if len(l.Visible()) != 3 {
		t.Error("a second press did not bring every agent back")
	}

	popup := strings.Join(Popup(l, 160, 12), "\n")
	for _, want := range []string{"%1@main", "%2@feat/oauth"} {
		if !strings.Contains(popup, want) {
			t.Errorf("popup lacks %q:\n%s", want, popup)
		}
	}
	if side := strings.Join(Sidebar(l, nil, "work", false, 40, 16), "\n"); !strings.Contains(side, " · feat/oauth") {
		t.Errorf("sidebar lacks the branch:\n%s", side)
	}
}

func TestRenameCopyAndTimeline(t *testing.T) {
	p := pane("%1", "a", "1", "waiting", "2.1.284", "✳ a long generated title", 0)
	p.Reason, p.Started = "question", now.Unix()-2*3600
	l := &List{All: Agents([]tmux.Pane{p}, now), Now: now}

	if o := l.Handle(Key{Rune: 'y'}); o != Copy {
		t.Errorf("y = %v, want Copy", o)
	}
	l.Note = "copied 12 lines"
	if foot := Popup(l, 90, 10)[9]; !strings.Contains(foot, "copied 12 lines") {
		t.Errorf("footer does not show the note: %q", foot)
	}
	l.Handle(Key{Rune: 'j'})
	if l.Note != "" {
		t.Error("the note outlived the next key")
	}

	l.Handle(Key{Rune: 'r'})
	typeKeys(l, "billing fix")
	if foot := Popup(l, 90, 10)[9]; !strings.Contains(foot, "billing fix▏") {
		t.Errorf("footer does not show the new name: %q", foot)
	}
	if o := l.Handle(Key{Name: "enter"}); o != Rename || l.Reply != "billing fix" || l.Renaming {
		t.Errorf("got %v %q", o, l.Reply)
	}
	// An empty name is still a rename: it removes the label.
	l.Handle(Key{Rune: 'r'})
	if o := l.Handle(Key{Name: "enter"}); o != Rename || l.Reply != "" {
		t.Errorf("empty rename = %v %q", o, l.Reply)
	}
	// Renaming starts from the current label, and the label wins over the title.
	p.Label = "billing fix"
	l.All = Agents([]tmux.Pane{p}, now)
	if l.All[0].Name != "billing fix" {
		t.Errorf("name = %q, want the label", l.All[0].Name)
	}
	l.Handle(Key{Rune: 'r'})
	if l.Draft != "billing fix" {
		t.Errorf("draft = %q, want the current label", l.Draft)
	}
	l.Handle(Key{Name: "esc"})

	l.SetPreview("%1", "Which database?\n❯ 1. Postgres\n  2. Redis\n")
	l.Timeline = []string{"e1", "e2", "e3", "e4", "14:05:12  running -> waiting  Permission  question"}
	view := strings.Join(Popup(l, 100, 34), "\n")
	for _, want := range []string{"── billing fix · question · session 2h ──", "running -> waiting", "❯ 1. Postgres", "e2"} {
		if !strings.Contains(view, want) {
			t.Errorf("popup lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "e1") {
		t.Errorf("more than the last four state changes are shown:\n%s", view)
	}
	if lines := Popup(l, 100, 34); len(lines) != 34 {
		t.Errorf("rendered %d lines for a 34-line screen", len(lines))
	}
	// A short preview keeps the screen and drops the timeline.
	if view := strings.Join(Popup(l, 100, 15), "\n"); strings.Contains(view, "running -> waiting") || !strings.Contains(view, "❯ 1. Postgres") {
		t.Errorf("short popup:\n%s", view)
	}
}
