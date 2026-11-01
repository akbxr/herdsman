package main

// A new task from the phone: a folder, an agent and what to do. It opens a
// tab on the Mac in that folder running the agent, gives the agent the
// task, and answers with the new terminal for the phone to open.
//
// How the task reaches the agent:
//   argv   tego, Claude Code, Codex, omp and Pi start their interactive
//          session with a prompt given as an argument (`claude "fix it"`):
//          it is there from the first frame, even behind a trust dialog.
//   typed  every other program, and a task an agent would mistake for a
//          flag ("-v …") or one of its commands (a single word: `claude
//          update`): it starts bare, and once its screen settles (or it
//          reports idle) hc pastes the task and presses Enter.
//
// Herdr runs argv as "$@" in your login shell (`zsh -lic '"$@"; exec …'`):
// no word of it is ever parsed by a shell.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// AgentInfo is a program a task can run.
type AgentInfo struct {
	ID   string   `json:"id"`
	Name string   `json:"name"` // shown on the phone
	Argv []string `json:"argv"`
	// Positional: it takes the first message as its last argument and stays
	// interactive. Otherwise the task is typed in once it is ready.
	Positional bool `json:"positional"`
}

// Agents the phone knows by name, in the order it shows them.
var knownAgents = []AgentInfo{
	{ID: "tego", Name: "tego", Argv: []string{"tego"}, Positional: true},
	{ID: "claude", Name: "Claude Code", Argv: []string{"claude"}, Positional: true},
	{ID: "codex", Name: "Codex", Argv: []string{"codex"}, Positional: true},
	{ID: "omp", Name: "omp", Argv: []string{"omp"}, Positional: true},
	{ID: "pi", Name: "Pi", Argv: []string{"pi"}, Positional: true},
}

const taskMaxPrompt = 32 << 10

// Variables for the tests.
var (
	taskReady  = 90 * time.Second        // the most a typed task waits for its program
	taskPoll   = 500 * time.Millisecond  // how often it looks
	taskSettle = 1500 * time.Millisecond // a screen unchanged this long has settled
	taskWarmup = 2 * time.Second         // and the program has run at least this long
)

// Shells: the foreground before (and after) the agent runs.
var shells = map[string]bool{"zsh": true, "bash": true, "fish": true, "sh": true, "dash": true, "ksh": true, "tcsh": true, "csh": true, "nu": true, "login": true}

