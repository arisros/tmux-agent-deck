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
	"github.com/arisros/tmux-agent-deck/internal/tmux"
)

type fakeTmux struct {
	screen  string
	opts    map[string]string // "pane/@name" -> value
	batches [][][]string
}

func newFake() *fakeTmux { return &fakeTmux{opts: map[string]string{}} }

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

// sounds lists the sound commands published; the fake has no client, so
// every pane counts as unwatched.
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

// watchedBranch is what tmux runs for a pane someone is looking at.
func (f *fakeTmux) watchedBranch() string {
	for _, b := range f.batches {
		for _, c := range b {
			if c[0] == "if-shell" {
				return c[len(c)-2]
			}
		}
	}
	return ""
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
	d := newDeck(t, f)
	send(t, d, "%1", ev("UserPromptSubmit", ""))
	send(t, d, "%1", ev("Stop", ""))
	if got := f.watchedBranch(); got != "set-option -p -t %1 @deck_state idle" {
		t.Errorf("watched branch = %q, want the state set to idle without a sound", got)
	}
}

func TestNoVisibilityRoundTrip(t *testing.T) {
	f := newFake()
	d := newDeck(t, f)
	send(t, d, "%1", ev("UserPromptSubmit", ""))
	before := len(f.batches)
	send(t, d, "%1", ev("Stop", ""))
	if len(f.batches)-before != 1 {
		t.Errorf("a Stop took %d tmux calls, want 1", len(f.batches)-before)
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
	// Captured from this project's own session: busy, with the user typing
	// a message that Claude queues. The footer hides "esc to interrupt".
	screenTypingWhileBusy = `────────────────────────────────
❯ need reload or what?
────────────────────────────────
  ⏵⏵ auto mode on (shift+tab to cycle) · ← 5 agents
`
	screenQueued = `────────────────────────────────
❯ Press up to edit queued messages
────────────────────────────────
  ⏵⏵ auto mode on (shift+tab to cycle) · ← 5 agents
`
	// A 50-column pane: Claude cuts the footer off.
	screenNarrowIdle = `──────────────────────────────────────────────────
❯ 
──────────────────────────────────────────────────
  ⏵⏵ auto mode on (shift+tab to cycle) · ← 5 ag…
`
	screenNarrowWorking = `──────────────────────────────────────────────────
❯ 
──────────────────────────────────────────────────
  ⏵⏵ auto mode on (shift+tab to cycle) · esc to…
`
	screenNarrowerWorking = `────────────────────────────────────
❯ 
────────────────────────────────────
  ⏵⏵ auto mode on (shift+tab to cyc…
`
	// An idle prompt with a background agent still working under it.
	screenBackgroundAgent = `────────────────────────────────────────────────────────
❯ 
────────────────────────────────────────────────────────
  ⏵⏵ auto mode on · 1 shell · ← 4 agents · ↓ to manage
  ⏺ main
  ◯ general-purpose  Adding b… 4m 29s · ↓ 95.1k tokens
`
	// This project's own session while a Bash tool ran in auto mode: no
	// "esc to interrupt" anywhere, which once read as idle.
	screenToolRunningAuto = `● Bash(go test ./...)
  ⎿  Running…
                                           ✔ Update installed · Restart to update
──────────────────────────────────────────────────── tmux-agent-deck-plan ─
❯ 
───────────────────────────────────────────────────────────────────────────
  Opus 5.5 (1M context) · ctx 59% · 5h 25% · 7d 5%
  ⏵⏵ auto mode on (shift+tab to cycle) · ← 5 agents
`
	// This project's own session mid-turn: the spinner above the input box
	// is the one sign of work on the screen.
	screenSpinner = `✽ Mustering… (4m 8s · ↓ 13.3k tokens)
                                                           ✔ Update installed · Restart to update
──────────────────────────────────────────────────────────── tmux-agent-deck-plan ─
❯ 
───────────────────────────────────────────────────────────────────────────────────
  Opus 5.5 (1M context) · ctx 64% · 5h 27% · 7d 5%
  ⏵⏵ auto mode on (shift+tab to cycle) · ← 5 agents
`
	// Lab captures: Esc during a running tool, and Esc at a permission prompt.
	screenInterrupted = `  Ran 1 shell command
  ⎿  Interrupted · What should Claude do instead?
────────────────────────────────
❯ 
────────────────────────────────
  ⏸ manual mode on · ? for shortcuts · ← 4 agents
`
	screenDeniedAtPrompt = `✻ Sautéed for 2s · done 2:08 AM
────────────────────────────────
❯ 
────────────────────────────────
  ⏸ manual mode on · ? for shortcuts · ← 4 agents
`
	screenQuestion = `❯ 1. Blue
  2. Red
  6. Chat about this
Enter to select · ↑/↓ to navigate · Esc to cancel
`
)

func TestClassify(t *testing.T) {
	cases := map[string]string{
		screenIdle:            machine.ScreenIdle,
		screenInterrupted:     machine.ScreenIdle,
		screenDeniedAtPrompt:  machine.ScreenIdle,
		screenWorking:         machine.ScreenWorking,
		screenQueued:          machine.ScreenWorking,
		screenNarrowWorking:   machine.ScreenWorking,
		screenBackgroundAgent: machine.ScreenWorking,
		screenDialog:          machine.ScreenDialog,
		screenQuestion:        machine.ScreenDialog,
		// No end marker: never idle, whatever the footer says.
		screenToolRunningAuto: machine.ScreenNoDialog,
		screenSpinner:         machine.ScreenWorking,
		screenTypingWhileBusy: machine.ScreenNoDialog,
		screenNarrowIdle:      machine.ScreenNoDialog,
		screenNarrowerWorking: machine.ScreenNoDialog,
		screenIdleAuto:        machine.ScreenNoDialog,
		"":                    "",
		"$ ls\nfoo\n":         "",
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

func TestDiscoverUnknownClaudePanes(t *testing.T) {
	f := newFake()
	f.screen = screenWorking
	d := newDeck(t, f)
	panes := []tmux.Pane{
		{ID: "%1", Command: "2.1.284"},                   // unknown agent: discovered
		{ID: "%2", Command: "2.1.284", State: "waiting"}, // known: left alone
		{ID: "%3", Command: "zsh"},                       // not claude
		{ID: "%4", Command: "2.1.284", Sidebar: "1"},     // the deck's own sidebar
	}
	if n := d.Discover(panes); n != 1 {
		t.Fatalf("discovered %d panes, want 1", n)
	}
	if f.state("%1") != machine.Running || f.state("%2") != "" || f.state("%3") != "" {
		t.Errorf("states: %v", f.opts)
	}
	if n := d.Discover([]tmux.Pane{{ID: "%1", Command: "2.1.284", State: "running"}}); n != 0 {
		t.Errorf("rediscovered a known pane")
	}
}

// A permission was approved and its tool is still running: no hook arrives
// until the tool ends, so without the screen the agent would stay red.
func TestApprovedPromptLeavesWaitingBeforeTheToolEnds(t *testing.T) {
	f := newFake()
	d := newDeck(t, f)
	send(t, d, "%1", ev("UserPromptSubmit", ""))
	send(t, d, "%1", ev("PermissionRequest", ""))
	f.screen = screenToolRunningAuto
	if err := d.Reconcile("%1", "s1", machine.Waiting); err != nil {
		t.Fatal(err)
	}
	if f.state("%1") != machine.Running {
		t.Errorf("state = %q, want running", f.state("%1"))
	}
}

// The bug this redesign fixes: a running tool must never read as idle.
func TestRunningToolNeverReadsIdle(t *testing.T) {
	f := newFake()
	d := newDeck(t, f)
	send(t, d, "%1", ev("UserPromptSubmit", ""))
	f.screen = screenToolRunningAuto
	if err := d.Reconcile("%1", "s1", machine.Running); err != nil {
		t.Fatal(err)
	}
	if f.state("%1") != machine.Running {
		t.Errorf("state = %q, want running", f.state("%1"))
	}
}
