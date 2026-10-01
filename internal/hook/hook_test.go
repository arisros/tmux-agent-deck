package hook

import (
	"strings"
	"testing"

	"github.com/arisros/tmux-agent-deck/internal/machine"
)

func TestMap(t *testing.T) {
	cases := []struct {
		in     string
		action Action
		want   machine.Event
	}{
		{`{"hook_event_name":"SessionStart","source":"startup"}`, Begin, nil},
		{`{"hook_event_name":"SessionStart","source":"clear"}`, Begin, nil},
		{`{"hook_event_name":"SessionStart","source":"compact"}`, Ignore, nil},
		{`{"hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`, End, nil},
		{`{"hook_event_name":"UserPromptSubmit","prompt":"x"}`, Send, machine.Prompt{At: 7}},
		{`{"hook_event_name":"PreToolUse","tool_name":"Bash"}`, Send, machine.ToolStart{At: 7}},
		{`{"hook_event_name":"PostToolUse"}`, Send, machine.ToolEnd{At: 7}},
		{`{"hook_event_name":"PostToolUseFailure"}`, Send, machine.ToolEnd{At: 7}},
		{`{"hook_event_name":"PostToolUse","agent_id":"a1","agent_type":"Explore"}`, Send, machine.ToolEnd{At: 7, Subagent: true}},
		{`{"hook_event_name":"PermissionRequest","tool_name":"AskUserQuestion"}`, Send, machine.Permission{At: 7, Reason: machine.ReasonQuestion}},
		{`{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`, Send, machine.Permission{At: 7, Reason: machine.ReasonPermission, Tool: "Bash"}},
		{`{"hook_event_name":"PermissionRequest","tool_name":"mcp__internal-crm__lookup"}`, Send, machine.Permission{At: 7, Reason: machine.ReasonPermission, Tool: "mcp"}},
		{`{"hook_event_name":"PermissionRequest"}`, Send, machine.Permission{At: 7, Reason: machine.ReasonPermission}},
		{`{"hook_event_name":"Notification","notification_type":"permission_prompt"}`, Send, machine.NeedsInput{At: 7, Reason: machine.ReasonPermission}},
		{`{"hook_event_name":"Notification","notification_type":"elicitation_dialog"}`, Send, machine.NeedsInput{At: 7, Reason: machine.ReasonElicitation}},
		{`{"hook_event_name":"Notification","notification_type":"elicitation_url_dialog"}`, Send, machine.NeedsInput{At: 7, Reason: machine.ReasonElicitation}},
		{`{"hook_event_name":"Notification","notification_type":"agent_needs_input"}`, Send, machine.NeedsInput{At: 7, Reason: machine.ReasonInput}},
		{`{"hook_event_name":"Notification","notification_type":"idle_prompt"}`, Send, machine.IdlePrompt{At: 7}},
		{`{"hook_event_name":"Notification","notification_type":"auth_success"}`, Ignore, nil},
		{`{"hook_event_name":"Stop","background_tasks":[{"id":"x"}]}`, Send, machine.Stop{At: 7, Background: 1}},
		{`{"hook_event_name":"Stop","background_tasks":[]}`, Send, machine.Stop{At: 7}},
		{`{"hook_event_name":"SubagentStop","agent_type":"Explore"}`, Ignore, nil},
		{`{"hook_event_name":"SubagentStart"}`, Ignore, nil},
		{`{"hook_event_name":"PreCompact"}`, Ignore, nil},
	}
	for _, c := range cases {
		p, err := Decode(strings.NewReader(c.in))
		if err != nil {
			t.Fatal(err)
		}
		action, ev := Map(p, 7)
		if action != c.action || ev != c.want {
			t.Errorf("%s: got %v %#v, want %v %#v", c.in, action, ev, c.action, c.want)
		}
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	if _, err := Decode(strings.NewReader("{")); err == nil {
		t.Fatal("want error")
	}
}

func TestCount(t *testing.T) {
	for in, want := range map[string]int{`[1,2]`: 2, `{"a":1}`: 1, `3`: 3, `null`: 0, ``: 0, `"x"`: 0} {
		if got := Count([]byte(in)); got != want {
			t.Errorf("Count(%s) = %d, want %d", in, got, want)
		}
	}
}
