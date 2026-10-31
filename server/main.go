// herdr-remote serves a small HTTP API that lets a phone drive the Herdr
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

const version = "0.1.0"

var (
	terminalRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}(/[A-Za-z0-9_.-]{1,64})?$`)
	commandRe  = regexp.MustCompile(`^[a-z][a-z0-9_-]*(\.[a-z0-9_-]+)+$`)
	keyRe      = regexp.MustCompile(`^[A-Za-z0-9+_-]{1,24}$`)
)

type server struct {
	token     []byte
	tokenFile string
	herdr     string // herdr-app binary
	hc        string // hc binary
	shotDir   string
}

func usage() {
	fmt.Fprintf(os.Stderr, `herdr-remote: control the Herdr app on this Mac from a phone.

Usage:
  herdr-remote install     run at login (launchd), and show a QR code to pair a phone
  herdr-remote pair        show the QR code and a fresh pairing code
  herdr-remote status      whether it runs, where, and if it answers
  herdr-remote uninstall   stop running at login (keeps the token)
  herdr-remote [serve]     serve in this terminal

Flags (install passes them on to the server):
`)
	flag.PrintDefaults()
}

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	addr := flag.String("addr", "", "listen address (default: this Mac's Tailscale IP, port 7710)")
	tokenFile := flag.String("token-file", defaultTokenFile(), "file holding the access token (created if missing)")
	newToken := flag.Bool("new-token", false, "replace the token (phones paired before must pair again)")
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
			run = func() error { return install(*addr, string(token), *tokenFile) }
		}
		if err := run(); err != nil {
			fmt.Fprintln(os.Stderr, "herdr-remote:", err)
			os.Exit(1)
		}
		return
	case "uninstall":
		if err := uninstall(); err != nil {
			fmt.Fprintln(os.Stderr, "herdr-remote:", err)
			os.Exit(1)
		}
		return
	case "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "herdr-remote: unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}

	token, err := loadToken(*tokenFile, *newToken)
	if err != nil {
		log.Fatalf("token: %v", err)
	}
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

	s := &server{token: token, tokenFile: *tokenFile, herdr: findTool("herdr-app"), hc: findTool("hc")}
	if s.shotDir, err = os.MkdirTemp("", "herdr-remote-"); err != nil {
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
	mux.HandleFunc("GET /api/attach", s.authQuery(s.attach)) // WebSocket: a live terminal

	srv := &http.Server{Addr: listen, Handler: logRequests(mux), ReadHeaderTimeout: 10 * time.Second}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("herdr-remote %s listening on http://%s", version, listen)
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
	return filepath.Join(home, ".config", "herdr-remote", "token")
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

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h.ServeHTTP(w, r)
		log.Printf("%s %s %s (%s)", r.RemoteAddr, r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
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
	writeJSON(w, map[string]string{"ok": "true", "version": version, "host": host})
}

// state is what the phone polls: the window (status) and its spaces, tabs
// and terminals (ls), in one request.
func (s *server) state(w http.ResponseWriter, r *http.Request) {
	status, err := run(r.Context(), 10*time.Second, s.herdr, "status")
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("herdr-app status: %w (is the Herdr window open?)", err))
		return
	}
	ls, err := run(r.Context(), 10*time.Second, s.herdr, "ls")
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("herdr-app ls: %w", err))
		return
	}
	writeJSON(w, map[string]json.RawMessage{"status": status, "ls": ls})
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

func validTerminal(t string) bool { return terminalRe.MatchString(t) }

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
	out, err := run(r.Context(), 10*time.Second, s.hc, "read", "--json", "--lines", strconv.Itoa(lines), t)
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
	args := []string{"send", "--json"}
	if req.Force {
		args = append(args, "--force")
	}
	args = append(args, req.Terminal, req.Text)
	out, err := run(r.Context(), 40*time.Second, s.hc, args...)
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
	out, err := run(r.Context(), 10*time.Second, s.hc, append([]string{"keys", "--json", req.Terminal}, req.Keys...)...)
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
	out, err := run(r.Context(), 15*time.Second, s.hc, "interrupt", "--json", req.Terminal)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeRaw(w, out)
}
