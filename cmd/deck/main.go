// Command deck is the tmux-agent-deck binary: Claude Code hooks call it, and
// tmux key bindings open its views.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/arisros/tmux-agent-deck/internal/install"
	"github.com/arisros/tmux-agent-deck/internal/record"
)

var version = "dev"

// recordEvents are the hooks the recorder listens to. Every name here must be
// a documented Claude Code event: an unknown one could invalidate the whole
// settings file.
var recordEvents = []string{
	"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure",
	"PermissionRequest", "Notification", "Stop", "SubagentStart", "SubagentStop",
	"PreCompact", "SessionEnd",
}

const usage = `usage: deck <command> [flags]

commands:
  hook --record                          record a redacted hook event read from stdin
  install --claude --record [--apply]    add the recorder hooks to Claude settings (preview by default)
  uninstall --claude [--apply]           remove every deck hook from Claude settings
  version
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "hook":
		err = runHook(args, os.Stdin)
	case "install":
		err = runInstall(args, true)
	case "uninstall":
		err = runInstall(args, false)
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "deck:", err)
		os.Exit(1)
	}
}

// runHook must never disturb Claude: failures are reported on stderr, and the
// exit status stays 0 because a non-zero status is surfaced to the user and,
// for some events, changes Claude's behavior.
func runHook(args []string, stdin io.Reader) error {
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	rec := fs.Bool("record", false, "record a redacted event")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*rec {
		return nil
	}
	e, err := record.FromHook(stdin, os.Getenv("TMUX_PANE"), time.Now())
	if err == nil {
		err = record.Append(record.DefaultDir(), e)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "deck hook:", err)
	}
	return nil
}

func runInstall(args []string, add bool) error {
	name := "uninstall"
	if add {
		name = "install"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	claude := fs.Bool("claude", false, "target Claude Code")
	rec := fs.Bool("record", false, "install the recorder hooks")
	apply := fs.Bool("apply", false, "write the change (default: preview only)")
	path := fs.String("settings", defaultSettingsPath(), "Claude settings file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*claude {
		return errors.New("--claude is required")
	}
	if add && !*rec {
		return errors.New("only --record hooks exist so far")
	}

	before, err := os.ReadFile(*path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var after []byte
	if add {
		bin, err := selfPath()
		if err != nil {
			return err
		}
		after, err = install.Add(before, recordEvents, install.Hook{
			Command: shellQuote(bin) + " hook --record",
			Async:   true,
			Timeout: 5,
		})
		if err != nil {
			return err
		}
	} else if after, err = install.Remove(before); err != nil {
		return err
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
	fmt.Println("Running Claude sessions pick this up on their next settings reload; restart one if it does not.")
	return nil
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
	if !strings.Contains(p, install.Marker) {
		return "", fmt.Errorf("binary path %s must contain %q so uninstall can find its hooks", p, install.Marker)
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
		dir := filepath.Join(filepath.Dir(record.DefaultDir()), "backups")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		f, err := os.CreateTemp(dir, "settings-"+time.Now().Format("20060102T150405")+"-*.json")
		if err != nil {
			return "", err
		}
		backup = f.Name()
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
