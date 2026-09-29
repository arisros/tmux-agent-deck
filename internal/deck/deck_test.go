package deck

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arisros/tmux-agent-deck/internal/hook"
	"github.com/arisros/tmux-agent-deck/internal/machine"
)

type fakeTmux struct {
	visible bool
	screen  string
	opts    map[string]string // "pane/@name" -> value
	batches [][][]string
}

func newFake() *fakeTmux { return &fakeTmux{opts: map[string]string{}} }

func (f *fakeTmux) Visible(string) (bool, error)           { return f.visible, nil }
func (f *fakeTmux) Capture(string) (string, error)         { return f.screen, nil }
func (f *fakeTmux) PaneOption(p, n string) (string, error) { return f.opts[p+"/"+n], nil }

func (f *fakeTmux) Batch(cmds [][]string) error {
	f.batches = append(f.batches, cmds)
	for _, c := range cmds {
		if len(c) == 6 && c[0] == "set-option" && c[1] == "-p" {
			f.opts[c[3]+"/"+c[4]] = c[5]
		}
		if len(c) == 6 && c[0] == "set-option" && c[2] == "-u" {
			delete(f.opts, c[4]+"/"+c[5])
		}
	}
	return nil
}

func (f *fakeTmux) state(pane string) string { return f.opts[pane+"/@deck_state"] }

func (f *fakeTmux) sounds() []string {
	var out []string
	for _, b := range f.batches {
		for _, c := range b {
			if c[0] == "if-shell" {
				out = append(out, c[len(c)-1])
			}
		}
	}
	return out
}

