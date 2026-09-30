# Changelog

## 0.1.0 (2026-09-30)

First release.

### Added

* **Agent states** for every Claude Code session in tmux: waiting, done, running and idle, driven by Claude Code hooks through a [fate](https://github.com/arisros/fate) state machine. Tool calls inside a turn cost no tmux call.
* **Popup** (`prefix a`): agents of every session, most urgent first; jump, filter, and kill with confirmation.
* **Sidebar** (`prefix e`): one per session. It follows you across windows, returns to the left column after swaps and layout changes, scrolls with keys and the wheel, and restores its width when squeezed.
* **Tab and border icons** rendered by tmux formats, with an optional pulse for running agents (`@deck-tab-pulse`).
* **Sounds** when an agent starts waiting or finishes, only for panes you are not watching.
* **Repairs for silent endings**: Esc and denied permissions fire no hook, so the state is corrected from Claude's own screen markers when you leave the pane. Agents already running when the plugin loads are discovered the same way.
* **Usage and plan limits** from Claude's statusLine: context used, tokens and cost per agent, and the 5-hour and 7-day windows.
* **`deck install --claude`** previews, then applies, the hooks and statusLine with a backup; **`deck doctor`** checks the setup.
* **Release binaries** for macOS and Linux (amd64, arm64). The tpm entrypoint builds with Go when it can, otherwise downloads a binary and verifies its checksum.
