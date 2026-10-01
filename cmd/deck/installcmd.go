package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/arisros/tmux-agent-deck/internal/agent"
	"github.com/arisros/tmux-agent-deck/internal/install"
	"github.com/arisros/tmux-agent-deck/internal/store"
)

func runInstall(args []string, add bool) error {
	name := "uninstall"
	if add {
		name = "install"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	chosen := map[string]*bool{}
	for _, t := range targets {
		chosen[t.name] = fs.Bool(t.name, false, "target "+t.title)
	}
	rec := fs.Bool("record", false, "install the recorder hooks")
	apply := fs.Bool("apply", false, "write the change (default: preview only)")
	wrap := fs.Bool("wrap-statusline", false, "keep your own statusLine and record usage through it (Claude Code)")
	path := fs.String("settings", "", "the agent's settings or hooks file (default: its usual place)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var t *target
	for i := range targets {
		if *chosen[targets[i].name] {
			if t != nil {
				return errors.New("name one agent at a time")
			}
			t = &targets[i]
		}
	}
	if t == nil {
		return errors.New("name the agent: --claude, --codex, --gemini or --opencode")
	}
	if *path == "" {
		*path = t.path()
	}
	claude := t.name == "claude"

	before, err := os.ReadFile(*path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var after []byte
	if t.plugin != nil {
		return installPlugin(t, *path, before, add, *apply)
	}
	if add {
		bin, err := selfPath()
		if err != nil {
			return err
		}
		live, recorded, note := t.events()
		if note != "" {
			fmt.Println("Note:", note)
		}
		h, events := install.Hook{Command: guarded(bin, "hook"+t.hookArgs), Timeout: t.timeout}, live
		if *rec {
			fmt.Printf("Note: the recorder replaces the live hooks; run install --%s again to return to them.\n", t.name)
			// Recording never feeds state, so it may run async and out of order.
			h, events = install.Hook{Command: guarded(bin, "hook --record"), Async: t.async, Timeout: t.timeout}, recorded
		}
		after, err = install.Add(before, events, h)
		if err != nil {
			return err
		}
		if claude && !*rec {
			var owned bool
			if after, owned, err = install.SetStatusLine(after, guarded(bin, "statusline")); err != nil {
				return err
			}
			if !owned && *wrap {
				if after, owned, err = install.WrapStatusLine(after, func(b64 string) string { return wrapping(bin, b64) }); err != nil {
					return err
				}
			}
			if !owned && !strings.Contains(string(after), install.WrapFlag) {
				fmt.Println("Note: you have your own statusLine, so the deck will not show token usage or plan limits.")
				fmt.Println("      Add --wrap-statusline to keep yours and record them through it.")
			}
		}
	} else {
		if after, err = install.Remove(before); err != nil {
			return err
		}
		if claude {
			if after, err = install.RemoveStatusLine(after); err != nil {
				return err
			}
		}
	}

	if string(before) == string(after) {
		fmt.Println("No change needed:", *path)
		return nil
	}
	if !*apply {
		fmt.Printf("Would update %s\n\n", *path)
		printDiff(before, after)
		fmt.Println("\nPreview only. Repeat with --apply to save.")
		return nil
	}
	backup, err := writeWithBackup(*path, before, after)
	if err != nil {
		return err
	}
	fmt.Println("Updated", *path)
	if backup != "" {
		fmt.Println("Backup:", backup)
	}
	if add {
		fmt.Println(t.afterApply)
	}
	return nil
}

// target is an agent whose hooks the deck can install.
type target struct {
	name, title string
	path        func() string
	// events returns the live and the recorder events, and a note to print.
	events     func() (live, recorded []string, note string)
	hookArgs   string
	async      bool // whether the agent's hooks take "async"
	timeout    int  // in the agent's own unit
	afterApply string
	// plugin, when set, makes the target a single file the deck owns whole
	// instead of hook entries merged into the agent's settings.
	plugin func(bin string) []byte
}

// installPlugin writes or removes a plugin file. The deck owns the whole
// file, and never touches one it did not write.
func installPlugin(t *target, path string, before []byte, add, apply bool) error {
	if before != nil && !strings.Contains(string(before), install.Marker) {
		return fmt.Errorf("%s is not the deck's plugin; move it away first", path)
	}
	var after []byte
	if add {
		bin, err := selfPath()
		if err != nil {
			return err
		}
		after = t.plugin(bin)
	}
	if string(before) == string(after) {
		fmt.Println("No change needed:", path)
		return nil
	}
	if !apply {
		verb := "write"
		if !add {
			verb = "remove"
		}
		fmt.Printf("Would %s %s\n\nPreview only. Repeat with --apply to save.\n", verb, path)
		return nil
	}
	if !add {
		if err := os.Remove(path); err != nil {
			return err
		}
		fmt.Println("Removed", path)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := writeWithBackup(path, before, after); err != nil {
		return err
	}
	fmt.Println("Wrote", path)
	fmt.Println(t.afterApply)
	return nil
}

func geminiSettingsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gemini", "settings.json")
}

func opencodePluginPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "opencode", "plugins", "tmux-agent-deck.js")
}

var geminiEvents = []string{
	"SessionStart", "SessionEnd", "BeforeAgent", "AfterAgent", "BeforeModel", "BeforeTool", "AfterTool", "Notification",
}

var targets = []target{
	{
		name: "claude", title: "Claude Code", path: defaultSettingsPath, async: true, timeout: 5,
		events:     func() ([]string, []string, string) { return liveEvents, recordEvents, "" },
		afterApply: "Running Claude sessions pick this up on their next settings reload; restart one if it does not.",
	},
	{
		name: "codex", title: "Codex CLI", path: codexHooksPath, async: true, timeout: 5, hookArgs: " --agent codex",
		events:     codexEvents,
		afterApply: "Codex runs a new hook only after you trust it: open Codex and run /hooks. A changed command needs trusting again.",
	},
	{
		// Gemini's timeout is in milliseconds.
		name: "gemini", title: "Gemini CLI", path: geminiSettingsPath, timeout: 5000, hookArgs: " --agent gemini",
		events: func() ([]string, []string, string) { return geminiEvents, geminiEvents, "" },
		afterApply: "Restart Gemini CLI to load the hooks. If no agent shows up, its environment redaction is hiding TMUX_PANE from hooks: " +
			"allow that variable in Gemini's settings.",
	},
	{
		name: "opencode", title: "opencode", path: opencodePluginPath,
		plugin: func(bin string) []byte {
			return []byte(strings.ReplaceAll(agent.OpenCodePlugin, "__DECK__", strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(bin)))
		},
		afterApply: "Restart opencode to load the plugin. It reports sessions of an opencode started in a tmux pane, not ones reached with opencode attach.",
	},
}

func codexHooksPath() string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return filepath.Join(dir, "hooks.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex", "hooks.json")
}

