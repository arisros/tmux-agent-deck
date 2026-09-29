#!/usr/bin/env bash
# tpm entrypoint: rebuild the binary when the checkout changed, then let it
# configure tmux. A prebuilt binary at bin/deck is used as is.
set -u

dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
bin="$dir/bin/deck"
rev=$(git -C "$dir" rev-parse --short HEAD 2>/dev/null || echo unknown)

if [ ! -x "$bin" ] || [ "$(cat "$dir/bin/.rev" 2>/dev/null)" != "$rev" ]; then
	go=$(command -v go || true)
	# tmux may run without a login shell's PATH; try the usual install spots.
	for g in "$HOME/.local/share/mise/shims/go" /opt/homebrew/bin/go /usr/local/go/bin/go /usr/local/bin/go /usr/bin/go; do
		[ -z "$go" ] && [ -x "$g" ] && go=$g
	done
	if [ -z "$go" ]; then
		tmux display-message "tmux-agent-deck: install Go to build the plugin (see README)"
		exit 0
	fi
	mkdir -p "$dir/bin"
	if ! (cd "$dir" && "$go" build -trimpath -ldflags "-s -w -X main.version=$rev" -o bin/deck ./cmd/deck) >"$dir/bin/build.log" 2>&1; then
		tmux display-message "tmux-agent-deck: build failed, see $dir/bin/build.log"
		exit 0
	fi
	echo "$rev" >"$dir/bin/.rev"
fi

"$bin" tmux-init 2>"$dir/bin/init.log" || tmux display-message "tmux-agent-deck: tmux-init failed, see $dir/bin/init.log"
