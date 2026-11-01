package main

// Running at login through launchd, so there is nothing to start by hand:
// install copies this binary to ~/.local/bin and loads a LaunchAgent that
// keeps the server up (it waits for Tailscale by itself).

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const agentLabel = "dev.akbxr.herdr-remote"

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

func plistPath() string {
	return filepath.Join(homeDir(), "Library", "LaunchAgents", agentLabel+".plist")
}

func logPath() string { return filepath.Join(homeDir(), "Library", "Logs", "herdr-remote.log") }

func installedBinary() string { return filepath.Join(homeDir(), ".local", "bin", "herdr-remote") }

func launchdTarget() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// address is where the server listens (or will): -addr, else Tailscale.
func address(flagAddr string) (string, error) {
	if flagAddr != "" {
		return flagAddr, nil
	}
	if ip := tailscaleIP(); ip != "" {
		return net.JoinHostPort(ip, "7710"), nil
	}
	return "", errors.New("Tailscale is off: turn it on (menu bar, or tailscale up), then run this again")
}

func pairNow(flagAddr, token, tokenFile string) error {
	addr, err := address(flagAddr)
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	return printPairing(os.Stdout, "http://"+addr, token, host, tokenFile)
}

func xmlText(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// plist is the LaunchAgent: start at login, restart if it stops, log to
// ~/Library/Logs, and a PATH that finds herdr-app and hc.
func plist(bin string, args []string) string {
	var argv strings.Builder
	for _, a := range append([]string{bin, "serve"}, args...) {
		fmt.Fprintf(&argv, "\n\t\t<string>%s</string>", xmlText(a))
	}
	path := strings.Join([]string{filepath.Dir(bin), "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin"}, ":")
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>%s
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>%s</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, agentLabel, argv.String(), xmlText(path), xmlText(logPath()), xmlText(logPath()))
}

// copyBinary puts this executable at dest, replacing it atomically.
func copyBinary(dest string) error {
	src, err := os.Executable()
	if err != nil {
		return err
	}
	if src, err = filepath.EvalSymlinks(src); err != nil {
		return err
	}
	if d, err := filepath.EvalSymlinks(dest); err == nil && d == src {
		return nil // already running the installed copy
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

func launchctl(args ...string) (string, error) {
	out, err := exec.Command("/bin/launchctl", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func install(flagAddr, token, tokenFile string, awake bool) error {
	bin := installedBinary()
	if err := copyBinary(bin); err != nil {
		return fmt.Errorf("copy to %s: %w", bin, err)
	}
	var args []string
	if flagAddr != "" {
		args = append(args, "-addr", flagAddr)
	}
	if tokenFile != defaultTokenFile() {
		args = append(args, "-token-file", tokenFile)
	}
	if !awake {
		args = append(args, "-awake=false")
	}
	if err := os.MkdirAll(filepath.Dir(plistPath()), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(logPath()), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(plistPath(), []byte(plist(bin, args)), 0o644); err != nil {
		return err
	}
	launchctl("bootout", launchdTarget()+"/"+agentLabel) // an older copy, if loaded
	if out, err := launchctl("bootstrap", launchdTarget(), plistPath()); err != nil {
		return fmt.Errorf("launchctl bootstrap: %s", out)
	}
	fmt.Printf("Installed: herdr-remote now starts at login and restarts if it stops.\n")
	fmt.Printf("  binary  %s\n  agent   %s\n  log     %s\n", bin, plistPath(), logPath())

	addr, err := address(flagAddr)
	if err != nil {
		fmt.Printf("\nIt waits for Tailscale. Once Tailscale is on, run herdr-remote pair to pair a phone.\n")
		return nil
	}
	host, _ := os.Hostname()
	return printPairing(os.Stdout, "http://"+addr, token, host, tokenFile)
}

func uninstall() error {
	out, err := launchctl("bootout", launchdTarget()+"/"+agentLabel)
	loaded := err == nil
	if rmErr := os.Remove(plistPath()); rmErr != nil && !os.IsNotExist(rmErr) {
		return rmErr
	}
	if !loaded && !strings.Contains(out, "No such process") && !strings.Contains(out, "Could not find") {
		fmt.Println("It was not running.")
	}
	fmt.Printf("Uninstalled: herdr-remote no longer starts at login.\n")
	fmt.Printf("Kept: %s and the token (phones stay paired if you install again).\n", installedBinary())
	return nil
}

var pidRe = regexp.MustCompile(`(?m)^\s*pid = (\d+)`)

func status(flagAddr, token string) error {
	if _, err := os.Stat(plistPath()); err != nil {
		fmt.Println("Not installed: run herdr-remote install to start it at login.")
	} else if out, err := launchctl("print", launchdTarget()+"/"+agentLabel); err != nil {
		fmt.Println("Installed but not loaded: run herdr-remote install again.")
	} else if m := pidRe.FindStringSubmatch(out); m != nil {
		fmt.Printf("Running at login (pid %s). Log: %s\n", m[1], logPath())
	} else {
		fmt.Printf("Installed, but not running right now. Log: %s\n", logPath())
	}

	addr, err := address(flagAddr)
	if err != nil {
		fmt.Println(err)
		return nil
	}
	req, _ := http.NewRequest("GET", "http://"+addr+"/api/ping", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		fmt.Printf("Address http://%s: not answering.\n", addr)
		return nil
	}
	defer resp.Body.Close()
	var ping struct {
		Version string
		Awake   bool
	}
	json.NewDecoder(resp.Body).Decode(&ping)
	if resp.StatusCode != 200 {
		fmt.Printf("Address http://%s: answers, but refused this token (%d).\n", addr, resp.StatusCode)
		return nil
	}
	fmt.Printf("Address http://%s: answering (version %s). Pair a phone: herdr-remote pair\n", addr, ping.Version)
	if ping.Awake {
		fmt.Println("Keeping the Mac awake: an agent is working or waiting (or was, minutes ago).")
	}
	return nil
}