func newDeck(t *testing.T, f *fakeTmux) *Deck {
	t.Helper()
	d, err := New(f, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d.Now = func() time.Time { return time.Unix(1000, 0) }
	return d
}

func send(t *testing.T, d *Deck, pane, payload string) {
	t.Helper()
	p, err := hook.Decode(strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Hook(p, pane); err != nil {
		t.Fatal(err)
	}
}

func ev(name, extra string) string {
	return fmt.Sprintf(`{"hook_event_name":%q,"session_id":"s1"%s}`, name, extra)
}

func TestHotPathSkipsTmux(t *testing.T) {
	f := newFake()
	d := newDeck(t, f)
	send(t, d, "%1", ev("SessionStart", `,"source":"startup"`))
	send(t, d, "%1", ev("UserPromptSubmit", ""))
	before := len(f.batches)
	for i := 0; i < 20; i++ {
		send(t, d, "%1", ev("PreToolUse", ""))
		send(t, d, "%1", ev("PostToolUse", ""))
	}
	if len(f.batches) != before {
		t.Errorf("tool calls inside a running turn made %d tmux calls, want 0", len(f.batches)-before)
	}
	if f.state("%1") != machine.Running {
		t.Errorf("state = %q", f.state("%1"))
	}
}

func TestSoundsOnlyOnEdges(t *testing.T) {
	f := newFake()
	d := newDeck(t, f)
	send(t, d, "%1", ev("UserPromptSubmit", ""))
	send(t, d, "%1", ev("PermissionRequest", ""))
	send(t, d, "%1", ev("Notification", `,"notification_type":"permission_prompt"`))
	send(t, d, "%1", ev("PostToolUse", ""))
	send(t, d, "%1", ev("Stop", `,"background_tasks":[]`))
	got := f.sounds()
	if len(got) != 2 || !strings.Contains(got[0], "sound-waiting") || !strings.Contains(got[1], "sound-done") {
		t.Errorf("sounds = %v, want one waiting then one done", got)
	}
	if f.state("%1") != machine.Done {
		t.Errorf("state = %q", f.state("%1"))
	}
}

func TestWatchedStopIsIdleAndSilent(t *testing.T) {
	f := newFake()
	f.visible = true
	d := newDeck(t, f)
	send(t, d, "%1", ev("UserPromptSubmit", ""))
	send(t, d, "%1", ev("Stop", ""))
	if f.state("%1") != machine.Idle || len(f.sounds()) != 0 {
		t.Errorf("state %q, sounds %v", f.state("%1"), f.sounds())
	}
}

func TestSessionEndClearsPane(t *testing.T) {
	f := newFake()
	d := newDeck(t, f)
	send(t, d, "%1", ev("UserPromptSubmit", ""))
	send(t, d, "%1", ev("SessionEnd", `,"reason":"prompt_input_exit"`))
	if _, ok := f.opts["%1/@deck_state"]; ok {
		t.Error("state not cleared")
	}
	if _, err := os.Stat(filepath.Join(d.Dir, "s1.json")); !os.IsNotExist(err) {
		t.Error("record not deleted")
	}
}

func TestFocusClearsDone(t *testing.T) {
	f := newFake()
	d := newDeck(t, f)
	send(t, d, "%1", ev("UserPromptSubmit", ""))
	send(t, d, "%1", ev("Stop", ""))
	if err := d.Focus("%1"); err != nil {
		t.Fatal(err)
	}
	if f.state("%1") != machine.Idle {
		t.Errorf("state = %q", f.state("%1"))
	}
}

func TestReconcileRepairsSilentEndings(t *testing.T) {
	f := newFake()
	d := newDeck(t, f)
	send(t, d, "%1", ev("UserPromptSubmit", ""))
	send(t, d, "%1", ev("PermissionRequest", ""))
	f.screen = screenIdle
	if err := d.Reconcile("%1", "s1", machine.Waiting); err != nil {
		t.Fatal(err)
	}
	if f.state("%1") != machine.Idle {
		t.Errorf("state = %q, want idle after a silent deny", f.state("%1"))
	}
}

func TestIgnoresPayloadWithoutSession(t *testing.T) {
	f := newFake()
	d := newDeck(t, f)
	send(t, d, "%1", `{"hook_event_name":"UserPromptSubmit"}`)
	if len(f.batches) != 0 {
		t.Error("published without a session id")
	}
}

// Screens captured from Claude Code v2.1.284 in the lab session.
const (
	screenIdle = `❯ run exactly: touch /tmp/x
⏺ Done.
✻ Worked for 2s · done 2:09 AM
────────────────────────────────
❯
────────────────────────────────
  ⏸ manual mode on · ? for shortcuts · ← 4 agents
`
	screenIdleAuto = `────────────────────────────────
❯ Try "fix typecheck errors"
────────────────────────────────
  ⏵⏵ auto mode on (shift+tab to cycle) · ← 4 agents
`
	screenWorking = `  ⎿  Tip: Use Plan Mode to prepare for a complex request.
────────────────────────────────
❯
────────────────────────────────
  ⏸ manual mode on · esc to interrupt · ← 4 agents
`
	screenDialog = ` Bash command
   touch /tmp/deck-lab/a
 Do you want to proceed?
 ❯ 1. Yes
   4. No
 Esc to cancel · Tab to amend
`
	screenQuestion = `❯ 1. Blue
  2. Red
  6. Chat about this
Enter to select · ↑/↓ to navigate · Esc to cancel
`
)

func TestClassify(t *testing.T) {
	cases := map[string]string{
		screenIdle:     machine.ScreenIdle,
		screenIdleAuto: machine.ScreenIdle,
		screenWorking:  machine.ScreenWorking,
		screenDialog:   machine.ScreenDialog,
		screenQuestion: machine.ScreenDialog,
		"":             "",
		"$ ls\nfoo\n":  "",
	}
	for screen, want := range cases {
		if got := Classify(screen); got != want {
			t.Errorf("Classify(%q) = %q, want %q", screen, got, want)
		}
	}
}

// TestFixtures replays sequences recorded from real sessions through the
// adapter. The screen step stands in for the footer check a view or a
// focus change runs, since those sequences end without any hook.
func TestFixtures(t *testing.T) {
	cases := map[string]struct {
		hooksOnly string // state after the recorded hooks
		screen    string // screen seen afterwards, "" for none
		final     string
	}{
		"permission-approve":  {machine.Done, "", machine.Done},
		"permission-deny-esc": {machine.Waiting, screenIdle, machine.Idle},
		"permission-deny-no":  {machine.Waiting, screenIdle, machine.Idle},
		"esc-during-tool":     {machine.Waiting, screenIdle, machine.Idle},
		"esc-queued-prompt":   {machine.Done, "", machine.Done},
		"subagent":            {machine.Done, "", machine.Done},
		"background-bash":     {machine.Done, "", machine.Done},
		"ask-user-question":   {machine.Done, "", machine.Done},
		"session-start-exit":  {"", "", ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake()
			d := newDeck(t, f)
			for _, line := range readFixture(t, name) {
				send(t, d, "%1", line)
			}
			if got := f.state("%1"); got != c.hooksOnly {
				t.Fatalf("after hooks: %q, want %q", got, c.hooksOnly)
			}
			if c.screen != "" {
				f.screen = c.screen
				if err := d.Reconcile("%1", "", f.state("%1")); err != nil {
					t.Fatal(err)
				}
			}
			if got := f.state("%1"); got != c.final {
				t.Errorf("final: %q, want %q", got, c.final)
			}
		})
	}
}

// readFixture converts a redacted fixture line back into a hook payload.
func readFixture(t *testing.T, name string) []string {
	t.Helper()
	file, err := os.Open(filepath.Join("..", "..", "test", "fixtures", name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var out []string
	sc := bufio.NewScanner(file)
	for sc.Scan() {
		var r struct {
			Event, NotificationType, Source, Reason string
			BackgroundTasks                         *int `json:"background_tasks"`
			Subagent                                bool
		}
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		var n struct {
			NotificationType string `json:"notification_type"`
		}
		_ = json.Unmarshal(sc.Bytes(), &n)
		p := map[string]any{"hook_event_name": r.Event, "session_id": "s1"}
		if n.NotificationType != "" {
			p["notification_type"] = n.NotificationType
		}
		if r.Source != "" {
			p["source"] = r.Source
		}
		if r.Subagent {
			p["agent_id"] = "a1"
		}
		if r.BackgroundTasks != nil {
			p["background_tasks"] = make([]int, *r.BackgroundTasks)
		}
		b, _ := json.Marshal(p)
		out = append(out, string(b))
	}
	return out
}