// GET /api/agents[?machine=box]: the agents a task can start (on box).
func (s *server) agentsHandler(w http.ResponseWriter, r *http.Request) {
	m := r.URL.Query().Get("machine")
	if !validMachine(m) {
		writeErr(w, http.StatusBadRequest, errors.New("bad machine name"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	list, err := s.agentsOn(ctx, s.on(m))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, map[string]any{"agents": list})
}

// agents: the known agents that are installed, then the other programs in
// Herdr's programs.json. A programs.json entry for a known agent lends it
// its argv (your flags).
func (s *server) agents() []AgentInfo {
	have := s.hasTool
	if have == nil {
		have = toolInstalled
	}
	return s.agentsWith(have)
}

// On another machine: which of the programs named are there, on its PATH or
// in the folders a login shell adds (as toolInstalled checks here).
const toolsScript = `for n; do
  case "$n" in
  */*) [ -f "$n" ] && [ -x "$n" ] && echo "$n"; continue;;
  esac
  if command -v "$n" >/dev/null 2>&1; then echo "$n"; continue; fi
  for d in "$HOME/.local/bin" "$HOME/bin" "$HOME/.bun/bin" "$HOME/.npm-global/bin" "$HOME/go/bin" "$HOME/.cargo/bin" "$HOME/.volta/bin" "$HOME/.deno/bin" /opt/homebrew/bin /usr/local/bin; do
    if [ -f "$d/$n" ] && [ -x "$d/$n" ]; then echo "$n"; break; fi
  done
done`

// agentsOn: agents for a task on h: what is installed there, checked in one
// round trip (programs.json is this Mac's Herdr's, its programs too must be
// there).
func (s *server) agentsOn(ctx context.Context, h host) ([]AgentInfo, error) {
	if !h.remote() {
		return s.agents(), nil
	}
	var names []string
	seen := map[string]bool{}
	add := func(n string) {
		if n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for _, a := range knownAgents {
		add(a.Argv[0])
	}
	for _, p := range s.programs().Programs {
		if len(p.Run) > 0 {
			add(p.Run[0])
		}
	}
	out, err := h.run(ctx, "", append([]string{"sh", "-c", toolsScript, "sh"}, names...)...)
	if err != nil {
		return nil, err
	}
	there := map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		there[strings.TrimSpace(l)] = true
	}
	return s.agentsWith(func(n string) bool { return there[n] }), nil
}

// programsJSON is Herdr's programs.json.
type programsJSON struct {
	Programs map[string]struct {
		Name string   `json:"name"`
		Run  []string `json:"run"`
	} `json:"programs"`
}

func (s *server) programs() programsJSON {
	file := s.programsFile
	if file == "" {
		home, _ := os.UserHomeDir()
		file = filepath.Join(home, ".config", "herdr-app", "programs.json")
	}
	var progs programsJSON
	if b, err := os.ReadFile(file); err == nil {
		json.Unmarshal(b, &progs)
	}
	return progs
}

// agentsWith: agents, with have telling what is installed.
func (s *server) agentsWith(have func(string) bool) []AgentInfo {
	progs := s.programs()

	list := []AgentInfo{}
	known := map[string]int{}
	for _, a := range knownAgents {
		if have(a.Argv[0]) {
			known[a.ID] = len(list)
			list = append(list, a)
		}
	}
	// Map order is random: sort the extra programs by id.
	ids := make([]string, 0, len(progs.Programs))
	for id := range progs.Programs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p := progs.Programs[id]
		if len(p.Run) == 0 || p.Run[0] == "" {
			continue
		}
		base := filepath.Base(p.Run[0])
		k, isKnown := known[id]
		if !isKnown {
			k, isKnown = known[base]
		}
		if isKnown {
			list[k].Argv = append([]string(nil), p.Run...)
			continue
		}
		if !have(p.Run[0]) {
			continue
		}
		name := p.Name
		if name == "" {
			name = id
		}
		// A program from programs.json that is a known agent under another name.
		positional := false
		for _, a := range knownAgents {
			if a.Argv[0] == base {
				positional = true
			}
		}
		list = append(list, AgentInfo{ID: id, Name: name, Argv: append([]string(nil), p.Run...), Positional: positional})
		known[id] = len(list) - 1
	}
	return list
}

// toolInstalled: on PATH, or in the folders a login shell adds (launchd's
// PATH is short).
func toolInstalled(name string) bool {
	if strings.Contains(name, "/") {
		st, err := os.Stat(name)
		return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
	}
	if _, err := exec.LookPath(name); err == nil {
		return true
	}
	home, _ := os.UserHomeDir()
	for _, d := range []string{".local/bin", "bin", ".bun/bin", ".npm-global/bin", "go/bin", ".cargo/bin", ".volta/bin", ".deno/bin"} {
		if st, err := os.Stat(filepath.Join(home, d, name)); err == nil && !st.IsDir() {
			return true
		}
	}
	for _, d := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
		if st, err := os.Stat(filepath.Join(d, name)); err == nil && !st.IsDir() {
			return true
		}
	}
	return false
}

// taskLaunch: the argv to start (with flags, before the prompt), and how
// the prompt gets there.
func taskLaunch(a AgentInfo, prompt string, flags ...string) (argv []string, delivery string) {
	argv = append(append([]string(nil), a.Argv...), flags...)
	switch {
	case prompt == "":
		return argv, "none"
	case a.Positional && !strings.HasPrefix(prompt, "-") && strings.ContainsAny(prompt, " \t\n"):
		return append(argv, prompt), "argv"
	default:
		return argv, "typed"
	}
}

