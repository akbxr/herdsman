package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestLiveTailTego(t *testing.T) {
	screen := strings.Join([]string{
		" older text",
		"",
		" I'm wiring the live screen read into the chat handler.",
		" Then a liveTail helper, with tests.",
		"",
		"",
		" ╭─ Bash: go test ./... ─────────────",
		" │ ⣾ running · 0s",
		" ╰──────────────────────────────────",
		"",
		" ⠙ Working… 153.1s · 99 tok/s · esc to interrupt",
		"──────────────────────────────── Riset herdr-c",
		"❯",
		"────────────────────────────────────────────",
		" ◆ Opus 5.5 · ~ · master",
	}, "\n")
	want := []string{
		"older text",
		"",
		"I'm wiring the live screen read into the chat handler.",
		"Then a liveTail helper, with tests.",
		"",
		"",
		"╭─ Bash: go test ./... ─────────────",
		"│ ⣾ running · 0s",
		"╰──────────────────────────────────",
	}
	if got := liveTail(screen); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestLiveTailClaude(t *testing.T) {
	screen := strings.Join([]string{
		"⏺ Reading the login form.",
		"",
		"⏺ The bug is in resetForm: it runs on every error",
		"  and clears the email too.",
		"",
		"✻ Thinking… (12s · esc to interrupt)",
		"",
		"╭──────────────────────────────╮",
		"│ >                            │",
		"╰──────────────────────────────╯",
		"  ? for shortcuts",
	}, "\n")
	want := []string{
		"⏺ Reading the login form.",
		"",
		"⏺ The bug is in resetForm: it runs on every error",
		"  and clears the email too.",
	}
	if got := liveTail(screen); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestLiveTailKeepsTheLastLines(t *testing.T) {
	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines, "line "+itoa(int64(i)))
	}
	got := liveTail(strings.Join(append(lines, "────", "❯", "────", "status"), "\n"))
	if len(got) != liveLines || got[len(got)-1] != "line 39" || got[0] != "line 28" {
		t.Errorf("got %q", got)
	}
}
