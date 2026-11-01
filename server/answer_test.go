package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeTool writes a script that records its arguments, one call per line.
func fakeTool(t *testing.T, name, body string) (bin, calls string) {
	t.Helper()
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")
	bin = filepath.Join(dir, name)
	script := "#!/bin/sh\necho \"$*\" >> " + calls + "\n" + body + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, calls
}

func readCalls(t *testing.T, calls string) string {
	b, _ := os.ReadFile(calls)
	return strings.TrimSpace(string(b))
}

func TestAnswer(t *testing.T) {
	hc, calls := fakeTool(t, "hc", `echo '{"ok":true}'`)
	s := &server{hc: hc, events: newEventHub(hc)}
	s.events.where["t7"] = "~ / 1" // known: add() doesn't ask hc for it
	s.events.add(context.Background(), AgentEvent{Terminal: "t7", State: "blocked", Kind: "permission"})
	seq := s.events.current["t7"].Seq

	post := func(body string) int {
		rec := httptest.NewRecorder()
		s.answer(rec, httptest.NewRequest("POST", "/api/answer", strings.NewReader(body)))
		return rec.Code
	}

	cases := []struct {
		body string
		code int
		call string
	}{
		{`{"terminal":"t7","seq":` + itoa(seq) + `,"answer":"allow"}`, 200, "keys --json t7 enter"},
		{`{"terminal":"t7","answer":"deny"}`, 200, "keys --json t7 esc"},
		{`{"terminal":"t7","answer":"reply","text":"use the second one"}`, 200, "send --json --force t7 use the second one"},
		{`{"terminal":"t7","seq":` + itoa(seq+5) + `,"answer":"allow"}`, http.StatusConflict, ""}, // a newer prompt
		{`{"terminal":"t9","answer":"allow"}`, http.StatusConflict, ""},                           // not waiting
		{`{"terminal":"t7","answer":"reply","text":"  "}`, http.StatusBadRequest, ""},
		{`{"terminal":"t7","answer":"rm -rf"}`, http.StatusBadRequest, ""},
		{`{"terminal":"t7; ls","answer":"allow"}`, http.StatusBadRequest, ""},
	}
	for _, c := range cases {
		os.Remove(calls)
		if got := post(c.body); got != c.code {
			t.Errorf("%s: code %d, want %d", c.body, got, c.code)
		}
		if got := readCalls(t, calls); got != c.call {
			t.Errorf("%s: hc called with %q, want %q", c.body, got, c.call)
		}
	}

	// Finished: a reply is the next instruction (no --force); allow means nothing.
	s.events.add(context.Background(), AgentEvent{Terminal: "t7", State: "done"})
	os.Remove(calls)
	if got := post(`{"terminal":"t7","answer":"allow"}`); got != http.StatusConflict {
		t.Errorf("allow after done: code %d, want 409", got)
	}
	if got := post(`{"terminal":"t7","answer":"reply","text":"now the tests"}`); got != 200 {
		t.Errorf("reply after done: code %d, want 200", got)
	}
	if got := readCalls(t, calls); got != "send --json t7 now the tests" {
		t.Errorf("reply after done: hc called with %q", got)
	}

	// Answered on the Mac meanwhile: working again, so nothing is typed.
	s.events.add(context.Background(), AgentEvent{Terminal: "t7", State: "working"})
	os.Remove(calls)
	if got := post(`{"terminal":"t7","answer":"allow"}`); got != http.StatusConflict {
		t.Errorf("after the prompt went: code %d, want 409", got)
	}
	if got := readCalls(t, calls); got != "" {
		t.Errorf("after the prompt went: hc called with %q", got)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestKeepAwake(t *testing.T) {
	bin, calls := fakeTool(t, "caffeinate", "exec sleep 30")
	hub := newEventHub("/bin/false")
	hub.where["t1"] = ""
	k := &keepAwake{hub: hub, bin: bin, linger: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { k.run(ctx); close(done) }()

	waitFor := func(what string, want bool) {
		t.Helper()
		for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			if k.on() == want {
				return
			}
		}
		t.Fatalf("%s: awake = %v, want %v", what, k.on(), want)
	}

	waitFor("nothing busy", false)
	hub.add(ctx, AgentEvent{Terminal: "t1", State: "working"})
	waitFor("working", true)
	for end := time.Now().Add(2 * time.Second); readCalls(t, calls) == "" && time.Now().Before(end); {
		time.Sleep(20 * time.Millisecond)
	}
	if got := readCalls(t, calls); !strings.HasPrefix(got, "-i -w ") {
		t.Errorf("caffeinate called with %q", got)
	}
	hub.add(ctx, AgentEvent{Terminal: "t1", State: "blocked"})
	waitFor("blocked", true)
	hub.add(ctx, AgentEvent{Terminal: "t1", State: "done"})
	time.Sleep(100 * time.Millisecond)
	if !k.on() {
		t.Error("asleep right after done: should linger")
	}
	// The linger runs out on the next wake; an unrelated event is one.
	time.Sleep(time.Second)
	hub.add(ctx, AgentEvent{Terminal: "t2", State: "idle"})
	waitFor("after the linger", false)

	hub.add(ctx, AgentEvent{Terminal: "t1", State: "working"})
	waitFor("working again", true)
	cancel()
	<-done
	if k.on() {
		t.Error("still awake after the server stopped")
	}
}
