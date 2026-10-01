package agent

import (
	"strings"
	"testing"

	"github.com/arisros/tmux-agent-deck/internal/hook"
	"github.com/arisros/tmux-agent-deck/internal/machine"
)

func TestFor(t *testing.T) {
	for name, want := range map[string]string{"": "claude", "claude": "claude", "codex": "codex"} {
		if a, ok := For(name); !ok || a.Name != want {
			t.Errorf("For(%q) = %q %v, want %q", name, a.Name, ok, want)
		}
	}
	if _, ok := For("nope"); ok {
		t.Error("an unknown agent was accepted")
	}
}

func TestCodexMap(t *testing.T) {
	cases := []struct {
		in     string
		action hook.Action
		want   machine.Event
	}{
		{`{"hook_event_name":"SessionStart","source":"startup"}`, hook.Begin, nil},
		{`{"hook_event_name":"SessionStart","source":"resume"}`, hook.Begin, nil},
		{`{"hook_event_name":"SessionStart","source":"compact"}`, hook.Ignore, nil},
		{`{"hook_event_name":"SessionEnd","reason":"other"}`, hook.End, nil},
		{`{"hook_event_name":"UserPromptSubmit","prompt":"x"}`, hook.Send, machine.Prompt{At: 7}},
		{`{"hook_event_name":"PreToolUse","tool_name":"Bash"}`, hook.Send, machine.ToolStart{At: 7}},
		{`{"hook_event_name":"PostToolUse","tool_name":"Bash"}`, hook.Send, machine.ToolEnd{At: 7}},
		{`{"hook_event_name":"PermissionRequest","tool_name":"apply_patch"}`, hook.Send, machine.Permission{At: 7, Reason: machine.ReasonPermission, Tool: "apply_patch"}},
		{`{"hook_event_name":"Stop"}`, hook.Send, machine.Stop{At: 7}},
		{`{"hook_event_name":"Interrupt"}`, hook.Send, machine.Interrupt{At: 7}},
		// A subagent's events never move the main turn.
		{`{"hook_event_name":"Stop","agent_id":"a1"}`, hook.Ignore, nil},
		{`{"hook_event_name":"PostToolUse","agent_id":"a1"}`, hook.Ignore, nil},
		{`{"hook_event_name":"SubagentStop"}`, hook.Ignore, nil},
		{`{"hook_event_name":"PreCompact"}`, hook.Ignore, nil},
		// Codex has no notifications; one from elsewhere means nothing here.
		{`{"hook_event_name":"Notification","notification_type":"idle_prompt"}`, hook.Ignore, nil},
	}
	for _, c := range cases {
		p, err := hook.Decode(strings.NewReader(c.in))
		if err != nil {
			t.Fatal(err)
		}
		action, ev := Codex.Map(p, 7)
		if action != c.action || ev != c.want {
			t.Errorf("%s: got %v %#v, want %v %#v", c.in, action, ev, c.action, c.want)
		}
	}
}

// Screens assembled from the strings in Codex's TUI source, not captured
// from a session.
func TestCodexClassify(t *testing.T) {
	cases := map[string]string{
		"• Working (12s • esc to interrupt)\n\n› Ask Codex to do anything\n  ? for shortcuts   100% context left\n":                                     machine.ScreenWorking,
		"  1 background terminal running · /ps to view · /stop to close\n› \n  98% context left\n":                                                      machine.ScreenWorking,
		"Would you like to run the following command?\n  $ rm -rf build\n› 1. Yes, proceed (y)\n  2. No, and tell Codex what to do differently (esc)\n": machine.ScreenDialog,
		"Would you like to make the following edits?\n› 1. Yes, proceed (y)\n":                                                                          machine.ScreenDialog,
		"Do you want to approve network access to \"example.com\"?\n":                                                                                   machine.ScreenDialog,
		"• Ran tests\n\n› Ask Codex to do anything\n  ? for shortcuts   81% context left\n":                                                             machine.ScreenNoDialog,
		"$ ls\nfoo\n": "",
		"":            "",
	}
	for screen, want := range cases {
		if got := Codex.Classify(screen); got != want {
			t.Errorf("classifyCodex(%q) = %q, want %q", screen, got, want)
		}
	}
	for screen := range cases {
		if Codex.Classify(screen) == machine.ScreenIdle {
			t.Errorf("a Codex screen was read as idle, which nothing on it can prove: %q", screen)
		}
	}
}

func TestScreenWithoutAClassifier(t *testing.T) {
	if got := (Agent{Name: "quiet"}).Screen("anything"); got != "" {
		t.Errorf("Screen = %q", got)
	}
}
