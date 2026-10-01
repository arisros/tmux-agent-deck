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

function run(args, payload) {
  queue = queue.then(
    () =>
      new Promise((done) => {
        try {
          const p = spawn(DECK, args, { stdio: ["pipe", "ignore", "ignore"] })
          p.on("error", done)
          p.on("close", done)
          p.stdin.on("error", () => {})
          p.stdin.end(JSON.stringify(payload))
        } catch {
          done()
        }
      }),
  )
}

function report(event, sessionID, extra) {
  if (!sessionID || children.has(sessionID)) return
  run(["hook", "--agent", "opencode"], { hook_event_name: event, session_id: sessionID, ...extra })
}

// What each assistant message cost, by session and message. opencode updates
// a message many times while it streams, so the latest figures replace the
// earlier ones. Only messages seen since opencode started are counted.
const spent = new Map()

function note(info) {
  if (info?.role !== "assistant" || !info.sessionID || !info.id || children.has(info.sessionID)) return
  if (!spent.has(info.sessionID)) spent.set(info.sessionID, new Map())
  const t = info.tokens ?? {}
  spent.get(info.sessionID).set(info.id, {
    cost: Number(info.cost) || 0,
    input: (Number(t.input) || 0) + (Number(t.cache?.read) || 0) + (Number(t.cache?.write) || 0),
    output: (Number(t.output) || 0) + (Number(t.reasoning) || 0),
    model: String(info.modelID ?? ""),
  })
}

// Usage is reported once per turn, just before the turn's end, so the view
// that redraws on that end already has the numbers.
function usage(sessionID) {
  const messages = spent.get(sessionID)
  if (!messages || children.has(sessionID)) return
  const total = { session_id: sessionID, model: "", cost_usd: 0, input_tokens: 0, output_tokens: 0 }
  for (const m of messages.values()) {
    total.cost_usd += m.cost
    total.input_tokens += m.input
    total.output_tokens += m.output
    if (m.model) total.model = m.model
  }
  run(["usage"], total)
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
        spent.delete(id)
        if (!children.delete(id)) report("SessionEnd", id)
        break
      case "message.updated":
        note(p.info)
        break
      case "session.idle":
        usage(id)
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
