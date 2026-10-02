package agent

import (
	_ "embed" // the plugin source below

	"github.com/arisros/ytta/internal/hook"
	"github.com/arisros/ytta/internal/machine"
)

// OpenCode is opencode. It has no hook commands: a plugin file inside
// opencode listens to its events and runs `ytta hook` for each, with a
// payload in the shape the other agents send. Unlike them it reports a
// denied permission, and an aborted turn as an error.
//
// The plugin and the mapping come from opencode's plugin API and event
// types, not yet from recorded sessions, and ytta reads no opencode
// screens.
var OpenCode = Agent{
	Name: "opencode",
	Map:  mapOpenCode,
}

// OpenCodePlugin is the plugin's source. __YTTA__ stands for the path of the
// ytta binary, filled in when it is installed.
//
//go:embed opencode-plugin.js
var OpenCodePlugin string

func mapOpenCode(p hook.Payload, now int64) (hook.Action, machine.Event) {
	switch p.Event {
	case "SessionStart":
		return hook.Begin, nil
	case "SessionEnd":
		return hook.End, nil
	case "UserPromptSubmit":
		return hook.Send, machine.Prompt{At: now}
	case "PreToolUse":
		return hook.Send, machine.ToolStart{At: now}
	case "PostToolUse", "PermissionReplied":
		return hook.Send, machine.ToolEnd{At: now}
	case "PermissionRequest":
		if p.ToolName == "AskUserQuestion" {
			return hook.Send, machine.Permission{At: now, Reason: machine.ReasonQuestion}
		}
		return hook.Send, machine.Permission{At: now, Reason: machine.ReasonPermission, Tool: hook.ToolClass(p.ToolName)}
	case "Stop":
		return hook.Send, machine.Stop{At: now}
	case "Interrupt":
		return hook.Send, machine.Interrupt{At: now}
	}
	return hook.Ignore, nil
}
