// herdsman serves a small HTTP API that lets a phone drive the Herdr
// app on this Mac. It runs the herdr-app and hc command-line tools (never a
// shell) and needs a bearer token on every /api request.
//
// Anyone holding the token can type into your terminals: listen on your
// Tailscale address (or localhost behind an SSH tunnel), not the internet.
package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// version is bumped by release-please; release builds also set it with -ldflags "-X main.version=…".
var version = "0.3.1" // x-release-please-version

var (
	terminalRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}(/[A-Za-z0-9_.-]{1,64})?$`)
	commandRe  = regexp.MustCompile(`^[a-z][a-z0-9_-]*(\.[a-z0-9_-]+)+$`)
	keyRe      = regexp.MustCompile(`^[A-Za-z0-9+_-]{1,24}$`)
)

type server struct {
	token     []byte
	tokenFile string
	events    *eventHub  // agents' status changes, for the phone's notifications
	awake     *keepAwake // nil with -awake=false
	herdr     string     // herdr-app binary
	hc        string     // hc binary
	herdrSock string     // the herdr CLI's default session's API socket; "": none
	shotDir   string
	uploadDir string // where uploads for herdr CLI panes go ("": ~/Library/Application Support/herdsman/uploads)

	// For tests; empty or nil: the real ones.
	home         string                 // where recent folders and projects are looked for
	programsFile string                 // Herdr's programs.json
	hasTool      func(name string) bool // whether a program is installed
	// The transcript the agent in a terminal has open (sessions.go).
	transcriptOf func(ctx context.Context, terminal string) string

	titles titleCache // the session titles of the agents' terminals (titles.go)
}

func usage() {
	fmt.Fprintf(os.Stderr, `herdsman: look after the coding agents in Herdr (the app and the CLI) on this Mac from a phone.

Usage:
  herdsman install     run at login (launchd), and show a QR code to pair a phone
  herdsman pair        show the QR code and a fresh pairing code
  herdsman status      whether it runs, where, and if it answers
  herdsman uninstall   stop running at login (keeps the token)
  herdsman [serve]     serve in this terminal

