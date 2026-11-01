package main

// The machines a task can run on: this Mac, and the ones hc drives over ssh
// (`hc machine add`). Adding one never waits for a password: hc runs
// without a terminal (its own session, stdin closed), so ssh can only use
// keys; a machine that needs a password is logged in to on the Mac
// (`hc machine login NAME`).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// machineView is what the phone shows of a machine.
type machineView struct {
	Name      string `json:"name"`  // "" this Mac; else the part before "/" in its terminals' addresses
	Label     string `json:"label"` // "This Mac", or the name
	Host      string `json:"host"`  // the machine's own (short) host name
	State     string `json:"state"` // connecting, connected, needs_login, unreachable
	Why       string `json:"why"`   // what went wrong, when it isn't connected
	Temporary bool   `json:"temporary"`
	Local     bool   `json:"local"`
}

// hcMachine is a machine as hc reports it (machine ls, machine.connect).
type hcMachine struct {
	Name      string  `json:"name"`
	Host      string  `json:"host"`
	Target    string  `json:"target"`
	State     string  `json:"state"`
	Why       *string `json:"why"`
	Temporary bool    `json:"temporary"`
}

func (m hcMachine) view() machineView {
	v := machineView{Name: m.Name, Label: m.Name, Host: m.Host, State: m.State, Temporary: m.Temporary}
	if v.Host == "" {
		v.Host = m.Target
	}
	if m.Why != nil {
		v.Why = *m.Why
	}
	return v
}

// thisMac: the first machine of the list.
func thisMac() machineView {
	name, _ := os.Hostname()
	name, _, _ = strings.Cut(name, ".")
	return machineView{Label: "This Mac", Host: name, State: "connected", Local: true}
}

// machinesRaw: hc's list of machines, as it is.
func (s *server) machinesRaw(ctx context.Context) (json.RawMessage, error) {
	out, err := run(ctx, 5*time.Second, s.hc, "machine", "ls", "--json")
	if err != nil {
		return nil, hcError(err)
	}
	var ls struct {
		Machines json.RawMessage `json:"machines"`
	}
	if err := json.Unmarshal(out, &ls); err != nil || len(ls.Machines) == 0 || ls.Machines[0] != '[' {
		return nil, fmt.Errorf("hc machine ls said %q", firstLines(string(out), 2, 200))
	}
	return ls.Machines, nil
}

// machines: hc's machines.
func (s *server) machines(ctx context.Context) ([]hcMachine, error) {
	raw, err := s.machinesRaw(ctx)
	if err != nil {
		return nil, err
	}
	var list []hcMachine
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// GET /api/machines: this Mac, then hc's machines ({"machines": [...]};
// with "error" when hc couldn't list them).
func (s *server) machinesHandler(w http.ResponseWriter, r *http.Request) {
	list := []machineView{thisMac()}
	ms, err := s.machines(r.Context())
	for _, m := range ms {
		list = append(list, m.view())
	}
	reply := map[string]any{"machines": list}
	if err != nil {
		reply["error"] = err.Error()
	}
	writeJSON(w, reply)
}

// An ssh destination: user@host, host, an ssh config alias, [v6]; no
// spaces, nothing ssh could take for an option.
var sshTargetRe = regexp.MustCompile(`^[A-Za-z0-9_\[][A-Za-z0-9_.@:%\[\]-]{0,252}$`)

// How long adding a machine may take: dialling, maybe copying hc there.
var machineAddWait = 60 * time.Second

// POST /api/machines {"host": "user@box", "name": "box"}: hc adds the
// machine (name optional: hc picks one), copying hc there when it has none.
func (s *server) machineAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host string `json:"host"`
		Name string `json:"name"`
	}
	if err := decode(r, &req); err != nil || req.Host == "" {
		writeErr(w, http.StatusBadRequest, errors.New(`need {"host": "user@box"}`))
		return
	}
	req.Host, req.Name = strings.TrimSpace(req.Host), strings.TrimSpace(req.Name)
	if !sshTargetRe.MatchString(req.Host) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("%q isn't an ssh destination like user@box", req.Host))
		return
	}
	if req.Name != "" && !machineNameRe.MatchString(req.Name) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("%q isn't a machine name: letters, digits, . _ -", req.Name))
		return
	}
	args := []string{"machine", "add", "--json", "--install"}
	if req.Name != "" {
		args = append(args, "--name", req.Name)
	}
	out, err := runDetached(r.Context(), machineAddWait, s.hc, append(args, req.Host)...)
	if err != nil {
		writeErr(w, http.StatusBadGateway, hcError(err))
		return
	}
	var added hcMachine
	if err := json.Unmarshal(out, &added); err != nil || added.Name == "" {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("hc machine add said %q", firstLines(string(out), 2, 200)))
		return
	}
	view := added.view()
	if view.State == "" {
		view.State = "connecting"
	}
	if ms, err := s.machines(r.Context()); err == nil {
		for _, m := range ms {
			if m.Name == added.Name {
				view = m.view()
			}
		}
	}
	writeJSON(w, map[string]any{"machine": view})
}

