// Package machine is the agent state machine: which of idle, running,
// waiting, or done a session is in, driven by hook events, focus changes and
// screen checks.
//
// It is a pure fate statechart. The adapter restores a snapshot, sends
// one event, persists the result, and performs every side effect itself, so
// the machine stays deterministic and table-testable. The clock arrives inside
// events for the same reason.
package machine

import (
	"context"
	"fmt"

	"github.com/arisros/fate"
)

// States of an agent.
const (
	Idle    = "idle"
	Running = "running"
	Waiting = "waiting"
	Done    = "done"
)

// Ctx is the machine context persisted with every snapshot.
type Ctx struct {
	// Since is when the current state was entered, in unix seconds.
	Since int64 `json:"since"`
	// Background is how many background tasks the last Stop reported.
	Background int `json:"bg,omitempty"`
	// Reason is why the agent is waiting; empty in every other state.
	Reason string `json:"reason,omitempty"`
	// Tool is the tool a permission was asked for.
	Tool string `json:"tool,omitempty"`
	// Source is what caused the last transition.
	Source string `json:"src,omitempty"`
}

// Reasons an agent waits.
const (
	ReasonPermission  = "permission"
	ReasonQuestion    = "question"
	ReasonElicitation = "elicitation"
	ReasonInput       = "input"
	// ReasonDialog is a dialog found on screen: no hook said what it asks.
	ReasonDialog = "dialog"
)

// Sources of a transition.
const (
	SourceHook   = "hook"
	SourceScreen = "screen"
	SourceFocus  = "focus"
)

// Event is anything the machine reacts to. EventName keeps fate off its
// reflection fallback.
type Event interface{ EventName() string }

// Begin starts a session (SessionStart on startup, resume, or clear). Every
// state accepts it, so a pane reused by a new session starts clean.
type Begin struct{ At int64 }

// Prompt is a UserPromptSubmit. Claude also emits one on its own when a
// background task or subagent finishes and it resumes.
type Prompt struct{ At int64 }

// ToolStart is a PreToolUse.
type ToolStart struct{ At int64 }

// ToolEnd is a PostToolUse or PostToolUseFailure. Subagent is set when a
// subagent, not the main agent, ran the tool.
type ToolEnd struct {
	At       int64
	Subagent bool
}

// Permission is a PermissionRequest. Reason tells a question the agent asks
// from a tool it wants to run; Tool names that tool.
type Permission struct {
	At           int64
	Reason, Tool string
}

// NeedsInput is a Notification asking for the user.
type NeedsInput struct {
	At     int64
	Reason string
}

// IdlePrompt is Claude's idle_prompt notification: the agent has sat at its
// prompt for a while. It is the only end-of-turn signal after a turn Claude
// resumed by itself when a background task finished, which emits no Stop.
type IdlePrompt struct{ At int64 }

// Stop ends a turn. Background counts tasks still running.
//
// A turn the user watched end is idle rather than done. The machine does not
// ask: the adapter resolves it inside tmux in the same call that publishes
// the state, which saves a round trip per turn. Stored done and displayed
// idle behave the same for every event except Focus, which only fires on a
// displayed done.
type Stop struct {
	At         int64
	Background int
}

// Interrupt is the user stopping a turn, for agents whose hooks report it.
// Claude Code reports nothing, and its Esc is read from the screen instead.
type Interrupt struct{ At int64 }

// Focus means the user looked at the pane.
type Focus struct{ At int64 }

// Screen is what the pane's footer shows. It repairs the cases no hook
// reports: Esc mid-turn and a denied permission end the turn silently.
type Screen struct {
	At   int64
	Kind string
}

// Screen kinds.
const (
	ScreenIdle    = "idle"
	ScreenWorking = "working"
	ScreenDialog  = "dialog"
	// ScreenNoDialog is a Claude screen with no dialog and no stronger sign:
	// a pending prompt has been answered, which is all it proves.
	ScreenNoDialog = "nodialog"
)

// EventName implements Event.
func (Begin) EventName() string { return "Begin" }

// EventName implements Event.
func (Prompt) EventName() string { return "Prompt" }

// EventName implements Event.
func (ToolStart) EventName() string { return "ToolStart" }

// EventName implements Event.
func (ToolEnd) EventName() string { return "ToolEnd" }

// EventName implements Event.
func (Permission) EventName() string { return "Permission" }

// EventName implements Event.
func (NeedsInput) EventName() string { return "NeedsInput" }

// EventName implements Event.
func (IdlePrompt) EventName() string { return "IdlePrompt" }

// EventName implements Event.
func (Stop) EventName() string { return "Stop" }

// EventName implements Event.
func (Interrupt) EventName() string { return "Interrupt" }

// EventName implements Event.
func (Focus) EventName() string { return "Focus" }

// EventName implements Event.
func (Screen) EventName() string { return "Screen" }

type (
	tr     = fate.TransitionConfig[Ctx, Event]
	action = fate.Action[Ctx, Event]
)

func at(e Event) int64 {
	switch e := e.(type) {
	case Begin:
		return e.At
	case Prompt:
		return e.At
	case ToolStart:
		return e.At
	case ToolEnd:
		return e.At
	case Permission:
		return e.At
	case NeedsInput:
		return e.At
	case IdlePrompt:
		return e.At
	case Stop:
		return e.At
	case Focus:
		return e.At
	case Interrupt:
		return e.At
	case Screen:
		return e.At
	}
	return 0
}

