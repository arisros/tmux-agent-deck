// Package ytta is the adapter around the pure state machine: it loads and
// saves session records, asks tmux the few questions the machine needs, and
// publishes the result as pane options that tmux formats render.
package ytta

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/arisros/ytta/internal/agent"
	"github.com/arisros/ytta/internal/events"
	"github.com/arisros/ytta/internal/hook"
	"github.com/arisros/ytta/internal/machine"
	"github.com/arisros/ytta/internal/store"
	"github.com/arisros/ytta/internal/tmux"
	"github.com/arisros/ytta/internal/usage"
)

// Tmux is the subset of tmux the adapter uses, so tests can fake it.
type Tmux interface {
	Batch(cmds [][]string) error
	Capture(pane string) (string, error)
	PaneOption(pane, name string) (string, error)
}

// Ytta wires the machine to a store and a tmux server.
type Ytta struct {
	Tmux    Tmux
	Dir     string
	Machine *machine.Machine
	Now     func() time.Time
	// Log, when set, records every state change a screen check makes: those
	// are inferences, and a wrong one should leave a trace.
	Log func(string)
	// Emit, when set, receives every state change, for the event log.
	Emit func(events.Event)
}

// New returns a Ytta for the given tmux server and state directory.
func New(t Tmux, dir string) (*Ytta, error) {
	m, err := machine.New()
	if err != nil {
		return nil, err
	}
	return &Ytta{Tmux: t, Dir: dir, Machine: m, Now: time.Now}, nil
}

// Signal is the wait-for channel sidebars block on.
const Signal = "ytta"

// Hook applies one hook payload from an agent session running in pane.
func (d *Ytta) Hook(p hook.Payload, pane string) error {
	now := d.Now().Unix()
	a, ok := agent.For(p.Agent)
	if !ok {
		return fmt.Errorf("unknown agent %q", p.Agent)
	}
	action, ev := a.Map(p, now)
	if action == hook.Ignore || p.SessionID == "" {
		return nil
	}
	l, err := store.Open(d.Dir, p.SessionID)
	if err != nil {
		return err
	}
	defer l.Close()
	rec, existed := l.Record()
	if pane == "" {
		pane = rec.Pane
	}

	switch action {
	case hook.End:
		if err := l.Delete(); err != nil {
			return err
		}
		d.emit(events.Event{SID: p.SessionID, Pane: pane, Kind: events.End, Source: machine.SourceHook})
		if pane == "" {
			return nil
		}
		return d.Tmux.Batch(clear(pane))
	case hook.Begin:
		store.Prune(d.Dir, 72*time.Hour)
		usage.Prune(usage.DefaultDir(d.Dir), 72*time.Hour)
		var snap []byte
		if existed {
			snap = rec.Snapshot
		}
		res, err := machine.Apply(d.Machine, snap, machine.Begin{At: now})
		if err != nil {
			return err
		}
		if err := l.Save(store.Record{Pane: pane, Agent: a.Name, Started: now, Snapshot: res.Snapshot}); err != nil {
			return err
		}
		d.emitResult(events.Begin, p.SessionID, pane, res)
		return d.publish(pane, p.SessionID, a.Name, res, "", now)
	}

	var snap []byte
	if existed {
		snap = rec.Snapshot
	}
	res, err := machine.Apply(d.Machine, snap, ev)
	if err != nil {
		return err
	}
	// A session ytta first hears from mid-flight started, as far as it
	// knows, now.
	started, fresh := rec.Started, int64(0)
	if !existed {
		started, fresh = now, now
	}
	if err := l.Save(store.Record{Pane: pane, Agent: a.Name, Started: started, Snapshot: res.Snapshot}); err != nil {
		return err
	}
	if res.From != res.To {
		d.emitResult(ev.EventName(), p.SessionID, pane, res)
	}
	// Tool calls inside a running turn are the hot path and change nothing:
	// no tmux round trip at all.
	if existed && res.From == res.To && rec.Pane == pane {
		return nil
	}
	sound := ""
	switch {
	case res.Entered(machine.Waiting):
		sound = "waiting"
	case res.Entered(machine.Done) && isStop(ev):
		sound = "done"
	}
	return d.publish(pane, p.SessionID, a.Name, res, sound, fresh)
}

func isStop(e machine.Event) bool { _, ok := e.(machine.Stop); return ok }

// Focus tells the machine the user looked at pane; a done agent becomes idle.
func (d *Ytta) Focus(pane string) error {
	sid, err := d.Tmux.PaneOption(pane, "@ytta_sid")
	if err != nil || sid == "" {
		return err
	}
	return d.send(pane, sid, machine.Focus{At: d.Now().Unix()})
}

