package agent

import (
	"github.com/arisros/tmux-agent-deck/internal/hook"
	"github.com/arisros/tmux-agent-deck/internal/machine"
)

// Gemini is Google's Gemini CLI. Its hooks have Claude Code's shape and
// payload fields under other event names. It reports a permission prompt as
// its one notification type and nothing when the prompt is answered, so the
// next model call is what ends the wait.
//
// The mapping comes from Gemini CLI's hook reference, not yet from recorded
// sessions, and the deck reads no Gemini screens.
var Gemini = Agent{
	Name: "gemini",
	Map:  mapGemini,
}

func mapGemini(p hook.Payload, now int64) (hook.Action, machine.Event) {
	switch p.Event {
	case "SessionStart":
		return hook.Begin, nil
	case "SessionEnd":
		return hook.End, nil
	case "BeforeAgent":
		return hook.Send, machine.Prompt{At: now}
	case "AfterAgent":
		return hook.Send, machine.Stop{At: now}
	case "BeforeTool":
		return hook.Send, machine.ToolStart{At: now}
	case "AfterTool", "BeforeModel":
		// Either one means the agent is working again: a tool finished, or
		// the model is asked what to do after a prompt was answered or denied.
		return hook.Send, machine.ToolEnd{At: now}
	case "Notification":
		if p.NotificationType == "ToolPermission" {
			return hook.Send, machine.NeedsInput{At: now, Reason: machine.ReasonPermission}
		}
	}
	return hook.Ignore, nil
}
