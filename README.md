# tmux-agent-deck

See which Claude Code agents are blocked, done, or still running across every tmux session, and jump to one.

> **Status:** early preview (`v0.x`). The first phase ships only a hook recorder, used to learn real event sequences before the state machine is built.

## Design goals

- **Event-driven, zero idle cost.** Claude Code hooks push state into tmux pane options. There is no daemon, no ticker, and no polling.
- **Stays inside tmux.** A popup lists agents from all sessions, and a sidebar shows the current session's agents. Borders and window tabs render state with native tmux formats.
- **Correct on real setups.** It is tested against a private tmux server loaded to 120 panes, on macOS and Linux.

The state machine will be built on [fate](https://github.com/arisros/fate).

## Phase 1: record hook sequences

```sh
make build
./bin/deck install --claude --record          # preview the settings change
./bin/deck install --claude --record --apply  # write it, with a backup
```

Each hook event is appended to `~/.local/state/tmux-agent-deck/record/<date>.jsonl`. The recorder keeps the following, and nothing else:

- the event name
- the notification type
- the tool name (MCP tools collapse to `mcp`)
- the session source and end reason
- a hash of the session id
- the tmux pane id
- the payload's top-level field names

Prompts, tool inputs, paths, and titles are never written. The recorder hooks run async, so Claude never waits on them.

To remove the recorder:

```sh
./bin/deck uninstall --claude --apply
```

## Development

```sh
make test    # unit tests, including the fixture leak scanner
make lint    # golangci-lint v2
make build   # bin/deck
```

Files under `test/fixtures/` come from real sessions and are checked by a scanner. The scanner fails the build if they contain a path, an email address, a ticket key, or a prompt or tool-input field.

## License

MIT