Flags (install passes them on to the server):
`)
	flag.PrintDefaults()
}

func main() {
	announceMigration(homeDir()) // before the token is read from its folder
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	addr := flag.String("addr", "", "listen address (default: this Mac's Tailscale IP, port 7710)")
	tokenFile := flag.String("token-file", defaultTokenFile(), "file holding the access token (created if missing)")
	newToken := flag.Bool("new-token", false, "replace the token (phones paired before must pair again)")
	awake := flag.Bool("awake", true, "keep the Mac from sleeping on its own while agents work (-awake=false to let it)")
	flag.Usage = usage
	flag.CommandLine.Parse(args)
	log.SetFlags(log.LstdFlags)

	switch cmd {
	case "serve":
	case "pair", "status", "install":
		token, err := loadToken(*tokenFile, *newToken)
		if err != nil {
			log.Fatalf("token: %v", err)
		}
		var run func() error
		switch cmd {
		case "pair":
			run = func() error { return pairNow(*addr, string(token), *tokenFile) }
		case "status":
			run = func() error { return status(*addr, string(token)) }
		case "install":
			run = func() error { return install(*addr, string(token), *tokenFile, *awake) }
		}
		if err := run(); err != nil {
			fmt.Fprintln(os.Stderr, "herdsman:", err)
			os.Exit(1)
		}
		return
	case "uninstall":
		if err := uninstall(); err != nil {
			fmt.Fprintln(os.Stderr, "herdsman:", err)
			os.Exit(1)
		}
		return
	case "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "herdsman: unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}

	token, err := loadToken(*tokenFile, *newToken)
	if err != nil {
		log.Fatalf("token: %v", err)
	}
	// Started in a terminal of hc's, the server still speaks for the phone:
	// with that terminal's address hc would sign what it types as from there
	// (<hc from="t7">…</hc>) and make what it starts its children.
	os.Unsetenv("HC_TERMINAL_ID")
	os.Unsetenv("HC_TERMINAL_SOCKET")
	listen := *addr
	if listen == "" {
		// Under launchd this starts at login, often before Tailscale is up: wait for it.
		ip := tailscaleIP()
		if ip == "" {
			log.Print("waiting for Tailscale (or pass -addr, e.g. -addr 127.0.0.1:7710 behind an SSH tunnel)")
			for ip == "" {
				time.Sleep(3 * time.Second)
				ip = tailscaleIP()
			}
		}
		listen = net.JoinHostPort(ip, "7710")
	}
	host, _ := os.Hostname()

	s := &server{token: token, tokenFile: *tokenFile, herdr: findTool("herdr-app"), hc: findTool("hc"), herdrSock: defaultHerdrSock()}
	if s.shotDir, err = os.MkdirTemp("", "herdsman-"); err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(s.shotDir)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/pair", s.pair) // trades a pairing code for the token
	mux.HandleFunc("GET /api/ping", s.auth(s.ping))
	mux.HandleFunc("GET /api/state", s.auth(s.state))
	mux.HandleFunc("GET /api/shot", s.auth(s.shot))
	mux.HandleFunc("POST /api/do", s.auth(s.do))
	mux.HandleFunc("GET /api/read", s.auth(s.read))
	mux.HandleFunc("POST /api/send", s.auth(s.send))
	mux.HandleFunc("POST /api/keys", s.auth(s.keys))
	mux.HandleFunc("POST /api/interrupt", s.auth(s.interrupt))
	mux.HandleFunc("GET /api/attach", s.authQuery(s.attach))   // WebSocket: a live terminal
	mux.HandleFunc("POST /api/upload", s.auth(s.upload))       // an attachment, placed beside a terminal
	mux.HandleFunc("GET /api/events", s.auth(s.eventsHandler)) // long poll: agents' status changes
	mux.HandleFunc("POST /api/answer", s.auth(s.answer))       // allow, deny or reply to a waiting agent
	mux.HandleFunc("GET /api/chat", s.auth(s.chat))            // the agent's conversation, from its transcript
	mux.HandleFunc("GET /api/image", s.auth(s.image))          // a picture in the conversation (chatimage.go)
	mux.HandleFunc("GET /api/prompt", s.auth(s.promptHandler)) // the menu a waiting agent shows
	mux.HandleFunc("GET /api/changes", s.auth(s.changes))      // git status and diff of the agent's folder
	mux.HandleFunc("GET /api/commands", s.auth(s.commands))    // "/" in the message box
	mux.HandleFunc("GET /api/files", s.auth(s.files))          // "@" in the message box
	mux.HandleFunc("GET /api/folders", s.auth(s.folders))      // where a new task can start
	mux.HandleFunc("GET /api/agents", s.auth(s.agentsHandler)) // what a new task can run
	mux.HandleFunc("POST /api/task", s.auth(s.task))           // a new tab running an agent on a task
	mux.HandleFunc("GET /api/sessions", s.auth(s.sessions))    // earlier sessions an agent can resume
	mux.HandleFunc("POST /api/resume", s.auth(s.resume))       // a new tab resuming one
	mux.HandleFunc("GET /api/models", s.auth(s.modelsHandler)) // the models and thinking levels an agent offers
	mux.HandleFunc("POST /api/session", s.auth(s.session))     // switch a running agent's model or thinking level

	// The machines tasks run on (machines.go).
	mux.HandleFunc("GET /api/machines", s.auth(s.machinesHandler))         // this Mac and hc's machines
	mux.HandleFunc("POST /api/machines", s.auth(s.machineAdd))             // hc machine add
	mux.HandleFunc("POST /api/machines/remove", s.auth(s.machineRemove))   // hc machine rm
	mux.HandleFunc("POST /api/machines/connect", s.auth(s.machineConnect)) // dial one now

	// The Changes view's actions (commit.go).
	mux.HandleFunc("POST /api/commit", s.auth(s.commit))               // commit the files picked
	mux.HandleFunc("GET /api/commit/suggest", s.auth(s.commitSuggest)) // a message from the changed files' names
	mux.HandleFunc("POST /api/discard", s.auth(s.discard))             // one file back as the last commit has it

	// The herdr CLI's tabs and panes (herdrcontrol.go).
	mux.HandleFunc("POST /api/herdr", s.auth(s.herdrControl))

	s.events = newEventHub(s.hc)
	go s.events.run(context.Background())
	go s.herdrEvents(context.Background()) // the herdr CLI's agents, into the same hub
	if *awake {
		s.awake = &keepAwake{hub: s.events, bin: "/usr/bin/caffeinate", linger: awakeLinger}
		go s.awake.run(context.Background())
	}

	srv := &http.Server{Addr: listen, Handler: logRequests(mux), ReadHeaderTimeout: 10 * time.Second}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("herdsman %s listening on http://%s", version, listen)
	// The QR code is for a person at a terminal, not for launchd's log.
	if isTerminal(os.Stdout) {
		if err := printPairing(os.Stdout, "http://"+listen, string(token), host, *tokenFile); err != nil {
			log.Printf("pairing code: %v", err)
		}
	}
	log.Fatal(srv.Serve(ln))
}

func defaultTokenFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "herdsman", "token")
}

// loadToken reads the token, making a random one when the file is missing
// (or when asked for a new one).
func loadToken(path string, fresh bool) ([]byte, error) {
	if !fresh {
		if b, err := os.ReadFile(path); err == nil {
			if t := strings.TrimSpace(string(b)); len(t) >= 32 {
				return []byte(t), nil
			}
		}
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	t := hex.EncodeToString(raw)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(t+"\n"), 0o600); err != nil {
		return nil, err
	}
	return []byte(t), nil
}

// findTool looks on PATH, then in ~/.local/bin (launchd and ssh sessions
// often have a short PATH).
func findTool(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".local", "bin", name)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return name
}

// tailscaleIP returns this machine's Tailscale IPv4, or "" while Tailscale
// is off. It asks the tailscale CLI: other VPNs use the same 100.64.0.0/10
// range (Cloudflare WARP takes 100.96.x.x), so an interface with such an
// address isn't necessarily Tailscale's.
func tailscaleIP() string {
	for _, name := range []string{"tailscale", "/usr/local/bin/tailscale", "/opt/homebrew/bin/tailscale",
		"/Applications/Tailscale.app/Contents/MacOS/Tailscale"} {
		bin, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		out, err := exec.CommandContext(ctx, bin, "ip", "-4").Output()
		cancel()
		if err != nil {
			return "" // installed but not running
		}
		ip := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		if net.ParseIP(ip) != nil {
			return ip
		}
		return ""
	}
	return cgnatIP() // no CLI: best guess from the interfaces
}

// cgnatIP returns the first interface address in 100.64.0.0/10.
func cgnatIP() string {
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && cgnat.Contains(n.IP) {
			return n.IP.String()
		}
	}
	return ""
}

func (s *server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := []byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if subtle.ConstantTimeCompare(got, s.token) != 1 {
			time.Sleep(500 * time.Millisecond) // slow down guessing
			writeErr(w, http.StatusUnauthorized, errors.New("wrong or missing token"))
			return
		}
		h(w, r)
	}
}

// authQuery also takes the token from ?token=, for the WebSocket (a WebView
// can't set its headers). Request logs print the path only, never the query.
func (s *server) authQuery(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if t := r.URL.Query().Get("token"); t != "" && r.Header.Get("Authorization") == "" {
			r.Header.Set("Authorization", "Bearer "+t)
		}
		s.auth(h)(w, r)
	}
}

// statusWriter remembers the status code a handler sent.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) { w.code = code; w.ResponseWriter.WriteHeader(code) }

// Unwrap lets the WebSocket upgrade reach the real connection.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func logRequests(h http.Handler) http.Handler {
	// The phone polls these all the time; log them only when they fail.
	quiet := map[string]bool{"/api/state": true, "/api/events": true, "/api/read": true, "/api/shot": true, "/api/chat": true, "/api/image": true, "/api/commands": true, "/api/files": true, "/api/prompt": true, "/api/changes": true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		h.ServeHTTP(sw, r)
		if quiet[r.URL.Path] && sw.code < 400 {
			return
		}
		log.Printf("%s %s %s %d (%s)", r.RemoteAddr, r.Method, r.URL.Path, sw.code, time.Since(start).Round(time.Millisecond))
	})
}

// run executes a tool with argv (no shell) and returns its stdout.
func run(ctx context.Context, timeout time.Duration, bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(string(out))
		}
		if msg == "" {
			msg = err.Error()
		}
		return out, errors.New(msg)
	}
	return out, nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeRaw(w http.ResponseWriter, raw []byte) {
	if !json.Valid(raw) {
		writeJSON(w, map[string]string{"text": string(raw)})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func decode(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(v)
}

func (s *server) ping(w http.ResponseWriter, r *http.Request) {
	host, _ := os.Hostname()
	writeJSON(w, map[string]any{"ok": "true", "version": version, "host": host, "awake": s.awake != nil && s.awake.on()})
}

// state is what the phone polls: the window (status) and its spaces, tabs
// and terminals (ls), in one request; with the herdr CLI running, its
// session too (herdr: session.snapshot's snapshot). Either one alone is
// enough: it fails only when neither the Herdr app nor the herdr CLI answers.
func (s *server) state(w http.ResponseWriter, r *http.Request) {
	machines := make(chan json.RawMessage, 1) // hc's machines, meanwhile; left out when hc can't say
	go func() {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		raw, _ := s.machinesRaw(ctx)
		machines <- raw
	}()
	herdrSnap := make(chan json.RawMessage, 1) // the herdr CLI's session; left out when it doesn't answer
	go func() {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		raw, _ := s.herdrSnapshotRaw(ctx)
		herdrSnap <- raw
	}()
	reply := map[string]json.RawMessage{}
	appErr := func() error {
		status, err := run(r.Context(), 10*time.Second, s.herdr, "status")
		if err != nil {
			return fmt.Errorf("herdr-app status: %w (is the Herdr window open?)", err)
		}
		ls, err := run(r.Context(), 10*time.Second, s.herdr, "ls")
		if err != nil {
			return fmt.Errorf("herdr-app ls: %w", err)
		}
		reply["status"], reply["ls"] = status, ls
		return nil
	}()
	if h := <-herdrSnap; h != nil {
		reply["herdr"] = h
	} else if appErr != nil {
		writeErr(w, http.StatusBadGateway, appErr)
		return
	}
	if m := <-machines; m != nil {
		reply["machines"] = m
	}
	// terminal -> the title of the session its agent works in ("herdr:wF:p2": "Fix the nav")
	if t := s.sessionTitles(r.Context()); len(t) > 0 {
		if raw, err := json.Marshal(t); err == nil {
			reply["titles"] = raw
		}
	}
	writeJSON(w, reply)
}

// shot sends the window's last frame as a JPEG, scaled down for a phone.
func (s *server) shot(w http.ResponseWriter, r *http.Request) {
	maxSide, _ := strconv.Atoi(r.URL.Query().Get("max"))
	if maxSide < 200 || maxSide > 4000 {
		maxSide = 1600
	}
	png := filepath.Join(s.shotDir, fmt.Sprintf("shot-%d.png", time.Now().UnixNano()))
	jpg := strings.TrimSuffix(png, ".png") + ".jpg"
	defer os.Remove(png)
	defer os.Remove(jpg)
	if _, err := run(r.Context(), 10*time.Second, s.herdr, "shot", png); err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("herdr-app shot: %w", err))
		return
	}
	if _, err := run(r.Context(), 10*time.Second, "/usr/bin/sips", "-s", "format", "jpeg", "-s", "formatOptions", "75",
		"-Z", strconv.Itoa(maxSide), png, "--out", jpg); err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("sips: %w", err))
		return
	}
	b, err := os.ReadFile(jpg)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}

// do runs one app command: {"command": "tab.select", "args": {"id": "tb…"}}.
func (s *server) do(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Command string          `json:"command"`
		Args    json.RawMessage `json:"args"`
	}
	if err := decode(r, &req); err != nil || !commandRe.MatchString(req.Command) {
		writeErr(w, http.StatusBadRequest, errors.New("need {command: \"area.name\", args?: {…}}"))
		return
	}
	args := []string{"do", req.Command}
	if len(req.Args) > 0 && string(req.Args) != "null" {
		var obj map[string]any
		if err := json.Unmarshal(req.Args, &obj); err != nil {
			writeErr(w, http.StatusBadRequest, errors.New("args must be a JSON object"))
			return
		}
		args = append(args, string(req.Args))
	}
	out, err := run(r.Context(), 15*time.Second, s.herdr, args...)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeRaw(w, out)
}

// validTerminal: hc's "t12" or "machine/t12", or a herdr CLI pane "herdr:wF:p2".
func validTerminal(t string) bool {
	if _, ok := herdrPane(t); ok {
		return true
	}
	return terminalRe.MatchString(t)
}

// read returns a terminal's screen and some history.
func (s *server) read(w http.ResponseWriter, r *http.Request) {
	t := r.URL.Query().Get("terminal")
	if !validTerminal(t) {
		writeErr(w, http.StatusBadRequest, errors.New("bad terminal"))
		return
	}
	lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	if lines < 0 || lines > 2000 {
		lines = 200
	}
	out, err := s.backend(t).read(r.Context(), t, lines)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeRaw(w, out)
}

// send pastes text into a terminal and presses Enter.
func (s *server) send(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Terminal string `json:"terminal"`
		Text     string `json:"text"`
		Force    bool   `json:"force"`
	}
	if err := decode(r, &req); err != nil || !validTerminal(req.Terminal) || strings.TrimSpace(req.Text) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("need {terminal, text}"))
		return
	}
	out, err := s.backend(req.Terminal).send(r.Context(), req.Terminal, req.Text, req.Force)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeRaw(w, out)
}

// keys presses named keys: {"terminal": "t7", "keys": ["esc"]}.
func (s *server) keys(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Terminal string   `json:"terminal"`
		Keys     []string `json:"keys"`
	}
	if err := decode(r, &req); err != nil || !validTerminal(req.Terminal) || len(req.Keys) == 0 || len(req.Keys) > 32 {
		writeErr(w, http.StatusBadRequest, errors.New("need {terminal, keys: [...]}"))
		return
	}
	for _, k := range req.Keys {
		if !keyRe.MatchString(k) {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("bad key %q", k))
			return
		}
	}
	out, err := s.backend(req.Terminal).keys(r.Context(), req.Terminal, req.Keys, 10*time.Second)
	if herdrCode(err) == "invalid_key" {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeRaw(w, out)
}

func (s *server) interrupt(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Terminal string `json:"terminal"`
	}
	if err := decode(r, &req); err != nil || !validTerminal(req.Terminal) {
		writeErr(w, http.StatusBadRequest, errors.New("need {terminal}"))
		return
	}
	out, err := s.backend(req.Terminal).interrupt(r.Context(), req.Terminal)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeRaw(w, out)
}
