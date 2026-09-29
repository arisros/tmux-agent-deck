// Command fakeclaude stands in for Claude Code in integration tests. The
// harness builds it under the name "2.1.999", which is how a Claude pane
// looks to tmux: Claude renames its process to its version.
package main

import "time"

func main() { time.Sleep(24 * time.Hour) }
