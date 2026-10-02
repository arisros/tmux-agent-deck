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
