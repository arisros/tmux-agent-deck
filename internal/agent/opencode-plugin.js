// tmux-agent-deck: reports this opencode's sessions to the deck, the tmux
// plugin that shows which agents need you. Written by `deck install
// --opencode` and removed by `deck uninstall --opencode`; edits are lost.
import { spawn } from "node:child_process"

const DECK = "__DECK__"

// Sessions the task tool started. Their events say nothing about the turn
// the user is waiting on.
const children = new Set()

// One `deck hook` at a time, in the order the events happened: the deck's
// state machine needs them in order, and opencode never waits for it.
let queue = Promise.resolve()

function report(event, sessionID, extra) {
  if (!sessionID || children.has(sessionID)) return
  const payload = JSON.stringify({ hook_event_name: event, session_id: sessionID, ...extra })
  queue = queue.then(
    () =>
      new Promise((done) => {
        try {
          const p = spawn(DECK, ["hook", "--agent", "opencode"], { stdio: ["pipe", "ignore", "ignore"] })
          p.on("error", done)
          p.on("close", done)
          p.stdin.on("error", () => {})
          p.stdin.end(payload)
        } catch {
          done()
        }
      }),
  )
}

export const TmuxAgentDeck = async () => ({
  event: async ({ event }) => {
    const p = event.properties ?? {}
    const id = p.sessionID ?? p.info?.id
    switch (event.type) {
      case "session.created":
        if (p.info?.parentID) children.add(id)
        else report("SessionStart", id, { source: "startup" })
        break
      case "session.deleted":
        if (!children.delete(id)) report("SessionEnd", id)
        break
      case "session.idle":
        report("Stop", id)
        break
      case "session.error":
        report("Interrupt", id)
        break
      case "permission.asked":
      case "permission.updated":
        report("PermissionRequest", id, { tool_name: String(p.permission ?? p.type ?? "") })
        break
      case "question.asked":
        report("PermissionRequest", id, { tool_name: "AskUserQuestion" })
        break
      case "permission.replied":
      case "question.replied":
      case "question.rejected":
        report("PermissionReplied", id)
        break
    }
  },
  "chat.message": async (input) => report("UserPromptSubmit", input?.sessionID),
  "tool.execute.before": async (input) => report("PreToolUse", input?.sessionID, { tool_name: String(input?.tool ?? "") }),
  "tool.execute.after": async (input) => report("PostToolUse", input?.sessionID, { tool_name: String(input?.tool ?? "") }),
})
