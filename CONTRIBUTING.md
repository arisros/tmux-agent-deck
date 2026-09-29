# Contributing

Issues and pull requests are welcome. For anything larger than a fix, open an
issue first so we can agree on the shape.

## Ground rules

- **Event-driven.** Nothing may poll or keep a process running between events.
  A hook makes at most one tmux call, and views block in `tmux wait-for`.
- **Never guess a state.** A screen check only acts on Claude's explicit
  markers; when unsure, the state is left alone.
- **Every exported symbol is documented**, and every change comes with a test.
  A bug fix comes with a test that fails without it.
- **Tests never touch your tmux.** Integration tests start private servers
  with `tmux -L deck-test-*`; keep it that way.

## Build and test

```sh
make build   # bin/deck
make test    # unit, fixture replay, integration (needs tmux 3.3+)
make perf    # the 120-pane performance test and the benchmark, run alone
make lint    # golangci-lint v2
```

## Fixtures

Hook sequences under `test/fixtures/` come from real sessions:

1. `deck install --claude --record --apply`, then use Claude for a while.
   Traces land in `~/.local/state/tmux-agent-deck/record/`. The recorder keeps
   event names and metadata only, never prompts, paths or titles.
2. Cut a scenario out of a trace:
   `scripts/fixture-from-record.sh <trace.jsonl> <pane> <from> <to> > test/fixtures/<name>.jsonl`
3. `deck install --claude --apply` to return to the live hooks.
4. `go test ./test/fixtures` scans every fixture for paths, emails, ticket
   keys, session ids and hostnames. Put extra private words (project or
   company names) in `DECK_LEAK_WORDS`, comma separated.

## Commits and releases

Commit messages and PR titles follow [Conventional Commits](https://www.conventionalcommits.org/):
`feat`, `fix` and `perf` appear in the changelog, other types do not.

Releases are cut by release-please: merging to `main` updates a release PR
with the next version and its changelog. Merging that PR tags the version,
and GoReleaser publishes the binaries that the tpm entrypoint downloads when
Go is missing.
