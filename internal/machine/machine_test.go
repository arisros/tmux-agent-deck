package machine

import (
	"flag"
	"os"
	"testing"

	"github.com/arisros/fate/render"
)

var update = flag.Bool("update", false, "rewrite golden files")

func mustNew(t testing.TB) *Machine {
	t.Helper()
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// drive applies events from a fresh session and returns the final result.
func drive(t testing.TB, m *Machine, events ...Event) Result {
	t.Helper()
	res, err := Apply(m, nil, Begin{At: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if res, err = Apply(m, res.Snapshot, e); err != nil {
			t.Fatal(err)
		}
	}
	return res
}

func TestTransitions(t *testing.T) {
	m := mustNew(t)
	cases := []struct {
		name   string
		events []Event
		want   string
	}{
		{"fresh session is idle", nil, Idle},
		{"prompt runs", []Event{Prompt{At: 2}}, Running},
		{"tool start from idle runs", []Event{ToolStart{At: 2}}, Running},
		{"permission waits", []Event{Prompt{}, ToolStart{}, Permission{}}, Waiting},
		{"notification waits", []Event{Prompt{}, NeedsInput{}}, Waiting},
		{"approval resumes", []Event{Prompt{}, Permission{}, ToolEnd{}}, Running},
		{"subagent tool end keeps waiting", []Event{Prompt{}, Permission{}, ToolEnd{Subagent: true}}, Waiting},
		{"unwatched stop is done", []Event{Prompt{}, Stop{}}, Done},
		{"stop with background work keeps running", []Event{Prompt{}, Stop{Background: 1}}, Running},
		{"background resume then stop is done", []Event{Prompt{}, Stop{Background: 1}, Prompt{}, Stop{}}, Done},
		{"stop while waiting is done", []Event{Prompt{}, Permission{}, Stop{}}, Done},
		{"focus clears done", []Event{Prompt{}, Stop{}, Focus{}}, Idle},
		{"focus leaves running alone", []Event{Prompt{}, Focus{}}, Running},
		{"idle prompt ends a turn without stop", []Event{Prompt{}, IdlePrompt{}}, Done},
		{"idle prompt never downgrades waiting", []Event{Prompt{}, Permission{}, IdlePrompt{}}, Waiting},
		{"esc mid-turn repaired by screen", []Event{Prompt{}, ToolStart{}, Screen{Kind: ScreenIdle}}, Idle},
		{"denied permission repaired by screen", []Event{Prompt{}, Permission{}, Screen{Kind: ScreenIdle}}, Idle},
		{"missed dialog found by screen", []Event{Prompt{}, Screen{Kind: ScreenDialog}}, Waiting},
		{"answered prompt while the tool still runs", []Event{Prompt{}, Permission{}, Screen{Kind: ScreenNoDialog}}, Running},
		{"no dialog says nothing about a running agent", []Event{Prompt{}, Screen{Kind: ScreenNoDialog}}, Running},
		{"no dialog does not wake an idle agent", []Event{Screen{Kind: ScreenNoDialog}}, Idle},
		{"working screen leaves running alone", []Event{Prompt{}, Screen{Kind: ScreenWorking}}, Running},
		{"done agent seen working again", []Event{Prompt{}, Stop{}, Screen{Kind: ScreenWorking}}, Running},
		{"begin resets any state", []Event{Prompt{}, Permission{}, Begin{}}, Idle},
		{"interrupt ends a running turn", []Event{Prompt{}, ToolStart{}, Interrupt{}}, Idle},
		{"interrupt ends a pending prompt", []Event{Prompt{}, Permission{}, Interrupt{}}, Idle},
		{"interrupt leaves a finished turn alone", []Event{Prompt{}, Stop{}, Interrupt{}}, Done},
		{"new prompt from done", []Event{Prompt{}, Stop{}, Prompt{}}, Running},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := drive(t, m, c.events...).To; got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

func TestSinceMarksStateEntry(t *testing.T) {
	m := mustNew(t)
	res := drive(t, m, Prompt{At: 10}, ToolStart{At: 11}, Permission{At: 20}, ToolEnd{At: 30}, ToolStart{At: 31})
	if res.To != Running || res.Ctx.Since != 30 {
		t.Errorf("got %s since %d, want running since 30", res.To, res.Ctx.Since)
	}
	res = drive(t, m, Prompt{At: 10}, Stop{At: 12, Background: 2})
	if res.Ctx.Since != 10 || res.Ctx.Background != 2 {
		t.Errorf("background stop moved since or lost the count: %+v", res.Ctx)
	}
}

func TestWhyAndSource(t *testing.T) {
	m := mustNew(t)
	bash := Permission{Reason: ReasonPermission, Tool: "Bash"}
	cases := []struct {
		name                 string
		events               []Event
		reason, tool, source string
	}{
		{"permission names its tool", []Event{Prompt{}, bash}, ReasonPermission, "Bash", SourceHook},
		{"question has no tool", []Event{Prompt{}, Permission{Reason: ReasonQuestion}}, ReasonQuestion, "", SourceHook},
		{"notification alone carries its reason", []Event{Prompt{}, NeedsInput{Reason: ReasonElicitation}}, ReasonElicitation, "", SourceHook},
		{"the notification after a permission keeps the tool", []Event{Prompt{}, bash, NeedsInput{Reason: ReasonPermission}}, ReasonPermission, "Bash", SourceHook},
		{"dialog found on screen", []Event{Prompt{}, Screen{Kind: ScreenDialog}}, ReasonDialog, "", SourceScreen},
		{"approval clears the reason", []Event{Prompt{}, bash, ToolEnd{}}, "", "", SourceHook},
		{"stop while waiting clears the reason", []Event{Prompt{}, bash, Stop{}}, "", "", SourceHook},
		{"denial seen on screen clears the reason", []Event{Prompt{}, bash, Screen{Kind: ScreenIdle}}, "", "", SourceScreen},
		{"background stop keeps the reason", []Event{Prompt{}, bash, Stop{Background: 1}}, ReasonPermission, "Bash", SourceHook},
		{"focus is its own source", []Event{Prompt{}, Stop{}, Focus{}}, "", "", SourceFocus},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := drive(t, m, c.events...).Ctx
			if got.Reason != c.reason || got.Tool != c.tool || got.Source != c.source {
				t.Errorf("got %q %q from %q, want %q %q from %q", got.Reason, got.Tool, got.Source, c.reason, c.tool, c.source)
			}
		})
	}
}

// A record written by v0.1.3 has no reason, tool or source in its context.
func TestRestoresOlderSnapshot(t *testing.T) {
	old := []byte(`{"version":1,"status":"running","value":"running","context":{"since":1790831616}}`)
	res, err := Apply(mustNew(t), old, Permission{At: 5, Reason: ReasonQuestion})
	if err != nil {
		t.Fatal(err)
	}
	if res.From != Running || res.To != Waiting || res.Ctx.Reason != ReasonQuestion {
		t.Errorf("got %s -> %s (%+v)", res.From, res.To, res.Ctx)
	}
}

func TestEntered(t *testing.T) {
	m := mustNew(t)
	res := drive(t, m, Prompt{}, Permission{})
	if !res.Entered(Waiting) {
		t.Error("first permission should enter waiting")
	}
	res, _ = Apply(m, res.Snapshot, NeedsInput{})
	if res.Entered(Waiting) {
		t.Error("the notification after a permission request must not re-enter waiting (one sound, not two)")
	}
}

// FuzzSequences feeds the machine arbitrary event sequences. Whatever
// arrives, in whatever order hooks get lost or repeated, it must stay in one
// of its four states, never error, and keep its context consistent.
func FuzzSequences(f *testing.F) {
	for _, seed := range [][]byte{
		{}, {1, 4, 3, 7}, {1, 4, 4, 5, 9, 10}, {1, 7, 1, 7, 8}, {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13},
		{4, 12, 4, 11, 4, 10}, {1, 6, 1, 13, 7},
	} {
		f.Add(seed)
	}
	m := mustNew(f)
	valid := map[string]bool{Idle: true, Running: true, Waiting: true, Done: true}
	f.Fuzz(func(t *testing.T, script []byte) {
		if len(script) > 64 {
			script = script[:64]
		}
		res, err := Apply(m, nil, Begin{At: 1})
		if err != nil {
			t.Fatal(err)
		}
		for i, b := range script {
			at := int64(i + 2)
			events := []Event{
				Begin{At: at}, Prompt{At: at}, ToolStart{At: at}, ToolEnd{At: at}, ToolEnd{At: at, Subagent: true},
				Permission{At: at, Reason: ReasonPermission, Tool: "Bash"}, NeedsInput{At: at, Reason: ReasonElicitation},
				Stop{At: at}, Stop{At: at, Background: 1}, IdlePrompt{At: at}, Focus{At: at},
				Screen{At: at, Kind: ScreenIdle}, Screen{At: at, Kind: ScreenWorking}, Screen{At: at, Kind: ScreenDialog},
				Screen{At: at, Kind: ScreenNoDialog}, Interrupt{At: at},
			}
			e := events[int(b)%len(events)]
			prev := res
			if res, err = Apply(m, res.Snapshot, e); err != nil {
				t.Fatalf("step %d %s from %s: %v", i, e.EventName(), prev.To, err)
			}
			switch {
			case !valid[res.To]:
				t.Fatalf("step %d: state %q", i, res.To)
			case res.From != prev.To:
				t.Fatalf("step %d: resumed from %q, was %q", i, res.From, prev.To)
			case res.To != Waiting && (res.Ctx.Reason != "" || res.Ctx.Tool != ""):
				t.Fatalf("step %d: %s carries a reason %q %q", i, res.To, res.Ctx.Reason, res.Ctx.Tool)
			case res.To == Waiting && res.Ctx.Reason == "":
				t.Fatalf("step %d: waiting without a reason after %s", i, e.EventName())
			case res.Ctx.Since < prev.Ctx.Since:
				t.Fatalf("step %d: since went back from %d to %d", i, prev.Ctx.Since, res.Ctx.Since)
			case res.To != prev.To && res.Ctx.Since != at:
				t.Fatalf("step %d: entered %s at %d but since is %d", i, res.To, at, res.Ctx.Since)
			}
			if _, ok := e.(Begin); ok && res.To != Idle {
				t.Fatalf("step %d: Begin left the agent %s", i, res.To)
			}
		}
	})
}

func TestDiagramIsCurrent(t *testing.T) {
	got := "# State machine\n\nGenerated by `go test ./internal/machine -update` from the machine itself.\n\n```mermaid\n" +
		render.Mermaid(mustNew(t).Describe(), render.MermaidOptions{Direction: "LR"}) + "```\n"
	const path = "../../docs/state-machine.md"
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if string(want) != got {
		t.Error("docs/state-machine.md is stale; run go test ./internal/machine -update")
	}
}

// The hook path builds the machine, restores, sends, and persists once per
// process. The plan's budget for all of it is 1 ms.
func BenchmarkHookPath(b *testing.B) {
	m := mustNew(b)
	res := drive(b, m, Prompt{At: 1})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fresh, err := New()
		if err != nil {
			b.Fatal(err)
		}
		if _, err := Apply(fresh, res.Snapshot, ToolStart{At: 2}); err != nil {
			b.Fatal(err)
		}
	}
	_ = m
}