// Reconcile corrects a running or waiting agent from what its screen shows.
// Claude Code fires no hook on Esc mid-turn or on a denied permission, so
// without this an agent would look busy until its next prompt. Each agent
// reads its own screen; one ytta cannot read is left alone.
func (d *Ytta) Reconcile(pane, sid, state string) error {
	if state != machine.Running && state != machine.Waiting {
		return nil
	}
	if sid == "" {
		var err error
		if sid, err = d.Tmux.PaneOption(pane, "@ytta_sid"); err != nil || sid == "" {
			return err
		}
	}
	a := d.agentOf(sid)
	if a.Classify == nil {
		return nil
	}
	screen, err := d.Tmux.Capture(pane)
	if err != nil {
		return err
	}
	kind := a.Classify(screen)
	if kind == "" {
		return nil
	}
	from := state
	err = d.send(pane, sid, machine.Screen{At: d.Now().Unix(), Kind: kind})
	if to, _ := d.Tmux.PaneOption(pane, "@ytta_state"); to != from && d.Log != nil {
		last := ""
		if a.Evidence != nil {
			last = a.Evidence(screen)
		}
		d.Log(fmt.Sprintf("reconcile %s: %s -> %s (screen %s, last line %q)", pane, from, to, kind, last))
	}
	return err
}

// agentOf is the agent a session's record names; Claude when there is no
// record, as for a pane found by its screen.
func (d *Ytta) agentOf(sid string) agent.Agent {
	l, err := store.OpenExisting(d.Dir, sid)
	if err != nil {
		return agent.Claude
	}
	defer l.Close()
	rec, _ := l.Record()
	if a, ok := agent.For(rec.Agent); ok {
		return a
	}
	return agent.Claude
}

// ReconcileStale reconciles every agent that has been running or waiting for
// at least minAge, which is what a view does before it renders.
func (d *Ytta) ReconcileStale(panes []tmux.Pane, minAge time.Duration) {
	now := d.Now().Unix()
	for _, p := range panes {
		if p.SID != "" && now-p.Since >= int64(minAge.Seconds()) {
			_ = d.Reconcile(p.ID, p.SID, p.State)
		}
	}
}

// Discover publishes a state for every agent pane ytta has no session for,
// read from its screen. An agent that was already open when the hooks were
// installed fires no hook until its next prompt or tool, and would otherwise
// stay invisible, or keep the state it was first found in for the rest of a
// long turn. Only agents recognizable by their process name can be found
// this way. The state has no session record behind it; the session's next
// hook creates one and takes over. It returns how many panes it set.
func (d *Ytta) Discover(panes []tmux.Pane) int {
	now := strconv.FormatInt(d.Now().Unix(), 10)
	var cmds [][]string
	var found []events.Event
	for _, p := range panes {
		a, ok := detect(p.Command)
		if p.SID != "" || p.Sidebar != "" || !ok {
			continue
		}
		screen, err := d.Tmux.Capture(p.ID)
		if err != nil {
			continue
		}
		state := screenState(p.State, a.Screen(screen))
		if state == p.State {
			continue
		}
		cmds = append(cmds,
			[]string{"set-option", "-p", "-t", p.ID, "@ytta_state", state},
			[]string{"set-option", "-p", "-t", p.ID, "@ytta_since", now},
			[]string{"set-option", "-p", "-t", p.ID, "@ytta_agent", a.Name},
			remember(p.ID))
		found = append(found, events.Event{Pane: p.ID, Kind: events.Discover, From: p.State, To: state, Source: machine.SourceScreen})
	}
	if len(cmds) == 0 {
		return 0
	}
	cmds = append(cmds, []string{"wait-for", "-S", Signal})
	if err := d.Tmux.Batch(cmds); err != nil {
		return 0
	}
	for _, e := range found {
		d.emit(e)
	}
	return len(found)
}

// screenState is the state a pane without a session moves to from what its
// screen shows. A screen that proves nothing keeps the state, idle for a
// pane seen for the first time: a fresh or quiet session has no end marker.
func screenState(state, kind string) string {
	switch {
	case kind == machine.ScreenWorking:
		return machine.Running
	case kind == machine.ScreenDialog:
		return machine.Waiting
	case kind == machine.ScreenIdle, state == "":
		return machine.Idle
	case kind == machine.ScreenNoDialog && state == machine.Waiting:
		return machine.Running
	}
	return state
}

func detect(command string) (agent.Agent, bool) {
	for _, a := range agent.All {
		if a.Detect != nil && a.Detect(command) {
			return a, true
		}
	}
	return agent.Agent{}, false
}

// Sweep forgets every agent whose process has left its pane: the state
// options are unset and the session record deleted, so a pane that fell back
// to a shell stops being an agent at once instead of at the 72 hour prune.
// It returns how many panes it cleared.
func (d *Ytta) Sweep(panes []tmux.Pane) int {
	var cmds [][]string
	var gone []tmux.Pane
	for _, p := range panes {
		if p.State == "" || p.Sidebar != "" || p.Alive() {
			continue
		}
		cmds = append(cmds, unset(p.ID)...)
		gone = append(gone, p)
	}
	if len(gone) == 0 {
		return 0
	}
	cmds = append(cmds, []string{"wait-for", "-S", Signal})
	if err := d.Tmux.Batch(cmds); err != nil {
		return 0
	}
	for _, p := range gone {
		if p.SID != "" {
			if l, err := store.OpenExisting(d.Dir, p.SID); err == nil {
				_ = l.Delete()
				l.Close()
			}
		}
		d.emit(events.Event{SID: p.SID, Pane: p.ID, Kind: events.Exit, From: p.State})
	}
	return len(gone)
}

