// Package machine is the agent state machine: which of idle, running,
// waiting, or done a Claude session is in, driven by hook events.
//
// It is a pure fate statechart. The hook adapter restores a snapshot, sends
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
}

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

// Permission is a PermissionRequest, including AskUserQuestion prompts.
type Permission struct{ At int64 }

// NeedsInput is a Notification asking for the user (permission_prompt,
// elicitation dialogs).
type NeedsInput struct{ At int64 }

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
	case Screen:
		return e.At
	}
	return 0
}

var enter = fate.Named("since", fate.Assign(func(c Ctx, e Event) Ctx {
	c.Since = at(e)
	return c
}))

var recordBackground = fate.Named("bg", fate.Assign(func(c Ctx, e Event) Ctx {
	if s, ok := e.(Stop); ok {
		c.Background = s.Background
	}
	return c
}))

func to(target string) tr { return tr{Target: target, Actions: []action{enter}} }

func when(target, name string, guard func(Ctx, Event) bool) tr {
	return tr{Target: target, Guard: guard, GuardName: name, Actions: []action{enter}}
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
				"Screen":     {when(Idle, "idle", screen(ScreenIdle)), when(Waiting, "dialog", screen(ScreenDialog))},
			}},
			// A subagent's tool finishing says nothing about the main agent's
			// pending prompt, so only a main-agent ToolEnd resolves waiting.
			Waiting: {On: map[string][]tr{
				"Begin":   {to(Idle)},
				"ToolEnd": {when(Running, "main agent", mainAgent)},
				"Prompt":  {to(Running)},
				"Stop":    stopTransitions(),
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
