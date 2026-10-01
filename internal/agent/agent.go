// Package agent describes each coding agent the deck can track: how its hook
// payloads map to machine events, what its screen proves when no hook fires,
// and how its process is recognized.
//
// The state machine, the store and the views know nothing about any one
// agent. Everything that differs between them lives here.
package agent

import (
	"github.com/arisros/tmux-agent-deck/internal/hook"
	"github.com/arisros/tmux-agent-deck/internal/machine"
)

// Agent is one kind of coding agent.
type Agent struct {
	// Name is what `deck hook --agent` and `deck install --<name>` take.
	Name string
	// Map classifies one hook payload. now is unix seconds.
	Map func(p hook.Payload, now int64) (hook.Action, machine.Event)
	// Classify reads a screen into a machine.Screen kind, "" when the screen
	// proves nothing. Nil for an agent whose screen the deck does not read.
	Classify func(screen string) string
	// Evidence is the screen line a verdict relied on, for the log.
	Evidence func(screen string) string
	// Detect recognizes the agent's process by the command tmux reports, for
	// sessions that were running before the hooks were installed. Nil when
	// the process name says nothing, as for an agent started through node.
	Detect func(command string) bool
}

// All lists the agents, Claude first: it is the default.
var All = []Agent{Claude, Codex}

// For returns the agent called name; an empty name is Claude, whose hooks
// were installed before agents had names.
func For(name string) (Agent, bool) {
	if name == "" {
		return Claude, true
	}
	for _, a := range All {
		if a.Name == name {
			return a, true
		}
	}
	return Agent{}, false
}

// Screen classifies screen for a, "" when a reads no screens.
func (a Agent) Screen(screen string) string {
	if a.Classify == nil {
		return ""
	}
	return a.Classify(screen)
}