// codexEvents leaves out the events the installed Codex does not know: an
// unknown event name could invalidate the hooks file. SessionEnd arrived in
// 0.145 and Interrupt in 0.150.
func codexEvents() (live, recorded []string, note string) {
	live = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PermissionRequest", "PostToolUse", "Stop"}
	v := os.Getenv("DECK_CODEX_VERSION")
	if v == "" {
		out, _ := exec.Command("codex", "--version").Output()
		v = string(out)
	}
	switch {
	case tmuxAtLeast(v, 0, 150):
		live = append(live, "SessionEnd", "Interrupt")
	case tmuxAtLeast(v, 0, 145):
		live = append(live, "SessionEnd")
		note = "this Codex has no Interrupt hook (0.150 adds it): an interrupted turn shows as running until the next prompt"
	case tmuxAtLeast(v, 0, 124):
		note = "this Codex has no SessionEnd or Interrupt hook (0.145 and 0.150 add them): a closed session is forgotten when its pane changes, and an interrupted turn shows as running until the next prompt"
	default:
		note = "no Codex 0.124 or newer found on PATH; installing the events every such version knows. Run this again after upgrading Codex"
	}
	return live, live, note
}

// recordEvents are the hooks the recorder listens to. Every name here must be
// a documented Claude Code event: an unknown one could invalidate the whole
// settings file.
var recordEvents = []string{
	"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure",
	"PermissionRequest", "Notification", "Stop", "SubagentStart", "SubagentStop",
	"PreCompact", "SessionEnd",
}

// liveEvents drive the state machine. They run synchronously: the machine
// needs them in order, and the adapter answers in a few milliseconds.
var liveEvents = []string{
	"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure",
	"PermissionRequest", "Notification", "Stop", "SessionEnd",
}

// guarded keeps Claude quiet if the plugin is removed without uninstalling:
// a missing binary becomes a no-op instead of an error on every event. The
// trailing comment is how uninstall recognizes the deck's commands, wherever
// the binary lives.
func guarded(bin, args string) string {
	q := shellQuote(bin)
	return "test -x " + q + " && " + q + " " + args + "; exit 0 # " + install.Marker
}

// wrapping is the statusLine command that runs the user's own line through
// the deck. Without the binary it runs their line directly, so removing the
// plugin never blanks a status line.
func wrapping(bin, original64 string) string {
	q := shellQuote(bin)
	return "if test -x " + q + "; then " + q + " statusline " + install.WrapFlag + " " + original64 +
		`; else sh -c "$(echo ` + original64 + ` | base64 -d)"; fi # ` + install.Marker
}

func defaultSettingsPath() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "settings.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

// selfPath refuses a `go run` binary: its temp path disappears once the run
// ends, leaving Claude calling a command that no longer exists.
func selfPath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	if p, err = filepath.EvalSymlinks(p); err != nil {
		return "", err
	}
	if strings.Contains(p, "go-build") {
		return "", errors.New("run a built binary (make build), not go run")
	}
	return p, nil
}

func shellQuote(s string) string {
	if !strings.ContainsAny(s, " '\"$`\\") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func printDiff(before, after []byte) {
	dir, err := os.MkdirTemp("", "deck-diff")
	if err == nil {
		defer os.RemoveAll(dir)
		a, b := filepath.Join(dir, "current"), filepath.Join(dir, "proposed")
		if os.WriteFile(a, before, 0o600) == nil && os.WriteFile(b, after, 0o600) == nil {
			out, _ := exec.Command("diff", "-u", a, b).Output()
			if len(out) > 0 {
				os.Stdout.Write(out)
				return
			}
		}
	}
	os.Stdout.Write(after)
}

// writeWithBackup keeps the previous file, then replaces it atomically with
// the same permissions so a crash never leaves Claude a half-written file.
func writeWithBackup(path string, before, after []byte) (string, error) {
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	var backup string
	if before != nil {
		dir := filepath.Join(store.Root(), "backups")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		f, err := os.CreateTemp(dir, "settings-"+time.Now().Format("20060102T150405")+"-*.json")
		if err != nil {
			return "", err
		}
		backup = f.Name()
		defer pruneBackups(dir, 10)
		_, werr := f.Write(before)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return "", werr
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(after); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return "", err
	}
	return backup, os.Rename(tmp.Name(), path)
}

// pruneBackups keeps the newest n settings backups.
func pruneBackups(dir string, n int) {
	files, _ := filepath.Glob(filepath.Join(dir, "settings-*.json"))
	sort.Strings(files) // names start with a timestamp, so this is oldest first
	for len(files) > n {
		_ = os.Remove(files[0])
		files = files[1:]
	}
}
