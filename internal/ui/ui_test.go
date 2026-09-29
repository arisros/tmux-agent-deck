package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/arisros/tmux-agent-deck/internal/tmux"
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
	idle := Agents([]tmux.Pane{pane("%1", "a", "1", "idle", "2.1.284", "✳ x", 0)}, now)
	busy := Agents([]tmux.Pane{pane("%1", "a", "1", "running", "2.1.284", "✳ x", 0)}, now)
	if Animated(idle) || !Animated(busy) {
		t.Error("animation must run only while an agent is running")
	}
}
