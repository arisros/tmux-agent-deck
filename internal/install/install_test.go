package install

import (
	"encoding/json"
	"strings"
	"testing"
)

// Shaped like a real ~/.claude/settings.json: unrelated keys around hooks,
// foreign hooks with "&&" and matchers, and key order that is not sorted.
const settings = `{
  "model": "opus",
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "rtk hook claude"
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "afplay Funk.aiff; [ -n \"$TMUX\" ] && tmux refresh-client -S",
            "async": true
          }
        ]
      }
    ]
  },
  "attribution": {
    "commit": ""
  }
}
`

var ytta = Hook{Command: "/Users/x/ytta/bin/ytta hook --record", Async: true, Timeout: 5}

func TestAddThenRemoveRoundTrips(t *testing.T) {
	added, err := Add([]byte(settings), []string{"Stop", "SessionEnd"}, ytta)
	if err != nil {
		t.Fatal(err)
	}
	removed, err := Remove(added)
	if err != nil {
		t.Fatal(err)
	}
	if string(removed) != settings {
		t.Errorf("round trip changed the file:\n%s", removed)
	}
}

func TestAddKeepsForeignHooksAndOrder(t *testing.T) {
	out, err := Add([]byte(settings), []string{"PreToolUse", "SessionEnd"}, ytta)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "\\u0026") || !strings.Contains(s, "&&") {
		t.Error("existing && was escaped")
	}
	model, hooks, attr := strings.Index(s, `"model"`), strings.Index(s, `"hooks"`), strings.Index(s, `"attribution"`)
	if model > hooks || hooks > attr {
		t.Error("top-level key order changed")
	}
	var parsed struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
				Async   bool   `json:"async"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	pre := parsed.Hooks["PreToolUse"]
	if len(pre) != 2 || pre[0].Matcher != "Bash" || pre[0].Hooks[0].Command != "rtk hook claude" {
		t.Errorf("foreign PreToolUse group disturbed: %+v", pre)
	}
	if got := pre[1].Hooks[0]; got.Command != ytta.Command || !got.Async || got.Timeout != 5 {
		t.Errorf("ytta entry = %+v", got)
	}
	if len(parsed.Hooks["SessionEnd"]) != 1 {
		t.Errorf("SessionEnd not added: %+v", parsed.Hooks["SessionEnd"])
	}
}

func TestAddIsIdempotent(t *testing.T) {
	once, err := Add([]byte(settings), []string{"Stop"}, ytta)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := Add(once, []string{"Stop"}, ytta)
	if err != nil {
		t.Fatal(err)
	}
	if string(once) != string(twice) {
		t.Errorf("second Add changed the file:\n%s", twice)
	}
}

func TestRemoveOnlyTouchesYttaEntriesInSharedGroup(t *testing.T) {
	shared := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"afplay x"},{"type":"command","command":"/p/ytta/bin/ytta hook"}]}]}}`
	out, err := Remove([]byte(shared))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), Marker) || !strings.Contains(string(out), "afplay x") {
		t.Errorf("unexpected result:\n%s", out)
	}
}

func TestAddToEmptyOrHooklessFile(t *testing.T) {
	for _, in := range []string{"", "{}", `{"model":"opus"}`} {
		out, err := Add([]byte(in), []string{"Stop"}, ytta)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		events, err := Owned(out)
		if err != nil || len(events) != 1 || events[0] != "Stop" {
			t.Errorf("%q: owned = %v, %v", in, events, err)
		}
	}
}

func TestAddRejectsUnmarkedCommand(t *testing.T) {
	if _, err := Add([]byte("{}"), []string{"Stop"}, Hook{Command: "/usr/bin/true"}); err == nil {
		t.Fatal("want error for a command Remove could never find again")
	}
}

func TestRejectsNonObject(t *testing.T) {
	if _, err := Remove([]byte("[]")); err == nil {
		t.Fatal("want error")
	}
}

func TestStatusLineTakesOnlyAFreeSlot(t *testing.T) {
	cmd := "/p/ytta/bin/ytta statusline"
	out, owned, err := SetStatusLine([]byte(settings), cmd)
	if err != nil || !owned || !strings.Contains(string(out), cmd) {
		t.Fatalf("free slot not taken: %v %v\n%s", owned, err, out)
	}
	back, err := RemoveStatusLine(out)
	if err != nil || string(back) != settings {
		t.Errorf("remove did not restore the file:\n%s", back)
	}
	mine := `{"statusLine":{"type":"command","command":"~/bin/my-line"}}`
	out, owned, _ = SetStatusLine([]byte(mine), cmd)
	if owned || strings.Contains(string(out), "ytta") {
		t.Errorf("replaced the user's own status line:\n%s", out)
	}
	if out, _ := RemoveStatusLine([]byte(mine)); !strings.Contains(string(out), "my-line") {
		t.Errorf("removed the user's own status line")
	}
}

func TestWrapKeepsTheUsersStatusLineAndRestoresIt(t *testing.T) {
	mine := "{\n  \"statusLine\": {\n    \"type\": \"command\",\n    \"command\": \"~/bin/my-line --color 'a b' \\\"$HOME\\\"\",\n    \"padding\": 2\n  }\n}\n"
	wrap := func(b64 string) string { return "/p/ytta statusline " + WrapFlag + " " + b64 + " # " + Marker }
	out, ok, err := WrapStatusLine([]byte(mine), wrap)
	if err != nil || !ok {
		t.Fatalf("not wrapped: %v %v", ok, err)
	}
	if !strings.Contains(string(out), WrapFlag) || !strings.Contains(string(out), `"padding": 2`) || strings.Contains(string(out), "my-line") {
		t.Errorf("wrapped file:\n%s", out)
	}
	if again, ok, _ := WrapStatusLine(out, wrap); ok || string(again) != string(out) {
		t.Error("wrapping twice changed the file")
	}
	back, err := RemoveStatusLine(out)
	if err != nil || string(back) != mine {
		t.Errorf("uninstall did not restore the user's line:\n%s", back)
	}
	if _, ok, _ := WrapStatusLine([]byte(`{"model":"opus"}`), wrap); ok {
		t.Error("wrapped a status line that does not exist")
	}
}

func TestRemoveLeavesNoEmptyHooks(t *testing.T) {
	added, err := Add([]byte(`{"model":"opus"}`), []string{"Stop"}, ytta)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Remove(added)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "hooks") {
		t.Errorf("empty hooks left behind:\n%s", out)
	}
}

// Commands are found by the trailing marker, wherever the binary lives.
func TestMarkerFindsCommandsAtAnyPath(t *testing.T) {
	h := Hook{Command: "test -x /usr/local/bin/ytta && /usr/local/bin/ytta hook; exit 0 # ytta"}
	added, err := Add([]byte("{}"), []string{"Stop"}, h)
	if err != nil {
		t.Fatal(err)
	}
	if out, _ := Remove(added); strings.Contains(string(out), "ytta hook") {
		t.Errorf("not removed:\n%s", out)
	}
}

func TestCommandsListsOnlyYttaEntries(t *testing.T) {
	out, err := Add([]byte(settings), []string{"Stop", "SessionStart"}, Hook{Command: "/bin/ytta hook # ytta"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Commands(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "/bin/ytta hook # ytta" || got[1] != got[0] {
		t.Errorf("Commands = %q", got)
	}
	if got, _ := Commands([]byte(settings)); len(got) != 0 {
		t.Errorf("foreign hooks reported as ytta's: %q", got)
	}
}
