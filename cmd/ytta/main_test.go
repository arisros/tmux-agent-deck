package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUsageListsEveryCommand(t *testing.T) {
	for name := range commands {
		if !strings.Contains(usageText, "\n  "+name) {
			t.Errorf("usage text does not list %q", name)
		}
	}
	for _, sub := range []string{"sidebar toggle", "sidebar run", "sidebar pin"} {
		if !strings.Contains(usageText, sub) {
			t.Errorf("usage text does not list %q", sub)
		}
	}
	if strings.Contains(usageText, "sidebar follow") || strings.Contains(usageText, "--client") {
		t.Error("usage text lists a removed command or flag")
	}
}

func TestRunExitCodes(t *testing.T) {
	var out, errb bytes.Buffer
	cases := []struct {
		args []string
		code int
	}{
		{nil, 2},
		{[]string{"-h"}, 0},
		{[]string{"--help"}, 0},
		{[]string{"--version"}, 0},
		{[]string{"nope"}, 2},
		{[]string{"popup", "--client", "x"}, 1},
	}
	for _, c := range cases {
		out.Reset()
		errb.Reset()
		if got := run(c.args, &out, &errb); got != c.code {
			t.Errorf("run(%v) = %d, want %d (stderr %q)", c.args, got, c.code, errb.String())
		}
	}
}

func TestBuildVersionPrefersTheStamp(t *testing.T) {
	defer func(v string) { version = v }(version)
	version = "v0.1.0"
	if got := buildVersion(); got != "v0.1.0" {
		t.Errorf("buildVersion() = %q", got)
	}
	version = ""
	if got := buildVersion(); got == "" {
		t.Error("buildVersion() is empty without a stamp")
	}
}

func TestTmuxAtLeast(t *testing.T) {
	for v, want := range map[string]bool{
		"tmux 3.5a": true, "tmux 3.3": true, "tmux 3.2a": false, "tmux next-3.6": true,
		"tmux 4.0": true, "tmux 2.9": false, "": false, "tmux master": false,
	} {
		if got := tmuxAtLeast(v, 3, 3); got != want {
			t.Errorf("tmuxAtLeast(%q, 3, 3) = %v, want %v", v, got, want)
		}
	}
}

func TestGuardedCommandCarriesTheMarker(t *testing.T) {
	got := guarded("/opt/my tools/ytta", "hook")
	want := `test -x '/opt/my tools/ytta' && '/opt/my tools/ytta' hook; exit 0 # ytta`
	if got != want {
		t.Errorf("guarded = %q\nwant      %q", got, want)
	}
	if got := shellQuote("/usr/bin/ytta"); got != "/usr/bin/ytta" {
		t.Errorf("plain path quoted: %q", got)
	}
	if got := shellQuote("it's"); got != `'it'\''s'` {
		t.Errorf("quote = %q", got)
	}
}

func TestWriteWithBackupKeepsModeAndPrunes(t *testing.T) {
	t.Setenv("YTTA_STATE_DIR", filepath.Join(t.TempDir(), "sessions"))
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{}"), 0o640); err != nil {
		t.Fatal(err)
	}
	var backups []string
	for i := 0; i < 12; i++ {
		b, err := writeWithBackup(path, []byte("{}"), []byte(`{"n":1}`))
		if err != nil {
			t.Fatal(err)
		}
		backups = append(backups, b)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640", fi.Mode().Perm())
	}
	seen := map[string]bool{}
	for _, b := range backups {
		if seen[b] {
			t.Fatalf("backup name reused: %s", b)
		}
		seen[b] = true
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(backups[0]), "settings-*.json"))
	if len(left) != 10 {
		t.Errorf("%d backups kept, want 10", len(left))
	}
}

func TestGuardedWritesHomeRelativePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for bin, want := range map[string]string{
		home + "/dotfiles/ytta/bin/ytta": `test -x "$HOME"/dotfiles/ytta/bin/ytta && "$HOME"/dotfiles/ytta/bin/ytta hook; exit 0 # ytta`,
		home + "/my tools/ytta":          `test -x "$HOME"'/my tools/ytta' && "$HOME"'/my tools/ytta' hook; exit 0 # ytta`,
		"/opt/ytta":                      `test -x /opt/ytta && /opt/ytta hook; exit 0 # ytta`,
	} {
		got := guarded(bin, "hook")
		if got != want {
			t.Errorf("guarded(%q) = %q\nwant %q", bin, got, want)
		}
		if back := guardedBinary(got); back != bin {
			t.Errorf("guardedBinary(%q) = %q, want %q", got, back, bin)
		}
	}
	for _, bin := range []string{"/opt/it's/ytta", home + "/a b'c/ytta"} {
		if back := guardedBinary(wrapping(bin, "ZWNobw==")); back != bin {
			t.Errorf("guardedBinary(wrapping(%q)) = %q", bin, back)
		}
	}
	if got := guardedBinary("rtk hook claude"); got != "" {
		t.Errorf("unguarded command gave %q", got)
	}
}

func TestGoneBinariesFindsAMovedPlugin(t *testing.T) {
	dir := t.TempDir()
	here := filepath.Join(dir, "ytta")
	if err := os.WriteFile(here, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(dir, "old", "ytta")
	settings := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"` + guarded(here, "hook") + `"}]}],` +
		`"SessionStart":[{"hooks":[{"type":"command","command":"` + guarded(moved, "hook") + `"}]}]},` +
		`"statusLine":{"type":"command","command":"` + guarded(moved, "statusline") + `"}}`
	got := goneBinaries([]byte(settings))
	if len(got) != 1 || got[0] != moved {
		t.Errorf("goneBinaries = %q, want [%q]", got, moved)
	}
}

func TestWriteWithBackupKeepsASymlink(t *testing.T) {
	t.Setenv("YTTA_STATE_DIR", filepath.Join(t.TempDir(), "sessions"))
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "settings.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := writeWithBackup(link, []byte("{}"), []byte(`{"n":1}`)); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("settings.json is no longer a symlink (err %v)", err)
	}
	if b, _ := os.ReadFile(target); string(b) != `{"n":1}` {
		t.Errorf("target = %q, want the new content", b)
	}
}
