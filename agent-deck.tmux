#!/usr/bin/env bash
# tpm entrypoint. Picks a deck binary, then lets it configure tmux:
#   1. build from this checkout when a new enough Go is available
#   2. otherwise download the matching release binary and verify its checksum
#   3. otherwise keep the binary that is already there
# A failed update never turns the plugin off: tmux-init runs whenever any
# usable binary exists.
set -u

dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
bin="$dir/bin/deck"
stamp="$dir/bin/.rev"
log="$dir/bin/install.log"
mkdir -p "$dir/bin"

say() { tmux display-message "tmux-agent-deck: $*"; }

version=$(git -C "$dir" describe --tags --always --dirty 2>/dev/null || echo unknown)
current=$(cat "$stamp" 2>/dev/null || true)

go_bin() {
	local g
	for g in "$(command -v go 2>/dev/null)" "$HOME/.local/share/mise/shims/go" /opt/homebrew/bin/go \
		/usr/local/go/bin/go /usr/local/bin/go "$HOME/go/bin/go" "$HOME/sdk/go/bin/go" \
		/home/linuxbrew/.linuxbrew/bin/go /snap/bin/go /usr/bin/go; do
		[ -n "$g" ] && [ -x "$g" ] && { echo "$g"; return 0; }
	done
	return 1
}

# go_ok: the Go found is at least the version go.mod asks for.
go_ok() {
	local need have
	need=$(awk '/^go /{print $2; exit}' "$dir/go.mod")
	have=$("$1" env GOVERSION 2>/dev/null | sed 's/^go//')
	[ -n "$have" ] && [ "$(printf '%s\n%s\n' "$need" "$have" | sort -V | head -1)" = "$need" ]
}

build() {
	local g
	g=$(go_bin) || return 1
	go_ok "$g" || { echo "go $("$g" env GOVERSION) is older than go.mod asks" >>"$log"; return 1; }
	(cd "$dir" && "$g" build -trimpath -ldflags "-s -w -X main.version=$version" -o bin/deck.new ./cmd/deck) >>"$log" 2>&1 &&
		mv -f "$dir/bin/deck.new" "$bin"
}

# repo is owner/name from the checkout's remote, so forks download their own.
repo() {
	git -C "$dir" config --get remote.origin.url 2>/dev/null |
		sed -E 's#^(git@github\.com:|https://github\.com/)##; s#\.git$##'
}

fetch() { curl -fsSL --retry 2 "$1" -o "$2" 2>>"$log" || wget -q "$1" -O "$2" 2>>"$log"; }

sha256() {
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	else
		sha256sum "$1" | cut -d' ' -f1
	fi
}

download() {
	local slug tag os arch asset base tmp want got rc
	slug=$(repo)
	[ -n "$slug" ] || slug=arisros/tmux-agent-deck
	tag=$(git -C "$dir" describe --tags --exact-match 2>/dev/null) ||
		tag=$(curl -fsSI "https://github.com/$slug/releases/latest" 2>/dev/null |
			tr -d '\r' | awk -F/ 'tolower($0) ~ /^location:/ {print $NF}')
	[ -n "$tag" ] || { echo "no release found for $slug" >>"$log"; return 1; }
	os=$(uname -s | tr '[:upper:]' '[:lower:]')
	case $(uname -m) in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) echo "no release binary for $(uname -m)" >>"$log"; return 1 ;;
	esac
	asset="tmux-agent-deck_${tag#v}_${os}_${arch}.tar.gz"
	base="https://github.com/$slug/releases/download/$tag"
	tmp=$(mktemp -d)
	if ! fetch "$base/$asset" "$tmp/$asset" || ! fetch "$base/checksums.txt" "$tmp/checksums.txt"; then
		rm -rf "$tmp"
		return 1
	fi
	want=$(awk -v a="$asset" '$2 == a {print $1}' "$tmp/checksums.txt")
	got=$(sha256 "$tmp/$asset")
	if [ -z "$want" ] || [ "$want" != "$got" ]; then
		echo "checksum mismatch for $asset" >>"$log"
		rm -rf "$tmp"
		return 1
	fi
	tar -xzf "$tmp/$asset" -C "$tmp" deck && mv -f "$tmp/deck" "$bin" && chmod +x "$bin"
	rc=$?
	rm -rf "$tmp"
	[ $rc -eq 0 ] && version=$tag
	return $rc
}

if [ ! -x "$bin" ] || [ "$current" != "$version" ]; then
	: >"$log"
	if build || download; then
		echo "$version" >"$stamp"
	elif [ -x "$bin" ]; then
		say "update failed, still running ${current:-the old build} (see $log)"
	else
		say "no binary: install Go $(awk '/^go /{print $2; exit}' "$dir/go.mod") or newer, or check $log"
		exit 0
	fi
fi

"$bin" tmux-init 2>"$dir/bin/init.log" || say "tmux-init failed, see $dir/bin/init.log"
