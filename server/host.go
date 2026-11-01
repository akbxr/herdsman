package main

// Where a terminal runs: this Mac, or another machine hc reaches over ssh
// (`hc machine add`). A terminal there has the address "machine/t7"; hc's
// terminal commands take it as they are, but what the server does beside
// them (git, reading transcripts, listing folders) has to happen on that
// machine: through `hc exec --on machine` there, plain exec here.
//
// Remote round trips cost time (an ssh hop each), so the remote side batches:
// several commands in one `hc exec` (batch), a file's size with its bytes
// (readFrom), several paths' kinds and sizes in one call (stat). The local
// side stays what it was: exec and the os package, no shell, no hc.
//
// On the machine, argv runs as given (`hc exec` takes several words as a
// program and its arguments, no shell); the few shell scripts here get their
// values as positional arguments ("$1", "$@"), never pasted into the script.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// host runs things on one machine.
type host struct {
	machine string // "" this Mac
	hc      string // the hc binary, for another machine
	home    string // this Mac: a home for the tests ("" the real one)
}

// on: the host of a machine ("" this Mac).
func (s *server) on(machine string) host {
	if machine == "" {
		return host{home: s.home}
	}
	return host{machine: machine, hc: s.hc}
}

// hostOf: the host a terminal runs on.
func (s *server) hostOf(terminal string) host {
	m, _ := splitTerminal(terminal)
	return s.on(m)
}

// splitTerminal: "box/t7" is t7 on box; "t7" is on this Mac ("").
func splitTerminal(addr string) (machine, local string) {
	if m, t, ok := strings.Cut(addr, "/"); ok {
		return m, t
	}
	return "", addr
}

func (h host) remote() bool { return h.machine != "" }

// machineNameRe: what hc calls a machine (the part before "/" in an address).
var machineNameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,63}$`)

func validMachine(m string) bool { return m == "" || machineNameRe.MatchString(m) }

// The most hc exec hands back of what a program wrote (8 MB), less a margin.
const remoteReadMax = 6 << 20

// execResult is what a program on a machine did.
type execResult struct {
	stdout, stderr []byte
	code           int // -1: a signal ended it
	timedOut       bool
	truncated      bool
}

// deadline: the time left in ctx, else def.
func deadline(ctx context.Context, def time.Duration) time.Duration {
	if d, ok := ctx.Deadline(); ok {
		return max(time.Until(d), time.Second)
	}
	return def
}

// execArgs: hc's arguments that run argv on the machine, in dir ("" its
// home), with env on top of its environment.
func (h host) execArgs(timeout time.Duration, dir string, env []string, argv []string) []string {
	// It ends there a little before the time is up here, so that what it
	// wrote so far still comes back.
	secs := int(math.Floor((timeout - 1500*time.Millisecond).Seconds()))
	secs = min(max(secs, 1), 600)
	args := []string{"exec", "--json", "--on", h.machine, "--timeout", strconv.Itoa(secs)}
	if dir != "" {
		args = append(args, "--cwd", dir)
	}
	for _, e := range env {
		args = append(args, "--env", e)
	}
	// One word with spaces or shell signs goes to that machine's shell as a
	// line: a lone program name that isn't plain runs through env instead.
	if len(argv) == 1 && strings.ContainsFunc(argv[0], func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-+", r))
	}) {
		argv = []string{"env", argv[0]}
	}
	return append(append(args, "--"), argv...)
}

// exec runs argv on the machine through `hc exec`; a nonzero exit is a
// result, an error is hc's (no such machine, not connected, no such program).
func (h host) exec(ctx context.Context, dir string, env []string, argv ...string) (execResult, error) {
	timeout := deadline(ctx, 60*time.Second)
	out, err := run(ctx, timeout, h.hc, h.execArgs(timeout, dir, env, argv)...)
	if err != nil {
		return execResult{}, fmt.Errorf("%s: %w", h.machine, hcError(err))
	}
	var r struct {
		Code      *int   `json:"code"`
		Stdout    string `json:"stdout"`
		Stderr    string `json:"stderr"`
		TimedOut  bool   `json:"timed_out"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return execResult{}, fmt.Errorf("%s: hc exec said %q", h.machine, firstLines(string(out), 2, 200))
	}
	res := execResult{code: -1, timedOut: r.TimedOut, truncated: r.Truncated}
	if r.Code != nil {
		res.code = *r.Code
	}
	if res.stdout, err = base64.StdEncoding.DecodeString(r.Stdout); err != nil {
		return execResult{}, fmt.Errorf("%s: hc exec: %w", h.machine, err)
	}
	res.stderr, _ = base64.StdEncoding.DecodeString(r.Stderr)
	return res, nil
}

