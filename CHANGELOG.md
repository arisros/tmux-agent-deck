# Changelog

## [0.3.1](https://github.com/arisros/ytta/compare/v0.3.0...v0.3.1) (2026-10-03)


### Fixed

* **install:** keep symlinked settings and survive a moved plugin ([#15](https://github.com/arisros/ytta/issues/15)) ([6facdba](https://github.com/arisros/ytta/commit/6facdba73ce50f6062956b47938185a35414eca5))
* **screen:** follow agents without a session and see narrow dialogs ([#16](https://github.com/arisros/ytta/issues/16)) ([11b4f13](https://github.com/arisros/ytta/commit/11b4f13eed26b1a9447dd7fbb4f12ed25e602267))

## [0.3.0](https://github.com/arisros/ytta/compare/v0.2.0...v0.3.0) (2026-10-02)


### ⚠ BREAKING CHANGES

* the binary is `ytta` and the plugin is `arisros/ytta`. Options and formats are `@ytta-*` and `@ytta_*`, environment variables are `YTTA_*`, state lives in `~/.local/state/ytta`, hook entries carry the marker `# ytta`, the opencode plugin is `ytta.js`, and release archives are `ytta_<version>_<os>_<arch>.tar.gz`. Uninstall with the old `deck` binary before switching; the README has the steps.

### Changed

* rename tmux-agent-deck to ytta ([#12](https://github.com/arisros/ytta/issues/12)) ([48e218a](https://github.com/arisros/ytta/commit/48e218a02c3c1c2f2b1d10efe50ce6be9747af79))

## [0.2.0](https://github.com/arisros/tmux-agent-deck/compare/v0.1.3...v0.2.0) (2026-10-01)


### ⚠ BREAKING CHANGES

* deck list --json keys are now all snake_case (pane, target, state, name, path) and gain reason, agent, branch, session_id and usage.

### Added

* say why agents wait, answer them, and track codex, gemini, opencode ([#10](https://github.com/arisros/tmux-agent-deck/issues/10)) ([5ea3d64](https://github.com/arisros/tmux-agent-deck/commit/5ea3d64089ec12869dedd08c8f97022b56ff5e96))

## [0.1.3](https://github.com/arisros/tmux-agent-deck/compare/v0.1.2...v0.1.3) (2026-10-01)


### Fixed

* **sidebar:** defer a pin that comes too soon instead of dropping it ([a53c637](https://github.com/arisros/tmux-agent-deck/commit/a53c6374862fefa7c5313734115c7f98614c5bb3))

## [0.1.2](https://github.com/arisros/tmux-agent-deck/compare/v0.1.1...v0.1.2) (2026-10-01)


### Fixed

* **sidebar:** restore window layouts when the sidebar follows ([#6](https://github.com/arisros/tmux-agent-deck/issues/6)) ([eedc014](https://github.com/arisros/tmux-agent-deck/commit/eedc014aa49a70760eb968e1baf2024bccf26467))

## [0.1.1](https://github.com/arisros/tmux-agent-deck/compare/v0.1.0...v0.1.1) (2026-09-30)


### Fixed

* **tpm:** find a new enough go and parse tpm's clone url ([#4](https://github.com/arisros/tmux-agent-deck/issues/4)) ([027bb44](https://github.com/arisros/tmux-agent-deck/commit/027bb4468cf961de09ef9238b101677c6aab3aa2))

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
