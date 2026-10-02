# Security

## Reporting

Report vulnerabilities privately through
[GitHub private vulnerability reporting](https://github.com/arisros/ytta/security/advisories/new).
Please do not open a public issue. Expect an acknowledgement within a week;
this project has a single maintainer.

## Supported versions

Only the latest release receives fixes.

## Scope

Areas worth a close look:

- **Claude settings.** `ytta install` edits `~/.claude/settings.json`. It
  previews by default, writes atomically with the file's mode, keeps backups,
  and only removes commands carrying its own marker.
- **Hook input.** `ytta hook` and `ytta statusline` read JSON that Claude
  sends on stdin; session ids are validated before they become file names.
- **Binary download.** The tpm entrypoint downloads a release archive over
  HTTPS and refuses it unless its SHA-256 matches `checksums.txt` from the
  same release.
- **Shell commands in tmux.** Hooks and key bindings run the ytta binary
  through `run-shell`; the install path must not contain spaces or quotes.