// remember stores the pane's foreground command, which is the agent while
// one of its hooks runs. tmux expands the format itself, in the same call.
func remember(pane string) []string {
	return []string{"set-option", "-p", "-F", "-t", pane, "@ytta_cmd", "#{pane_current_command}"}
}

func (d *Ytta) send(pane, sid string, ev machine.Event) error {
	l, err := store.OpenExisting(d.Dir, sid)
	if errors.Is(err, os.ErrNotExist) {
		return nil // pruned or never recorded: nothing to update
	}
	if err != nil {
		return err
	}
	defer l.Close()
	rec, existed := l.Record()
	if !existed {
		return nil
	}
	res, err := machine.Apply(d.Machine, rec.Snapshot, ev)
	if err != nil {
		return err
	}
	if res.From == res.To {
		return nil
	}
	if err := l.Save(store.Record{Pane: rec.Pane, Agent: rec.Agent, Started: rec.Started, Snapshot: res.Snapshot}); err != nil {
		return err
	}
	d.emitResult(ev.EventName(), sid, pane, res)
	name := rec.Agent
	if name == "" {
		name = agent.Claude.Name
	}
	return d.publish(pane, sid, name, res, "", 0)
}

func (d *Ytta) emitResult(kind, sid, pane string, res machine.Result) {
	d.emit(events.Event{SID: sid, Pane: pane, Kind: kind, From: res.From, To: res.To,
		Source: res.Ctx.Source, Reason: res.Ctx.Reason, Tool: res.Ctx.Tool})
}

func (d *Ytta) emit(e events.Event) {
	if d.Emit == nil {
		return
	}
	e.TS = d.Now().UnixMilli()
	d.Emit(e)
}

// shellWords keeps the characters a reason is made of, so nothing a hook
// payload carried can end the quoting it is placed in.
func shellWords(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == ' ', r == '_', r == '-', r == '.':
			return r
		}
		return -1
	}, s)
}

// reason is what @ytta_reason shows: "permission Bash", "question".
func reason(c machine.Ctx) string {
	if c.Tool == "" {
		return c.Reason
	}
	return c.Reason + " " + c.Tool
}

// publish writes a session's state to its pane. started, when not zero, is
// also written: it only changes when a session begins.
func (d *Ytta) publish(pane, sid, name string, res machine.Result, sound string, started int64) error {
	if pane == "" {
		return nil
	}
	cmds := [][]string{
		{"set-option", "-p", "-t", pane, "@ytta_state", res.To},
		{"set-option", "-p", "-t", pane, "@ytta_since", strconv.FormatInt(res.Ctx.Since, 10)},
		{"set-option", "-p", "-t", pane, "@ytta_sid", sid},
		{"set-option", "-p", "-t", pane, "@ytta_agent", name},
	}
	cmds = append(cmds, remember(pane))
	if started > 0 {
		cmds = append(cmds, []string{"set-option", "-p", "-t", pane, "@ytta_started", strconv.FormatInt(started, 10)})
	}
	if why := reason(res.Ctx); why != "" {
		cmds = append(cmds, []string{"set-option", "-p", "-t", pane, "@ytta_reason", why})
	} else {
		cmds = append(cmds, []string{"set-option", "-p", "-u", "-t", pane, "@ytta_reason"})
	}
	// tmux decides and plays in the same round trip: nothing is queried
	// first, and the player runs detached from the hook. The user's notify
	// command follows the same path, with the state, the pane and the reason
	// as arguments; a pane title or a prompt never reaches a shell here.
	play := `if-shell -F "#{!=:#{@ytta-sound},off}" "run-shell -b '#{@ytta-sound-command} #{@ytta-sound-` + sound + `}'" ; ` +
		`if-shell -F "#{@ytta-notify-command}" "run-shell -b '#{@ytta-notify-command} ` +
		strings.TrimSpace(sound+" "+pane+" "+shellWords(reason(res.Ctx))) + `'"`
	switch {
	case sound == "done":
		// A turn the user watched end is idle and silent.
		cmds = append(cmds, []string{"if-shell", "-F", "-t", pane, tmux.VisibleFormat,
			"set-option -p -t " + pane + " @ytta_state idle", play})
	case sound != "":
		cmds = append(cmds, []string{"if-shell", "-F", "-t", pane, tmux.VisibleFormat, "", play})
	}
	cmds = append(cmds, []string{"wait-for", "-S", Signal})
	return d.Tmux.Batch(cmds)
}

func clear(pane string) [][]string {
	return append(unset(pane), []string{"wait-for", "-S", Signal})
}

func unset(pane string) [][]string {
	var cmds [][]string
	for _, o := range []string{"@ytta_state", "@ytta_since", "@ytta_sid", "@ytta_reason", "@ytta_cmd", "@ytta_agent", "@ytta_started"} {
		cmds = append(cmds, []string{"set-option", "-p", "-u", "-t", pane, o})
	}
	return cmds
}
