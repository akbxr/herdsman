package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// call runs a handler with a JSON body (GET without one).
func call(h http.HandlerFunc, method, target, body string) (int, map[string]any) {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

const machineLS = `{"machines":[{"host":"buildbox","id":"8d19","name":"box","session":"box","state":"connected","target":"me@buildbox","temporary":false,"version":"0.1.16","via":false},` +
	`{"host":"","id":"77aa","name":"pi","session":"pi","state":"needs_login","target":"pi@10.0.0.7","temporary":true,"why":"Permission denied (publickey,password)."}]}`

func TestMachinesList(t *testing.T) {
	hc, calls := fakeTool(t, "hc", `echo '`+machineLS+`'`)
	s := &server{hc: hc}
	code, out := call(s.machinesHandler, "GET", "/api/machines", "")
	if code != 200 {
		t.Fatal(code, out)
	}
	b, _ := json.Marshal(out["machines"])
	host, _ := os.Hostname()
	host, _, _ = strings.Cut(host, ".")
	want := `[{"host":"` + host + `","label":"This Mac","local":true,"name":"","state":"connected","temporary":false,"why":""},` +
		`{"host":"buildbox","label":"box","local":false,"name":"box","state":"connected","temporary":false,"why":""},` +
		`{"host":"pi@10.0.0.7","label":"pi","local":false,"name":"pi","state":"needs_login","temporary":true,"why":"Permission denied (publickey,password)."}]`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
	if c := readCalls(t, calls); c != "machine ls --json" {
		t.Errorf("calls: %s", c)
	}

	// hc can't list them: this Mac alone, and why.
	s.hc = "/nonexistent/hc"
	code, out = call(s.machinesHandler, "GET", "/api/machines", "")
	if ms, _ := out["machines"].([]any); code != 200 || len(ms) != 1 || out["error"] == nil {
		t.Errorf("without hc: %d %v", code, out)
	}
}

func TestMachineAdd(t *testing.T) {
	hc, calls := fakeTool(t, "hc", `case "$1 $2" in
"machine add") case "$*" in
  *broken*) echo '{"error":{"hint":"check ssh me@broken","kind":"unreachable","message":"me@broken: ssh: connect to host broken port 22: Connection refused"}}'; exit 1;;
  *) echo '{"name":"box","target":"me@buildbox","host":"buildbox","id":"8d19","version":"0.1.16"}';;
  esac;;
"machine ls") echo '`+machineLS+`';;
esac`)
	s := &server{hc: hc}
	for _, body := range []string{`{}`, `{"host":""}`, `{"host":"-oProxyCommand=sh"}`, `{"host":"me@box; rm -rf ~"}`, `{"host":"a b"}`,
		`{"host":"me@box","name":"-x"}`, `{"host":"me@box","name":"a/b"}`, `{"host":"me@box","name":"two words"}`, `not json`} {
		if code, out := call(s.machineAdd, "POST", "/api/machines", body); code != 400 {
			t.Errorf("%s: %d %v", body, code, out)
		}
	}
	if c := readCalls(t, calls); c != "" {
		t.Errorf("hc was called for bad input: %s", c)
	}

	code, out := call(s.machineAdd, "POST", "/api/machines", `{"host":"me@buildbox","name":"box"}`)
	m, _ := out["machine"].(map[string]any)
	if code != 200 || m["name"] != "box" || m["state"] != "connected" || m["host"] != "buildbox" || m["label"] != "box" {
		t.Errorf("add: %d %v", code, out)
	}
	if c := readCalls(t, calls); !strings.HasPrefix(c, "machine add --json --install --name box me@buildbox\nmachine ls --json") {
		t.Errorf("calls: %s", c)
	}

	code, out = call(s.machineAdd, "POST", "/api/machines", `{"host":"me@broken"}`)
	if code != http.StatusBadGateway || !strings.Contains(out["error"].(string), "Connection refused") || strings.Contains(out["error"].(string), "{") {
		t.Errorf("failure: %d %v", code, out)
	}
}

func TestMachineRemoveAndConnect(t *testing.T) {
	dir := t.TempDir()
	stdin := filepath.Join(dir, "stdin")
	hc, calls := fakeTool(t, "hc", `case "$1" in
machine) [ "$4" = box ] || { echo '{"error":{"kind":"machine_unknown","message":"no machine '$4'"}}'; exit 1; }; echo '{"removed":"box"}';;
connect) read -r line; echo "$line" >> `+stdin+`
  case "$line" in
  *'"box"'*) echo '{"jsonrpc":"2.0","id":1,"result":{"name":"box","host":"buildbox","state":"connected","target":"me@buildbox"}}';;
  *'"pi"'*) echo '{"jsonrpc":"2.0","id":1,"result":{"name":"pi","state":"needs_login","why":"password"}}';;
  *) echo '{"jsonrpc":"2.0","error":{"code":-32000,"data":{"hint":"hc machine ls","kind":"machine_unknown"},"message":"no machine x"},"id":1}';;
  esac;;
esac`)
	s := &server{hc: hc}

	if code, out := call(s.machineRemove, "POST", "/api/machines/remove", `{"name":"box"}`); code != 200 || out["removed"] != "box" {
		t.Errorf("rm: %d %v", code, out)
	}
	if code, _ := call(s.machineRemove, "POST", "/api/machines/remove", `{"name":"other"}`); code != 404 {
		t.Errorf("rm unknown: %d", code)
	}
	for _, body := range []string{`{}`, `{"name":"-rf"}`, `{"name":"a b"}`} {
		if code, _ := call(s.machineRemove, "POST", "/api/machines/remove", body); code != 400 {
			t.Errorf("rm %s: %d", body, code)
		}
	}
	if c := readCalls(t, calls); !strings.HasPrefix(c, "machine rm --json box\nmachine rm --json other") || strings.Count(c, "\n") != 1 {
		t.Errorf("calls: %s", c)
	}

	code, out := call(s.machineConnect, "POST", "/api/machines/connect", `{"name":"box"}`)
	if m, _ := out["machine"].(map[string]any); code != 200 || m["state"] != "connected" {
		t.Errorf("connect: %d %v", code, out)
	}
	code, out = call(s.machineConnect, "POST", "/api/machines/connect", `{"name":"pi"}`)
	if code != http.StatusConflict || out["error"] != "pi needs a password; log in on the Mac with: hc machine login pi" {
		t.Errorf("needs a password: %d %v", code, out)
	}
	if code, _ := call(s.machineConnect, "POST", "/api/machines/connect", `{"name":"x"}`); code != 404 {
		t.Errorf("unknown: %d", code)
	}
	b, _ := os.ReadFile(stdin)
	if first, _, _ := strings.Cut(string(b), "\n"); first != `{"id":1,"jsonrpc":"2.0","method":"machine.connect","params":{"machine":"box"}}` {
		t.Errorf("sent %s", b)
	}
}

func TestStateMachines(t *testing.T) {
	herdr, _ := fakeTool(t, "herdr-app", `case "$1" in status) echo '{"window":true}';; ls) echo '{"spaces":[]}';; esac`)
	hc, _ := fakeTool(t, "hc", `echo '`+machineLS+`'`)
	s := &server{herdr: herdr, hc: hc}
	rec := httptest.NewRecorder()
	s.state(rec, httptest.NewRequest("GET", "/api/state", nil))
	var out map[string]json.RawMessage
	json.Unmarshal(rec.Body.Bytes(), &out)
	var ms []map[string]any
	json.Unmarshal(out["machines"], &ms)
	if rec.Code != 200 || string(out["status"]) != `{"window":true}` || string(out["ls"]) != `{"spaces":[]}` || len(ms) != 2 || ms[1]["state"] != "needs_login" {
		t.Errorf("state: %d %s", rec.Code, rec.Body)
	}

	// hc failing doesn't fail the state.
	s.hc, _ = fakeTool(t, "hc", `echo boom >&2; exit 1`)
	rec = httptest.NewRecorder()
	s.state(rec, httptest.NewRequest("GET", "/api/state", nil))
	out = nil
	json.Unmarshal(rec.Body.Bytes(), &out)
	if _, has := out["machines"]; rec.Code != 200 || has || out["ls"] == nil {
		t.Errorf("without machines: %d %s", rec.Code, rec.Body)
	}
}

func TestTaskOnMachine(t *testing.T) {
	// The machine: /srv/app is a folder, /srv/none isn't; claude is installed.
	hc, hcCalls := fakeTool(t, "hc", `case "$*" in
*"test -d /srv/app"*) echo '{"code":0,"stdout":"","stderr":""}';;
*"test -d"*) echo '{"code":1,"stdout":"","stderr":""}';;
*"command -v"*) echo '{"code":0,"stdout":"Y2xhdWRlCg==","stderr":""}';;
esac`)
	ls := `{"spaces":[{"id":"s1","machine":null,"active":true,"tabs":[]},{"id":"s2","machine":"box","active":false,"tabs":[]},{"id":"s3","machine":"box","active":true,"tabs":[]},{"id":"s4","machine":"box","active":false,"tabs":[]}]}`
	herdr, calls := fakeTool(t, "herdr-app", `case "$1 $2" in
"ls ") echo '`+ls+`';;
"do tab.new") echo '{"space":"s3","tab":"tb1","terminal":"t5"}';;
"do space.new") case "$3" in *'"other"'*) echo '{"space":"s9","tab":"tb2"}';; *) echo '{"space":"s8","tab":"tb2","terminal":"pi/t9"}';; esac;;
esac`)
	s := &server{herdr: herdr, hc: hc, programsFile: "/nonexistent", hasTool: func(string) bool { return false }}
	post := func(body string) (int, map[string]any) { return call(s.task, "POST", "/api/task", body) }

	// A space on the machine (the active one): a tab in it.
	code, out := post(`{"cwd":"/srv/app","agent":"claude","prompt":"","machine":"box"}`)
	if code != 200 || out["terminal"] != "box/t5" || out["space"] != "s3" || out["agent"] != "claude" || out["prompt"] != "none" {
		t.Errorf("tab.new: %d %v", code, out)
	}
	if c := readCalls(t, calls); c != `ls
do tab.new {"argv":["claude"],"folder":"/srv/app","space":"s3"}` {
		t.Errorf("herdr-app calls:\n%s", c)
	}
	if c := readCalls(t, hcCalls); !strings.Contains(c, "exec --json --on box") || !strings.Contains(c, "test -d /srv/app") {
		t.Errorf("hc calls:\n%s", c)
	}

	// None there: a new space on the machine.
	os.Remove(calls)
	code, out = post(`{"cwd":"/srv/app/","agent":"claude","machine":"pi"}`)
	if code != 200 || out["terminal"] != "pi/t9" {
		t.Errorf("space.new: %d %v", code, out)
	}
	if c := readCalls(t, calls); c != `ls
do space.new {"argv":["claude"],"folder":"/srv/app","machine":"pi"}` {
		t.Errorf("herdr-app calls:\n%s", c)
	}

	// Refused before the window is touched.
	os.Remove(calls)
	for _, body := range []string{
		`{"cwd":"/srv/none","agent":"claude","machine":"box"}`,  // not a folder there
		`{"cwd":"/srv/app","agent":"tego","machine":"box"}`,     // not installed there
		`{"cwd":"/srv/app","agent":"claude","machine":"-oops"}`, // not a machine name
		`{"cwd":"srv/app","agent":"claude","machine":"box"}`,    // not a full path
	} {
		if code, out := post(body); code != 400 {
			t.Errorf("%s: %d %v", body, code, out)
		}
	}
	if c := readCalls(t, calls); c != "" {
		t.Errorf("herdr-app called: %s", c)
	}
	if _, out := post(`{"cwd":"/srv/none","agent":"claude","machine":"box"}`); out["error"] != "/srv/none is not a folder on box" {
		t.Errorf("message: %v", out)
	}
}

func TestOpenTabFindsTheTerminal(t *testing.T) {
	ls := `{"spaces":[{"id":"s9","machine":"other","active":true,"tabs":[{"id":"tb2","terminals":[{"terminal":"t3"}]}]}]}`
	herdr, _ := fakeTool(t, "herdr-app", `case "$1" in ls) echo '`+ls+`';; do) echo '{"space":"s9","tab":"tb2"}';; esac`)
	s := &server{herdr: herdr}
	made, _, err := s.openTabOn(context.Background(), "other", "/x", []string{"claude"})
	if err != nil || made.Terminal != "other/t3" {
		t.Errorf("got %+v %v", made, err)
	}
}

func TestModelsOnAnotherMachine(t *testing.T) {
	hc, calls := fakeTool(t, "hc", `echo '{}'`)
	s := &server{hc: hc, home: t.TempDir()}
	code, out := call(s.modelsHandler, "GET", "/api/models?terminal=box/t3&agent=claude", "")
	if code != 200 || out["supported"] != false || out["why"] != "Models are switched on this Mac only." || out["models"] != nil {
		t.Errorf("remote: %d %v", code, out)
	}
	code, out = call(s.modelsHandler, "GET", "/api/models?agent=claude", "")
	if code != 200 || out["supported"] != true || out["models"] == nil {
		t.Errorf("local: %d %v", code, out)
	}
	code, out = call(s.modelsHandler, "GET", "/api/models?agent=claude&terminal=t3", "")
	if code != 200 || out["supported"] != true {
		t.Errorf("local terminal: %d %v", code, out)
	}
	if code, _ := call(s.modelsHandler, "GET", "/api/models?terminal=box/../t3", ""); code != 400 {
		t.Errorf("bad terminal: %d", code)
	}

	code, out = call(s.session, "POST", "/api/session", `{"terminal":"box/t3","agent":"claude","model":"claude-opus-4-5"}`)
	if code != 200 || out["supported"] != false {
		t.Errorf("switch remote: %d %v", code, out)
	}
	if c := readCalls(t, calls); c != "" {
		t.Errorf("hc called: %s", c)
	}
}

func TestSessionsOnAnotherMachine(t *testing.T) {
	hc, _ := fakeRemote(t, "exit 1")
	s := &server{hc: hc}
	code, out := call(s.sessions, "GET", "/api/sessions?machine=sbox&cwd=/srv/app", "")
	if ss, _ := out["sessions"].([]any); code != 200 || ss == nil || len(ss) != 0 || out["note"] != "Earlier sessions are listed for this Mac only." || out["home"] != os.Getenv("HOME") {
		t.Errorf("sessions: %d %v", code, out)
	}
	if code, _ := call(s.resume, "POST", "/api/resume", `{"agent":"tego","id":"20261101-083706-ee31c7","machine":"sbox"}`); code != 400 {
		t.Errorf("resume: %d", code)
	}
}

func TestFoldersAndAgentsOnMachine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // the machine's home: fakeRemote runs here
	for _, d := range []string{"Code/app1/.git", "Code/app1/nested/.git", "Code/projects/app2/.git", "Code/node_modules/x/.git",
		"Code/.hidden/y/.git", "src/a/b/c/.git", "src/a/b/c/d/too-deep/.git", "Code/work/live/.git"} {
		os.MkdirAll(filepath.Join(home, d), 0o755)
	}
	os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o755)
	os.WriteFile(filepath.Join(home, ".local", "bin", "codex"), []byte("#!/bin/sh\n"), 0o755)
	live := "/srv/live" // where its terminal is (a temporary folder here would be left out)
	progs := filepath.Join(home, "programs.json")
	os.WriteFile(progs, []byte(`{"programs":{"ghost":{"name":"Ghost","run":["ghost-tool-xyz"]},"cx":{"name":"Codex here","run":["codex","--yolo"]}}}`), 0o644)
	t.Setenv("PATH", "/usr/bin:/bin")
	hc, _ := fakeRemote(t, `[ "$1" = ls ] && echo '{"terminals":[{"address":"fold/t1","cwd":"`+live+`","state":"live"},{"address":"t2","cwd":"/elsewhere","state":"live"}]}'`)
	homes.Lock()
	delete(homes.m, "fold")
	homes.Unlock()
	s := &server{hc: hc, programsFile: progs}

	code, out := call(s.folders, "GET", "/api/folders?machine=fold", "")
	if code != 200 || out["home"] != home {
		t.Fatalf("%d %v", code, out)
	}
	paths := func(key string) string {
		var ps []string
		for _, f := range out[key].([]any) {
			ps = append(ps, strings.TrimPrefix(f.(map[string]any)["path"].(string), home+"/"))
		}
		return strings.Join(ps, " ")
	}
	if got := paths("recent"); got != "/srv/live" {
		t.Errorf("recent: %s", got)
	}
	got := strings.Split(paths("projects"), " ")
	sort.Strings(got)
	if strings.Join(got, " ") != "Code/app1 Code/projects/app2 Code/work/live src/a/b/c" {
		t.Errorf("projects: %v", got)
	}
	if code, _ := call(s.folders, "GET", "/api/folders?machine=-x", ""); code != 400 {
		t.Errorf("bad machine: %d", code)
	}

	code, out = call(s.agentsHandler, "GET", "/api/agents?machine=fold", "")
	b, _ := json.Marshal(out["agents"])
	if code != 200 || !strings.Contains(string(b), `{"argv":["codex","--yolo"],"id":"codex","name":"Codex","positional":true}`) || strings.Contains(string(b), "ghost") {
		t.Errorf("agents: %d %s", code, b)
	}
}