var enter = fate.Named("since", fate.Assign(func(c Ctx, e Event) Ctx {
	c.Since = at(e)
	c.Reason, c.Tool = "", ""
	switch e.(type) {
	case Screen:
		c.Source = SourceScreen
	case Focus:
		c.Source = SourceFocus
	default:
		c.Source = SourceHook
	}
	return c
}))

var why = fate.Named("why", fate.Assign(func(c Ctx, e Event) Ctx {
	switch e := e.(type) {
	case Permission:
		c.Reason, c.Tool = e.Reason, e.Tool
	case NeedsInput:
		c.Reason = e.Reason
	case Screen:
		c.Reason = ReasonDialog
	}
	return c
}))

var recordBackground = fate.Named("bg", fate.Assign(func(c Ctx, e Event) Ctx {
	if s, ok := e.(Stop); ok {
		c.Background = s.Background
	}
	return c
}))

func entering(target string) []action {
	if target == Waiting {
		return []action{enter, why}
	}
	return []action{enter}
}

func to(target string) tr { return tr{Target: target, Actions: entering(target)} }

func when(target, name string, guard func(Ctx, Event) bool) tr {
	return tr{Target: target, Guard: guard, GuardName: name, Actions: entering(target)}
}

func screen(kind string) func(Ctx, Event) bool {
	return func(_ Ctx, e Event) bool { s, ok := e.(Screen); return ok && s.Kind == kind }
}

func background(_ Ctx, e Event) bool { s, ok := e.(Stop); return ok && s.Background > 0 }
func mainAgent(_ Ctx, e Event) bool  { t, ok := e.(ToolEnd); return ok && !t.Subagent }

// A Stop with background work left is not the end: Claude resumes by itself
// when the work finishes, so the agent stays running and nobody is paged.
func stopTransitions() []tr {
	return []tr{
		{Guard: background, GuardName: "background", Actions: []action{recordBackground}},
		{Target: Done, Actions: []action{enter, recordBackground}},
	}
}

// New builds the machine. Build it once per process; it is cheap enough that
// a hook invocation can afford it (see the benchmark).
func New() (*Machine, error) {
	return fate.CreateMachine(fate.MachineConfig[Ctx, Event]{
		ID:      "agent",
		Initial: Idle,
		States: map[string]fate.StateNodeConfig[Ctx, Event]{
			Idle: {On: map[string][]tr{
				"Begin":      {to(Idle)},
				"Prompt":     {to(Running)},
				"ToolStart":  {to(Running)},
				"Permission": {to(Waiting)},
				"NeedsInput": {to(Waiting)},
				"Screen":     {when(Running, "working", screen(ScreenWorking)), when(Waiting, "dialog", screen(ScreenDialog))},
			}},
			Running: {On: map[string][]tr{
				"Begin":      {to(Idle)},
				"Permission": {to(Waiting)},
				"NeedsInput": {to(Waiting)},
				"Stop":       stopTransitions(),
				"IdlePrompt": {to(Done)},
				"Interrupt":  {to(Idle)},
				"Screen":     {when(Idle, "idle", screen(ScreenIdle)), when(Waiting, "dialog", screen(ScreenDialog))},
			}},
			// A subagent's tool finishing says nothing about the main agent's
			// pending prompt, so only a main-agent ToolEnd resolves waiting.
			Waiting: {On: map[string][]tr{
				"Begin":     {to(Idle)},
				"ToolEnd":   {when(Running, "main agent", mainAgent)},
				"Prompt":    {to(Running)},
				"Stop":      stopTransitions(),
				"Interrupt": {to(Idle)},
				"Screen": {when(Idle, "idle", screen(ScreenIdle)), when(Running, "working", screen(ScreenWorking)),
					when(Running, "answered", screen(ScreenNoDialog))},
			}},
			Done: {On: map[string][]tr{
				"Begin":      {to(Idle)},
				"Prompt":     {to(Running)},
				"ToolStart":  {to(Running)},
				"Permission": {to(Waiting)},
				"NeedsInput": {to(Waiting)},
				"Focus":      {to(Idle)},
				"Screen":     {when(Running, "working", screen(ScreenWorking)), when(Waiting, "dialog", screen(ScreenDialog))},
			}},
		},
	})
}

// Result is the outcome of applying one event.
type Result struct {
	From, To string
	Ctx      Ctx
	Snapshot []byte
}

// Entered reports whether the event moved the agent into a new state.
func (r Result) Entered(state string) bool { return r.To == state && r.From != state }

// Apply restores snapshot (nil starts fresh at idle), sends e, and returns
// the new state with its persisted snapshot.
func Apply(m *Machine, snapshot []byte, e Event) (Result, error) {
	var a *fate.Actor[Ctx, Event]
	if snapshot == nil {
		a = fate.NewActor(m)
		if err := a.Start(context.Background()); err != nil {
			return Result{}, err
		}
	} else {
		var err error
		if a, err = fate.NewActorFromSnapshot[Ctx, Event](m, snapshot); err != nil {
			return Result{}, fmt.Errorf("restore snapshot: %w", err)
		}
	}
	from := a.Snapshot().Value.Path()
	if err := a.Send(context.Background(), e); err != nil {
		return Result{}, err
	}
	snap := a.Snapshot()
	blob, err := a.Persist()
	if err != nil {
		return Result{}, err
	}
	return Result{From: from, To: snap.Value.Path(), Ctx: snap.Context, Snapshot: blob}, nil
}

// Machine is the compiled agent machine.
type Machine = fate.Machine[Ctx, Event]
