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

var deck = Hook{Command: "/Users/x/tmux-agent-deck/bin/deck hook --record", Async: true, Timeout: 5}

func TestAddThenRemoveRoundTrips(t *testing.T) {
	added, err := Add([]byte(settings), []string{"Stop", "SessionEnd"}, deck)
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
	out, err := Add([]byte(settings), []string{"PreToolUse", "SessionEnd"}, deck)
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
	if got := pre[1].Hooks[0]; got.Command != deck.Command || !got.Async || got.Timeout != 5 {
		t.Errorf("deck entry = %+v", got)
	}
	if len(parsed.Hooks["SessionEnd"]) != 1 {
		t.Errorf("SessionEnd not added: %+v", parsed.Hooks["SessionEnd"])
	}
}

func TestAddIsIdempotent(t *testing.T) {
	once, err := Add([]byte(settings), []string{"Stop"}, deck)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := Add(once, []string{"Stop"}, deck)
	if err != nil {
		t.Fatal(err)
	}
	if string(once) != string(twice) {
		t.Errorf("second Add changed the file:\n%s", twice)
	}
}

func TestRemoveOnlyTouchesDeckEntriesInSharedGroup(t *testing.T) {
	shared := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"afplay x"},{"type":"command","command":"/p/tmux-agent-deck/bin/deck hook"}]}]}}`
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
		out, err := Add([]byte(in), []string{"Stop"}, deck)
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
