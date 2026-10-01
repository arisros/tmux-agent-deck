// Package hook turns a Claude Code hook payload into a machine event.
//
// The mapping follows sequences recorded from real sessions (see
// test/fixtures), not only the documentation. Notably: a denied permission and
// Esc mid-turn emit no event at all; Agent tool calls return at once while the
// subagent keeps working; and a Stop can arrive while background work still
// runs.
package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/arisros/tmux-agent-deck/internal/machine"
)

// Payload holds the fields the deck reads. Everything else in the hook input,
// including prompts and tool arguments, is never decoded into memory we keep.
type Payload struct {
	Event            string          `json:"hook_event_name"`
	SessionID        string          `json:"session_id"`
	NotificationType string          `json:"notification_type"`
	ToolName         string          `json:"tool_name"`
	AgentID          string          `json:"agent_id"`
	Source           string          `json:"source"`
	BackgroundTasks  json.RawMessage `json:"background_tasks"`
}

// Decode reads one payload.
func Decode(r io.Reader) (Payload, error) {
	var p Payload
	if err := json.NewDecoder(r).Decode(&p); err != nil {
		return Payload{}, fmt.Errorf("decode hook payload: %w", err)
	}
	return p, nil
}

// Action says what the adapter must do with a payload.
type Action int

// Actions.
const (
	Ignore Action = iota
	Send          // send Event to the machine
	Begin         // start the session record at idle
	End           // the session is over: drop its record
)

// Map classifies a payload. now is unix seconds.
func Map(p Payload, now int64) (Action, machine.Event) {
	switch p.Event {
	case "SessionStart":
		// A compaction mid-turn restarts the session hooks without ending the
		// turn, so it must not reset the state.
		if p.Source == "compact" {
			return Ignore, nil
		}
		return Begin, nil
	case "SessionEnd":
		return End, nil
	case "UserPromptSubmit":
		return Send, machine.Prompt{At: now}
	case "PreToolUse":
		return Send, machine.ToolStart{At: now}
	case "PostToolUse", "PostToolUseFailure":
		return Send, machine.ToolEnd{At: now, Subagent: p.AgentID != ""}
	case "PermissionRequest":
		if p.ToolName == "AskUserQuestion" {
			return Send, machine.Permission{At: now, Reason: machine.ReasonQuestion}
		}
		return Send, machine.Permission{At: now, Reason: machine.ReasonPermission, Tool: ToolClass(p.ToolName)}
	case "Stop":
		return Send, machine.Stop{At: now, Background: Count(p.BackgroundTasks)}
	case "Notification":
		switch p.NotificationType {
		case "permission_prompt":
			return Send, machine.NeedsInput{At: now, Reason: machine.ReasonPermission}
		case "elicitation_dialog", "elicitation_url_dialog":
			return Send, machine.NeedsInput{At: now, Reason: machine.ReasonElicitation}
		case "agent_needs_input":
			return Send, machine.NeedsInput{At: now, Reason: machine.ReasonInput}
		case "idle_prompt":
			return Send, machine.IdlePrompt{At: now}
		}
	}
	return Ignore, nil
}

// ToolClass is the tool name the deck keeps. MCP tool names embed the server
// name, which can identify internal systems, so they collapse to "mcp".
//
// What is left is cut to the characters a tool name is made of: it is shown,
// logged and handed to tmux, and must not be able to end a command there.
func ToolClass(name string) string {
	if strings.HasPrefix(name, "mcp__") {
		return "mcp"
	}
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			return r
		}
		return -1
	}, name)
	if len(name) > 32 {
		name = name[:32]
	}
	return name
}

// Count is the number of items in a list, object, or number field; 0 when it
// is absent or unreadable.
func Count(raw json.RawMessage) int {
	if n := CountPtr(raw); n != nil {
		return *n
	}
	return 0
}

// CountPtr is Count that tells "absent" (nil) apart from zero. The field's
// shape is undocumented, hence the tolerance for list, object, or number.
func CountPtr(raw json.RawMessage) *int {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		n := len(list)
		return &n
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil {
		n := len(obj)
		return &n
	}
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return &n
	}
	return nil
}