// POST /api/machines/remove {"name": "box"}: hc forgets the machine.
func (s *server) machineRemove(w http.ResponseWriter, r *http.Request) {
	name, ok := machineName(w, r)
	if !ok {
		return
	}
	if _, err := run(r.Context(), 30*time.Second, s.hc, "machine", "rm", "--json", name); err != nil {
		err = hcError(err)
		code := http.StatusBadGateway
		var he hcErr
		if errors.As(err, &he) && he.kind == "machine_unknown" {
			code = http.StatusNotFound
		}
		writeErr(w, code, err)
		return
	}
	writeJSON(w, map[string]string{"removed": name})
}

// POST /api/machines/connect {"name": "box"}: hc dials the machine now
// (machine.connect, which has no command of its own: through `hc connect`).
// One that wants a password is logged in to on the Mac.
func (s *server) machineConnect(w http.ResponseWriter, r *http.Request) {
	name, ok := machineName(w, r)
	if !ok {
		return
	}
	var m hcMachine
	err := s.hcCall(r.Context(), 45*time.Second, "machine.connect", map[string]string{"machine": name}, &m)
	var he hcErr
	needsLogin := m.State == "needs_login" || errors.As(err, &he) && he.kind == "needs_login"
	switch {
	case needsLogin:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("%s needs a password; log in on the Mac with: hc machine login %s", name, name)})
	case err != nil && he.kind == "machine_unknown":
		writeErr(w, http.StatusNotFound, err)
	case err != nil:
		writeErr(w, http.StatusBadGateway, err)
	default:
		writeJSON(w, map[string]any{"machine": m.view()})
	}
}

// machineName reads {"name": "box"}; false when it answered with an error.
func machineName(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decode(r, &req); err != nil || !machineNameRe.MatchString(req.Name) {
		writeErr(w, http.StatusBadRequest, errors.New(`need {"name": "box"}: a machine's name`))
		return "", false
	}
	return req.Name, true
}

// hcCall calls a method of hc's API through `hc connect` (one JSON-RPC
// message per line), the result into v.
func (s *server) hcCall(ctx context.Context, timeout time.Duration, method string, params, v any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	cmd := exec.CommandContext(ctx, s.hc, "connect")
	cmd.Stdin = bytes.NewReader(append(req, '\n'))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // no terminal to ask a password on
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	var res struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
			Data    struct {
				Kind string `json:"kind"`
				Hint string `json:"hint"`
			} `json:"data"`
		} `json:"error"`
	}
	line, _, _ := bytes.Cut(bytes.TrimSpace(out), []byte{'\n'})
	if jerr := json.Unmarshal(line, &res); jerr != nil {
		if err == nil {
			err = jerr
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return hcErr{msg: "hc connect: " + msg}
	}
	if res.Error != nil {
		msg := res.Error.Message
		if res.Error.Data.Hint != "" {
			msg += " (" + res.Error.Data.Hint + ")"
		}
		return hcErr{kind: res.Error.Data.Kind, msg: msg}
	}
	return json.Unmarshal(res.Result, v)
}

// runDetached is run without a terminal: in a session of its own with
// stdin closed, nothing it starts (ssh) can ask for a password.
func runDetached(ctx context.Context, timeout time.Duration, bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Env = append(os.Environ(), "SSH_ASKPASS=", "SSH_ASKPASS_REQUIRE=never")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(string(out)) // hc --json prints its error here
		if msg == "" {
			msg = strings.TrimSpace(stderr.String())
		}
		if ctx.Err() == context.DeadlineExceeded {
			msg = strings.TrimSpace(msg + " (gave up after " + timeout.String() + ")")
		}
		if msg == "" {
			msg = err.Error()
		}
		return out, errors.New(msg)
	}
	return out, nil
}
