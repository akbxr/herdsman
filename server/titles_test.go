package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
)

func TestSessionTitles(t *testing.T) {
	h := newSessionHome(t)
	h.fill(t)
	hc, _ := fakeTool(t, "hc", `echo '{"terminals":[
		{"address":"t5","state":"live","agent":"tego"},
		{"address":"t6","state":"finished","agent":"tego"},
		{"address":"t7","state":"live","agent":"zsh"},
		{"address":"t8","state":"live","agent":"claude"},
		{"address":"t9","state":"live","agent":"tego"},
		{"address":"box/t1","state":"live","agent":"tego"}]}'`)
	asked := map[string]bool{}
	var mu sync.Mutex
	s := &server{hc: hc, herdr: "/bin/false", home: h.home, programsFile: "/nonexistent",
		transcriptOf: func(ctx context.Context, terminal string) string {
			mu.Lock()
			asked[terminal] = true
			mu.Unlock()
			return map[string]string{"t5": h.paths[tegoA], "t8": h.paths[claudeA], "t9": h.paths[tegoB]}[terminal]
		},
	}
	got := s.sessionTitles(context.Background())
	want := map[string]string{
		"t5": "Login bug fix",          // the title tego gave it
		"t8": "Dark mode for settings", // Claude's own title, renamed by you last
		"t9": "check the flaky test",   // no title yet: the first message
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("titles:\n got %q\nwant %q", got, want)
	}
	for _, not := range []string{"t6", "t7", "box/t1"} {
		if asked[not] {
			t.Errorf("asked %s for its transcript: finished, a shell or another machine's", not)
		}
	}

	// In /api/state, beside the rest.
	herdrApp, _ := fakeTool(t, "herdr-app", `echo '{}'`)
	s.herdr = herdrApp
	rec := httptest.NewRecorder()
	s.state(rec, httptest.NewRequest("GET", "/api/state", nil))
	var out struct{ Titles map[string]string }
	json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != 200 || out.Titles["t5"] != "Login bug fix" {
		t.Fatalf("state: %d %s", rec.Code, rec.Body)
	}
}

func TestTranscriptTitleLong(t *testing.T) {
	h := newSessionHome(t)
	long := "please look at why the navigation bar jumps when the page scrolls on small phones and fix it properly"
	h.write(t, "tego", h.api, tegoA, jsonl(tegoHeader(tegoA, h.api), tegoMsg("user", long)), 0)
	got := transcriptTitle(h.paths[tegoA])
	if r := []rune(got); len(r) != titleMax || r[len(r)-1] != '…' {
		t.Fatalf("got %q (%d runes)", got, len(r))
	}
	if transcriptTitle("/somewhere/.codex/sessions/x.jsonl") != "" {
		t.Fatal("a file it can't read named")
	}
}
