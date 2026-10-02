# Contributing

Issues and pull requests are welcome. For anything larger than a fix, open an
issue first so we can agree on the shape.

## Ground rules

- **Event-driven.** Nothing may poll or keep a process running between events.
  A hook makes at most one tmux call, and views block in `tmux wait-for`.
- **Never guess a state.** A screen check only acts on an agent's explicit
  markers; when unsure, the state is left alone.
- **Nothing private is kept.** Hooks decode the event, the session id and the
  tool name. Prompts, tool arguments and pane titles are never stored, logged
  or passed to a shell.
- **Only a live agent is typed into.** Anything ytta sends to a pane goes
  through `ifAlive`, which checks inside tmux, in the same call, that the
  agent is still there.
- **Every exported symbol is documented**, and every change comes with a test.
  A bug fix comes with a test that fails without it.
- **Tests never touch your tmux.** Integration tests start private servers
  with `tmux -L ytta-test-*`; keep it that way.

## Compatibility

People put these in their tmux.conf, scripts and status lines, so they only
change with a `BREAKING CHANGE:` footer and a line in the changelog:

| Surface | What is stable |
|---|---|
| tmux options a user sets | every `@ytta-*` name and its values |
| tmux formats a user embeds | `@ytta_window_icon`, `@ytta_pane_icon`, `@ytta_state`, `@ytta_reason`, `@ytta_agent` |
| keys | the two prefix keys and their options; keys inside the views may grow |
| command line | every command and flag in `ytta --help`, the keys of `ytta list --json` and `ytta events --json`, and the arguments of the notify command |
| settings files | ytta only ever adds or removes entries carrying its marker, and uninstall restores the file |

Not stable: the state directory's files, the other `@ytta_*` pane options,
and the hook command line the installer writes (reinstalling refreshes it).
The oldest supported tmux is the oldest one CI runs.

## Adding an agent

An agent is one value in `internal/agent`: how its hook payloads map to
machine events, what its screen proves, and how its process is recognized.
Nothing else in the code knows which agent it is talking to.

1. Record first. `ytta install --<agent> --record --apply` where the agent
   has hooks; the mapping follows what sessions really emit, not only what
   the documentation says.
2. Add the mapping and, only for markers the agent prints itself, a screen
   reader. An agent whose screen proves nothing gets none.
3. Add an install target, a row in the README's agent table, and fixtures.

Codex, Gemini CLI and opencode were added from their documentation and
source before recordings existed. Recordings that confirm or correct them
are the most useful contribution right now.

## Build and test

```sh
make build   # bin/ytta
make test    # unit, fixture replay, integration (needs tmux 3.2+)
make perf    # the 120-pane performance test and the benchmark, run alone
make lint    # golangci-lint v2
```

## Fixtures

Hook sequences under `test/fixtures/` come from real sessions:

1. `ytta install --claude --record --apply`, then use Claude for a while
   (`--codex` and `--gemini` record the same way).
   Traces land in `~/.local/state/ytta/record/`. The recorder keeps
   event names and metadata only, never prompts, paths or titles.
2. Cut a scenario out of a trace:
   `scripts/fixture-from-record.sh <trace.jsonl> <pane> <from> <to> > test/fixtures/<name>.jsonl`
3. `ytta install --claude --apply` to return to the live hooks.
4. `go test ./test/fixtures` scans every fixture for paths, emails, ticket
   keys, session ids and hostnames. Put extra private words (project or
   company names) in `YTTA_LEAK_WORDS`, comma separated.

## Commits and releases

Commit messages and PR titles follow [Conventional Commits](https://www.conventionalcommits.org/):
`feat`, `fix` and `perf` appear in the changelog, other types do not.

Releases are cut by release-please: merging to `main` updates a release PR
with the next version and its changelog. Merging that PR tags the version,
and GoReleaser publishes the binaries that the tpm entrypoint downloads when
Go is missing.
