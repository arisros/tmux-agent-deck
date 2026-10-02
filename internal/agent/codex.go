package agent

import (
	"strings"

	"github.com/arisros/ytta/internal/hook"
	"github.com/arisros/ytta/internal/machine"
)

// Codex is OpenAI's Codex CLI. Its hooks carry the same payload fields as
// Claude Code's and mostly the same event names. It differs in three ways:
// an Interrupt event reports Esc (from 0.150), there are no notifications,
// and a Stop says nothing about background work.
//
// The mapping and the screen markers below come from Codex's published hook
// schema and its TUI source, not yet from recorded sessions.
var Codex = Agent{
	Name:     "codex",
	Map:      mapCodex,
	Classify: classifyCodex,
}

func mapCodex(p hook.Payload, now int64) (hook.Action, machine.Event) {
	// A subagent's events say nothing about the turn the user is waiting on.
	if p.AgentID != "" {
		return hook.Ignore, nil
	}
	switch p.Event {
	case "SessionStart":
		if p.Source == "compact" {
			return hook.Ignore, nil
		}
		return hook.Begin, nil
	case "SessionEnd":
		return hook.End, nil
	case "UserPromptSubmit":
		return hook.Send, machine.Prompt{At: now}
	case "PreToolUse":
		return hook.Send, machine.ToolStart{At: now}
	case "PostToolUse":
		return hook.Send, machine.ToolEnd{At: now}
	case "PermissionRequest":
		return hook.Send, machine.Permission{At: now, Reason: machine.ReasonPermission, Tool: hook.ToolClass(p.ToolName)}
	case "Stop":
		return hook.Send, machine.Stop{At: now}
	case "Interrupt":
		return hook.Send, machine.Interrupt{At: now}
	}
	return hook.Ignore, nil
}

// classifyCodex only concludes from Codex's own words. It never says idle:
// Codex prints no end-of-turn marker ytta has seen, so an ended turn is
// left to the Stop and Interrupt hooks.
func classifyCodex(screen string) string {
	lines := strings.Split(strings.TrimRight(screen, "\n "), "\n")
	var tail []string
	for i := len(lines) - 1; i >= 0 && len(tail) < 30; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			tail = append(tail, lines[i])
		}
	}
	has := func(words ...string) bool {
		for _, l := range tail {
			for _, w := range words {
				if strings.Contains(l, w) {
					return true
				}
			}
		}
		return false
	}
	switch {
	case has("Would you like to run the following command?", "Would you like to make the following edits?",
		"Would you like to grant these permissions?", "Do you want to approve network access", "Yes, proceed"):
		return machine.ScreenDialog
	case has("esc to interrupt", "background terminal running"):
		return machine.ScreenWorking
	case has("context left", "for shortcuts"):
		return machine.ScreenNoDialog
	}
	return ""
}