// task starts one: {"cwd": "/Users/me/Code/app", "agent": "claude",
// "prompt": "…", "model": "claude-sonnet-5-5", "thinking": "high",
// "machine": "box"}; model and thinking are optional (the agent's own
// default), machine too (this Mac; else cwd is a folder there).
func (s *server) task(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cwd      string `json:"cwd"`
		Agent    string `json:"agent"`
		Prompt   string `json:"prompt"`
		Model    string `json:"model"`
		Thinking string `json:"thinking"`
		Machine  string `json:"machine"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("need {cwd, agent, prompt}"))
		return
	}
	if !validMachine(req.Machine) {
		writeErr(w, http.StatusBadRequest, errors.New("bad machine name"))
		return
	}
	h := s.on(req.Machine)
	where := "the Mac"
	if h.remote() {
		where = req.Machine
	}
	cwd := path.Clean(req.Cwd)
	if req.Cwd == "" || !path.IsAbs(cwd) {
		writeErr(w, http.StatusBadRequest, errors.New("the folder must be a full path"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if !h.isDir(ctx, cwd) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("%s is not a folder on %s", cwd, where))
		return
	}
	prompt := strings.TrimSpace(req.Prompt)
	if len(prompt) > taskMaxPrompt {
		writeErr(w, http.StatusBadRequest, errors.New("the task is too long (32 KB at most)"))
		return
	}
	list, err := s.agentsOn(ctx, h)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	var agent *AgentInfo
	for _, a := range list {
		if a.ID == req.Agent {
			found := a
			agent = &found
			break
		}
	}
	if agent == nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("no agent %q on %s", req.Agent, where))
		return
	}

	flags, err := s.modelFlags(*agent, req.Model, req.Thinking)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	argv, delivery := taskLaunch(*agent, prompt, flags...)
	made, code, err := s.openTabOn(r.Context(), req.Machine, cwd, argv)
	if err != nil {
		writeErr(w, code, err)
		return
	}
	if delivery == "typed" {
		// The phone opens the terminal meanwhile and sees it arrive.
		go func() {
			if err := s.typeWhenReady(context.Background(), made.Terminal, prompt); err != nil {
				log.Printf("task for %s: %v", made.Terminal, err)
			}
		}()
	}
	writeJSON(w, map[string]string{"terminal": made.Terminal, "tab": made.Tab, "space": made.Space, "agent": agent.ID, "prompt": delivery})
}

// modelFlags: the flags that start an agent with a model and thinking level
// (none for "", its own default), once checked against what it offers.
func (s *server) modelFlags(agent AgentInfo, model, thinking string) ([]string, error) {
	if model == "" && thinking == "" {
		return nil, nil
	}
	kind := agentOf(foregroundInfo{Name: agent.ID, Argv: agent.Argv}) // programs.json may run it by another name
	choices := s.modelChoices(kind)
	if choices == nil {
		return nil, fmt.Errorf("%s doesn't take a model from here", agent.Name)
	}
	if err := choices.check(model, thinking); err != nil {
		return nil, err
	}
	return startFlags(kind, model, thinking), nil
}

// tabMade is the tab herdr-app opened and the terminal in it.
type tabMade struct{ Space, Tab, Terminal string }

// openTab opens a tab in the Herdr window in folder running argv; on
// failure, the HTTP status to answer with.
func (s *server) openTab(ctx context.Context, folder string, argv []string) (tabMade, int, error) {
	args, _ := json.Marshal(map[string]any{"folder": folder, "argv": argv})
	out, err := run(ctx, 20*time.Second, s.herdr, "do", "tab.new", string(args))
	if err != nil {
		return tabMade{}, http.StatusBadGateway, fmt.Errorf("herdr-app: %w (is the Herdr window open?)", err)
	}
	var made tabMade
	if err := json.Unmarshal(out, &made); err != nil || !validTerminal(made.Terminal) {
		return tabMade{}, http.StatusBadGateway, fmt.Errorf("herdr-app made the tab but didn't name its terminal: %s", strings.TrimSpace(string(out)))
	}
	return made, 0, nil
}

// openTabOn: openTab on a machine: a new tab in a space of the window's on
// that machine (the active one first), else a new space there.
func (s *server) openTabOn(ctx context.Context, machine, folder string, argv []string) (tabMade, int, error) {
	if machine == "" {
		return s.openTab(ctx, folder, argv)
	}
	ls, err := s.herdrSpaces(ctx)
	if err != nil {
		return tabMade{}, http.StatusBadGateway, fmt.Errorf("herdr-app: %w (is the Herdr window open?)", err)
	}
	space := ""
	for _, sp := range ls {
		if sp.Machine != nil && *sp.Machine == machine && (space == "" || sp.Active) {
			space = sp.ID
		}
	}
	cmd, args := "space.new", map[string]any{"machine": machine, "folder": folder, "argv": argv}
	if space != "" {
		cmd, args = "tab.new", map[string]any{"space": space, "folder": folder, "argv": argv}
	}
	js, _ := json.Marshal(args)
	out, err := run(ctx, 30*time.Second, s.herdr, "do", cmd, string(js))
	if err != nil {
		return tabMade{}, http.StatusBadGateway, fmt.Errorf("herdr-app: %w (is the Herdr window open?)", err)
	}
	var made tabMade
	json.Unmarshal(out, &made)
	if made.Terminal == "" && made.Tab != "" {
		// Not named: the terminal of the tab it made, from the window's list.
		if again, err := s.herdrSpaces(ctx); err == nil {
			for _, sp := range again {
				for _, t := range sp.Tabs {
					if t.ID == made.Tab && len(t.Terminals) > 0 {
						made.Terminal = t.Terminals[0].Terminal
					}
				}
			}
		}
	}
	if made.Terminal != "" && !strings.Contains(made.Terminal, "/") {
		made.Terminal = machine + "/" + made.Terminal
	}
	if !validTerminal(made.Terminal) {
		return tabMade{}, http.StatusBadGateway, fmt.Errorf("herdr-app made the tab but didn't name its terminal: %s", strings.TrimSpace(string(out)))
	}
	return made, 0, nil
}

// herdrSpace is a space of the Herdr window (herdr-app ls).
type herdrSpace struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Folder  string  `json:"folder"`
	Machine *string `json:"machine"` // nil: this Mac
	Active  bool    `json:"active"`
	Tabs    []struct {
		ID        string `json:"id"`
		Terminals []struct {
			Terminal string `json:"terminal"`
		} `json:"terminals"`
	} `json:"tabs"`
}

// herdrSpaces: the spaces of the Herdr window.
func (s *server) herdrSpaces(ctx context.Context) ([]herdrSpace, error) {
	out, err := run(ctx, 5*time.Second, s.herdr, "ls")
	if err != nil {
		return nil, err
	}
	var ls struct {
		Spaces []herdrSpace `json:"spaces"`
	}
	if err := json.Unmarshal(out, &ls); err != nil {
		return nil, fmt.Errorf("herdr-app ls: %w", err)
	}
	return ls.Spaces, nil
}

// typeWhenReady waits for the program to start in the terminal and settle
// (it reports idle, or its screen stops changing), then sends the text.
func (s *server) typeWhenReady(ctx context.Context, terminal, text string) error {
	ctx, cancel := context.WithTimeout(ctx, taskReady)
	defer cancel()
	var started, sameSince time.Time
	var screen string
	for {
		select {
		case <-ctx.Done():
			return errors.New("the program didn't get ready in time; the task was not typed")
		case <-time.After(taskPoll):
		}
		out, err := run(ctx, 5*time.Second, s.hc, "get", "--json", terminal)
		if err != nil {
			continue
		}
		var g struct {
			State      string
			Foreground *struct{ Name string }
			Status     *struct{ State string }
		}
		if json.Unmarshal(out, &g) != nil {
			continue
		}
		if g.State != "" && g.State != "live" {
			return errors.New("the terminal ended before the task was typed")
		}
		name := ""
		if g.Foreground != nil {
			name = strings.TrimPrefix(filepath.Base(g.Foreground.Name), "-")
		}
		if name == "" || shells[name] {
			if !started.IsZero() {
				return errors.New("the program quit before the task was typed")
			}
			continue
		}
		now := time.Now()
		if started.IsZero() {
			started = now
		}
		state := ""
		if g.Status != nil {
			state = g.Status.State
		}
		ready := state == "idle" || state == "done"
		if !ready && state == "" { // it says nothing: wait for its screen to settle
			rd, err := run(ctx, 5*time.Second, s.hc, "read", "--json", terminal)
			if err != nil {
				continue
			}
			if string(rd) != screen {
				screen, sameSince = string(rd), now
			}
			ready = now.Sub(sameSince) >= taskSettle && now.Sub(started) >= taskWarmup
		}
		if ready {
			_, err := run(ctx, 40*time.Second, s.hc, "send", "--json", terminal, text)
			return err
		}
	}
}
