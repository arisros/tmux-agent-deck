# Changelog

## 0.1.0 (2026-09-30)


### Added

* add the agent state machine, hook adapter and session store ([279b7b9](https://github.com/arisros/tmux-agent-deck/commit/279b7b99c32c1779746ea2b571014a5e31883b39))
* add the popup, the per-session sidebar and the tpm entrypoint ([d60ffa4](https://github.com/arisros/tmux-agent-deck/commit/d60ffa482a3e50992ddecb82aa4172914b47b561))
* bootstrap repo with a redacting claude hook recorder ([b72cab2](https://github.com/arisros/tmux-agent-deck/commit/b72cab226a97b91940e591a377553894a6450a15))
* discover idle agents, kill from the views, mark the current pane ([e543ede](https://github.com/arisros/tmux-agent-deck/commit/e543edefcfa048900d29049598b08c7f5bb17b79))
* pulse running agents green, show waiting as a red badge ([c9b2cc5](https://github.com/arisros/tmux-agent-deck/commit/c9b2cc5a82a65b448230e26924bebdd6b763da93))
* pulse the running dot by glyph, opt-in tab pulse via deck tick ([2d8ad15](https://github.com/arisros/tmux-agent-deck/commit/2d8ad15433a18329bb3cd65ef1350c5b3d8b4d2d))
* **record:** count background tasks and session crons on each event ([2da4149](https://github.com/arisros/tmux-agent-deck/commit/2da4149d12fb5531725b361a6a4b3c62cf7247d1))
* show token usage, cost and plan limits from claude's statusLine ([8b00620](https://github.com/arisros/tmux-agent-deck/commit/8b006202ca44409bd45ed8da5fa2adede91f899f))
* **sidebar:** leave a window once it is the only pane left ([fe2b4ab](https://github.com/arisros/tmux-agent-deck/commit/fe2b4aba9de08b46364763d0f9038f41af73f203))
* **sidebar:** return to the left column after swaps and layout changes ([aa15f9a](https://github.com/arisros/tmux-agent-deck/commit/aa15f9a6bb6d8592431409c4d75c3481e3036c20))
* **sidebar:** scroll with the cursor and the mouse wheel ([72e5e9d](https://github.com/arisros/tmux-agent-deck/commit/72e5e9db45b1531f59d9bd34055904f2a91bc3e3))
* **tpm:** DECK_BUILD=off and DECK_RELEASE_URL for the binary download ([5b26d2e](https://github.com/arisros/tmux-agent-deck/commit/5b26d2e2f78229843ef6f8c4465e8ad11e329e60))


### Fixed

* audit findings before v0.1.0 ([88068e5](https://github.com/arisros/tmux-agent-deck/commit/88068e53c4cad195854df218371fd53b0d9c04fb))
* never read a busy agent as idle while the user types ([2a921e4](https://github.com/arisros/tmux-agent-deck/commit/2a921e45722539f77307a8e6adc7b9034770e753))
* only read idle from claude's own end markers, match usage by pane ([77f6393](https://github.com/arisros/tmux-agent-deck/commit/77f639381dfb2b512922423bfb8ae32ae4227979))
* popup binding passes no formats; clearer, wider sidebar ([3b7e7de](https://github.com/arisros/tmux-agent-deck/commit/3b7e7def456a984c17a37711ac1fb30eb91903ef))
* sidebar follows back and forth, narrow panes never read as idle ([d94d6b0](https://github.com/arisros/tmux-agent-deck/commit/d94d6b0a4898d1faf3d9348bed64ba4d386c2df9))
* **sidebar:** drop the ctx label, never pulse with idle's glyph ([3e6d50b](https://github.com/arisros/tmux-agent-deck/commit/3e6d50bfc6456d29506829f85c4ed297149d04f2))
* **sidebar:** keep focus on the user's pane, read the spinner as work ([d2feb39](https://github.com/arisros/tmux-agent-deck/commit/d2feb3910cd4697d2cf9e5313c2458a4e5428edb))
* **sidebar:** stop the pin loop that froze tmux with pane borders on ([9536fab](https://github.com/arisros/tmux-agent-deck/commit/9536fab2093917b70ff6a59f95663cbc10e3334d))
* **ui:** ignore a stray esc as a view opens, log why a view closes ([3de61a7](https://github.com/arisros/tmux-agent-deck/commit/3de61a7f08050194a49aedf096cce8f71d80a5ec))
* **ui:** log a view that ends in an error or a panic ([f8ce28e](https://github.com/arisros/tmux-agent-deck/commit/f8ce28e5d3af99520f414288e8a84ff332251bcc))
* v0.1.0 audit fixes, readme, and release pipeline ([299f49b](https://github.com/arisros/tmux-agent-deck/commit/299f49bdc9588d9254e240095c17884c490bafc0))

## Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/). While in v0.x, a minor release may
change behavior; such changes are called out.

Entries are written by release-please from Conventional Commit messages.