// failure: a remote program's nonzero exit or timeout as an error, with
// what it said.
func (r execResult) failure() error {
	if r.code == 0 && !r.timedOut {
		return nil
	}
	msg := strings.TrimSpace(string(r.stderr))
	if msg == "" {
		msg = strings.TrimSpace(string(r.stdout))
	}
	switch {
	case r.timedOut:
		msg = strings.TrimSpace(msg + "\n(it took too long and was stopped)")
	case msg == "" && r.code < 0:
		msg = "ended by a signal"
	case msg == "":
		msg = "exit status " + strconv.Itoa(r.code)
	}
	return errors.New(msg)
}

// hcError: an error of hc's, with the message it printed as JSON
// ({"error": {"kind", "message", "hint"}}) when it did.
func hcError(err error) error {
	var e struct {
		Error *struct {
			Kind    string `json:"kind"`
			Message string `json:"message"`
			Hint    string `json:"hint"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(err.Error()), &e) != nil || e.Error == nil || e.Error.Message == "" {
		return hcErr{msg: err.Error()}
	}
	msg := e.Error.Message
	if e.Error.Hint != "" {
		msg += " (" + e.Error.Hint + ")"
	}
	return hcErr{kind: e.Error.Kind, msg: msg}
}

// hcErr is an error hc reported, with its stable kind (machine_unknown, …).
type hcErr struct{ kind, msg string }

func (e hcErr) Error() string { return e.msg }

// run runs argv in dir and returns what it wrote, as exec's Output does:
// an error for a nonzero exit.
func (h host) run(ctx context.Context, dir string, argv ...string) ([]byte, error) {
	if !h.remote() {
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir = dir
		return cmd.Output()
	}
	r, err := h.exec(ctx, dir, nil, argv...)
	if err != nil {
		return nil, err
	}
	return r.stdout, r.failure()
}

// git runs git in dir.
func (h host) git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if !h.remote() {
		return git(ctx, dir, args...)
	}
	return h.run(ctx, "", append([]string{"git", "-C", dir}, args...)...)
}

// gitDo runs git in dir for a change (see gitDo in commit.go).
func (h host) gitDo(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if !h.remote() {
		return gitDo(ctx, dir, args...)
	}
	r, err := h.exec(ctx, "", []string{"GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true"}, append([]string{"git", "-C", dir}, args...)...)
	if err != nil {
		return nil, err
	}
	if r.code == 0 && !r.timedOut {
		return r.stdout, nil
	}
	msg := strings.TrimSpace(strings.TrimSpace(string(r.stdout)) + "\n" + strings.TrimSpace(string(r.stderr)))
	if r.timedOut {
		msg = strings.TrimSpace(msg + "\n(git took too long and was stopped)")
	}
	if msg == "" {
		msg = r.failure().Error()
	}
	if len(msg) > maxGitError {
		msg = "…" + msg[len(msg)-maxGitError:]
	}
	return r.stdout, errors.New(msg)
}

// stepOut is what one command of a batch wrote, and whether it exited 0.
type stepOut struct {
	out []byte
	ok  bool
}

// The script that runs a batch: each command is its word count and its
// words; after each, a line with the marker and its exit code. The words are
// only ever referenced ("${3}"), never parsed.
const batchScript = `t=$1; shift
while [ $# -gt 0 ]; do
  n=$1; shift
  c=; i=1
  while [ "$i" -le "$n" ]; do c="$c \"\${$i}\""; i=$((i+1)); done
  (eval "exec $c") </dev/null; r=$?
  printf '\n%s %s\n' "$t" "$r"
  shift "$n"
done`

// batch runs commands one after the other in dir: on another machine in a
// single round trip. A command that couldn't run, or was cut off by the
// time limit, is not ok.
func (h host) batch(ctx context.Context, dir string, steps [][]string) ([]stepOut, error) {
	res := make([]stepOut, len(steps))
	if len(steps) == 0 {
		return res, nil
	}
	if !h.remote() {
		for i, st := range steps {
			cmd := exec.CommandContext(ctx, st[0], st[1:]...)
			cmd.Dir = dir
			out, err := cmd.Output()
			res[i] = stepOut{out, err == nil}
		}
		return res, nil
	}
	raw := make([]byte, 8)
	rand.Read(raw)
	token := "--" + hex.EncodeToString(raw) + "--"
	argv := []string{"sh", "-c", batchScript, "sh", token}
	for _, st := range steps {
		argv = append(append(argv, strconv.Itoa(len(st))), st...)
	}
	r, err := h.exec(ctx, dir, nil, argv...)
	if err != nil {
		return nil, err
	}
	splitBatch(r.stdout, token, res)
	return res, nil
}

// splitBatch: each command's output, up to the line with the marker after it.
func splitBatch(out []byte, token string, res []stepOut) {
	mark := []byte("\n" + token + " ")
	for i := range res {
		at := bytes.Index(out, mark)
		if at < 0 {
			return // cut off: the rest didn't run (or didn't finish)
		}
		rest := out[at+len(mark):]
		nl := bytes.IndexByte(rest, '\n')
		if nl < 0 {
			return
		}
		res[i] = stepOut{out[:at:at], string(rest[:nl]) == "0"}
		out = rest[nl+1:]
	}
}

// fileStat is a path's kind on a machine: f a regular file, d a folder, o
// something else, "" nothing there (links followed; link: it is one).
type fileStat struct {
	kind  string
	link  bool
	size  int64
	mtime time.Time
}

func (f fileStat) isDir() bool  { return f.kind == "d" }
func (f fileStat) isFile() bool { return f.kind == "f" }

// One line per path: kind, link, size, mtime (seconds). BSD stat has -f,
// GNU and busybox -c.
const statScript = `for f; do
  k=-; l=0; s=0; m=0
  [ -L "$f" ] && l=1
  if [ -d "$f" ]; then k=d; elif [ -f "$f" ]; then k=f; elif [ -e "$f" ]; then k=o; fi
  if [ "$k" = f ]; then
    s=$(wc -c < "$f" 2>/dev/null | tr -d ' ')
    m=$(stat -c %Y -- "$f" 2>/dev/null || stat -f %m -- "$f" 2>/dev/null || echo 0)
  fi
  printf '%s %s %s %s\n' "$k" "$l" "${s:-0}" "${m:-0}"
done`

// stat: what is at each path (absolute paths).
func (h host) stat(ctx context.Context, paths ...string) ([]fileStat, error) {
	out := make([]fileStat, len(paths))
	if len(paths) == 0 {
		return out, nil
	}
	if !h.remote() {
		for i, p := range paths {
			if l, err := os.Lstat(p); err == nil && l.Mode()&os.ModeSymlink != 0 {
				out[i].link = true
			}
			st, err := os.Stat(p)
			switch {
			case err != nil:
			case st.IsDir():
				out[i].kind = "d"
			case st.Mode().IsRegular():
				out[i] = fileStat{kind: "f", link: out[i].link, size: st.Size(), mtime: st.ModTime()}
			default:
				out[i].kind = "o"
			}
		}
		return out, nil
	}
	b, err := h.run(ctx, "", append([]string{"sh", "-c", statScript, "sh"}, paths...)...)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != len(paths) {
		return nil, fmt.Errorf("%s: stat gave %d lines for %d paths", h.machine, len(lines), len(paths))
	}
	for i, l := range lines {
		f := strings.Fields(l)
		if len(f) != 4 {
			return nil, fmt.Errorf("%s: stat said %q", h.machine, l)
		}
		if f[0] != "-" {
			out[i].kind = f[0]
		}
		out[i].link = f[1] == "1"
		out[i].size, _ = strconv.ParseInt(f[2], 10, 64)
		if sec, _ := strconv.ParseInt(f[3], 10, 64); sec > 0 {
			out[i].mtime = time.Unix(sec, 0)
		}
	}
	return out, nil
}

// statFile: a file's size and mtime (links followed), as os.Stat says it.
func (h host) statFile(ctx context.Context, p string) (int64, time.Time, error) {
	if !h.remote() {
		st, err := os.Stat(p)
		if err != nil {
			return 0, time.Time{}, err
		}
		return st.Size(), st.ModTime(), nil
	}
	sts, err := h.stat(ctx, p)
	if err != nil {
		return 0, time.Time{}, err
	}
	if sts[0].kind == "" {
		return 0, time.Time{}, fmt.Errorf("%s: %s: %w", h.machine, p, os.ErrNotExist)
	}
	return sts[0].size, sts[0].mtime, nil
}

// isDir: whether p is a folder on the machine.
func (h host) isDir(ctx context.Context, p string) bool {
	if !h.remote() {
		return isDir(p)
	}
	_, err := h.run(ctx, "", "test", "-d", p)
	return err == nil
}

// Prints the file's size, a newline, then at most $3 of its bytes: from
// byte $2 (1-based), or the last -$2.
const readScript = `s=$(wc -c < "$1") || exit 1
printf '%s\n' $s
if [ "$2" -lt 0 ]; then tail -c "${2#-}" "$1"; else tail -c +"$2" "$1"; fi | head -c "$3"`

// readFrom reads at most n bytes of a file from offset from (from < 0: the
// last -from bytes), and the file's size now.
func (h host) readFrom(ctx context.Context, path string, from, n int64) ([]byte, int64, error) {
	if !h.remote() {
		f, err := os.Open(path)
		if err != nil {
			return nil, 0, err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return nil, 0, err
		}
		size := st.Size()
		if from < 0 {
			from = max(size+from, 0)
		}
		if from >= size {
			return nil, size, nil
		}
		buf := make([]byte, min(n, size-from))
		k, err := f.ReadAt(buf, from)
		if k == len(buf) {
			err = nil
		}
		return buf[:k], size, err
	}
	pos := strconv.FormatInt(from+1, 10)
	if from < 0 {
		pos = strconv.FormatInt(from, 10)
	}
	b, err := h.run(ctx, "", "sh", "-c", readScript, "sh", path, pos, strconv.FormatInt(min(n, remoteReadMax), 10))
	if err != nil {
		return nil, 0, err
	}
	line, data, ok := bytes.Cut(b, []byte{'\n'})
	size, perr := strconv.ParseInt(strings.TrimSpace(string(line)), 10, 64)
	if !ok || perr != nil {
		return nil, 0, fmt.Errorf("%s: couldn't read %s", h.machine, path)
	}
	return data, size, nil
}

// readAll reads a file from offset from to its end (in pieces from another
// machine), but no further than until bytes; the size it had.
func (h host) readAll(ctx context.Context, path string, from, until int64) ([]byte, int64, error) {
	if !h.remote() {
		return h.readFrom(ctx, path, from, until)
	}
	var all []byte
	for {
		b, size, err := h.readFrom(ctx, path, from+int64(len(all)), min(remoteReadMax, until-int64(len(all))))
		if err != nil {
			return nil, 0, err
		}
		all = append(all, b...)
		if len(b) == 0 || from+int64(len(all)) >= size || int64(len(all)) >= until {
			return all, size, nil
		}
	}
}

// The homes of the machines, asked once.
var homes = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

// homeDir: the user's home on the machine.
func (h host) homeDir(ctx context.Context) (string, error) {
	if !h.remote() {
		if h.home != "" {
			return h.home, nil
		}
		return os.UserHomeDir()
	}
	homes.Lock()
	home, ok := homes.m[h.machine]
	homes.Unlock()
	if ok {
		return home, nil
	}
	b, err := h.run(ctx, "", "sh", "-c", `printf '%s' "$HOME"`)
	if err != nil {
		return "", err
	}
	home = strings.TrimSpace(string(b))
	if !strings.HasPrefix(home, "/") {
		return "", fmt.Errorf("%s: no home folder (HOME=%q)", h.machine, home)
	}
	homes.Lock()
	homes.m[h.machine] = home
	homes.Unlock()
	return home, nil
}

// dirEntry is one thing in a folder.
type dirEntry struct {
	name  string
	dir   bool
	size  int64
	mtime time.Time
}

// listDir: what a folder holds (links followed), no hidden names.
func (h host) listDir(ctx context.Context, dir string) ([]dirEntry, error) {
	if !h.remote() {
		ents, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		var out []dirEntry
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			st, err := os.Stat(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			out = append(out, dirEntry{e.Name(), st.IsDir(), st.Size(), st.ModTime()})
		}
		return out, nil
	}
	// The names first (NUL-separated), then a stat line each.
	b, err := h.run(ctx, "", "sh", "-c", `cd "$1" || exit 1
for f in *; do [ -e "$f" ] || [ -L "$f" ] && printf '%s\0' "$f"; done`, "sh", dir)
	if err != nil {
		return nil, err
	}
	var names, paths []string
	for _, n := range strings.Split(string(b), "\x00") {
		if n != "" {
			names = append(names, n)
			paths = append(paths, filepath.Join(dir, n))
		}
	}
	sts, err := h.stat(ctx, paths...)
	if err != nil {
		return nil, err
	}
	var out []dirEntry
	for i, st := range sts {
		if st.kind != "" {
			out = append(out, dirEntry{names[i], st.isDir(), st.size, st.mtime})
		}
	}
	return out, nil
}
