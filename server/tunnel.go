package main

// A Cloudflare tunnel: https://<hostname> reaches this Mac from anywhere,
// without Tailscale on the phone. `herdsman tunnel herdr.example.com` sets
// it up on your own Cloudflare account (the domain must be on it):
// cloudflared logs in once, makes a tunnel named after the hostname, points
// the hostname's DNS at it, and a LaunchAgent keeps cloudflared running. It
// forwards to the server on 127.0.0.1:7710 and nowhere else.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const tunnelLabel = agentLabel + ".tunnel"

type tunnelConfig struct {
	Hostname string `json:"hostname"`
	ID       string `json:"id"`
	Name     string `json:"name"`
}

func configDir() string      { return filepath.Join(homeDir(), ".config", "herdsman") }
func tunnelJSONPath() string { return filepath.Join(configDir(), "tunnel.json") }
func tunnelYAMLPath() string { return filepath.Join(configDir(), "tunnel.yml") }
func tunnelLogPath() string {
	return filepath.Join(homeDir(), "Library", "Logs", "herdsman-tunnel.log")
}
func tunnelPlistPath() string {
	return filepath.Join(homeDir(), "Library", "LaunchAgents", tunnelLabel+".plist")
}
func cloudflaredHome() string          { return filepath.Join(homeDir(), ".cloudflared") }
func tunnelCredsPath(id string) string { return filepath.Join(cloudflaredHome(), id+".json") }

// readTunnel returns the tunnel set up, if any.
func readTunnel() (tunnelConfig, bool) {
	var c tunnelConfig
	b, err := os.ReadFile(tunnelJSONPath())
	if err != nil || json.Unmarshal(b, &c) != nil || c.Hostname == "" {
		return tunnelConfig{}, false
	}
	return c, true
}

// tunnelHostname is the tunnel's hostname, "" without one.
func tunnelHostname() string {
	c, _ := readTunnel()
	return c.Hostname
}

var hostnameRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$`)

func findCloudflared() string {
	for _, name := range []string{"cloudflared", "/opt/homebrew/bin/cloudflared", "/usr/local/bin/cloudflared"} {
		if bin, err := exec.LookPath(name); err == nil {
			return bin
		}
	}
	return ""
}

// tunnelName is the Cloudflare tunnel's name for a hostname, so each Mac
// (hostname) has its own on one account.
func tunnelName(hostname string) string {
	return "herdsman-" + strings.ReplaceAll(hostname, ".", "-")
}

// tunnelYAML is cloudflared's config: the hostname to the server on
// loopback, anything else refused.
func tunnelYAML(c tunnelConfig) string {
	return fmt.Sprintf(`# Made by herdsman tunnel; it rewrites this file.
tunnel: %s
credentials-file: %s
ingress:
  - hostname: %s
    service: http://127.0.0.1:%s
  - service: http_status:404
`, c.ID, tunnelCredsPath(c.ID), c.Hostname, port)
}

// tunnelPlist is the LaunchAgent that keeps cloudflared running.
func tunnelPlist(bin string) string {
	var argv strings.Builder
	for _, a := range []string{bin, "tunnel", "--no-autoupdate", "--config", tunnelYAMLPath(), "run"} {
		fmt.Fprintf(&argv, "\n\t\t<string>%s</string>", xmlText(a))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>%s
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, tunnelLabel, argv.String(), xmlText(tunnelLogPath()), xmlText(tunnelLogPath()))
}

// cloudflared runs the CLI, its output (stdout and stderr) as one.
func cloudflared(bin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// jsonIn is the JSON value in a command's output (cloudflared logs around it).
func jsonIn(out string, v any) error {
	i := strings.IndexAny(out, "[{")
	if i < 0 {
		return errors.New("no JSON in: " + out)
	}
	return json.NewDecoder(strings.NewReader(out[i:])).Decode(v)
}

func tunnelSetup(hostname, token string) error {
	hostname = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(hostname)), ".")
	hostname = strings.TrimPrefix(strings.TrimPrefix(hostname, "https://"), "http://")
	if !hostnameRe.MatchString(hostname) {
		return fmt.Errorf("%q isn't a hostname: give one on a domain of your Cloudflare account, like herdsman.example.com", hostname)
	}
	bin := findCloudflared()
	if bin == "" {
		return errors.New("cloudflared isn't installed: brew install cloudflared, then run this again")
	}
	if _, err := os.Stat(filepath.Join(cloudflaredHome(), "cert.pem")); err != nil {
		fmt.Printf("Log in to Cloudflare in the browser that opens, and pick the domain of %s.\n", hostname)
		cmd := exec.Command(bin, "tunnel", "login")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("cloudflared tunnel login: %w", err)
		}
	}

	c := tunnelConfig{Hostname: hostname, Name: tunnelName(hostname)}
	out, err := cloudflared(bin, "tunnel", "list", "--output", "json", "--name", c.Name)
	if err != nil {
		return fmt.Errorf("cloudflared tunnel list: %s", out)
	}
	var found []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	jsonIn(out, &found) // "[]" or nothing when there is none
	for _, t := range found {
		if t.Name == c.Name {
			c.ID = t.ID
		}
	}
	if c.ID == "" {
		out, err := cloudflared(bin, "tunnel", "create", "--output", "json", c.Name)
		if err != nil {
			return fmt.Errorf("cloudflared tunnel create: %s", out)
		}
		var made struct {
			ID string `json:"id"`
		}
		if err := jsonIn(out, &made); err != nil || made.ID == "" {
			return fmt.Errorf("cloudflared tunnel create: no tunnel id in %q", out)
		}
		c.ID = made.ID
		fmt.Printf("Made the tunnel %s.\n", c.Name)
	} else {
		fmt.Printf("Using the tunnel %s.\n", c.Name)
	}
	if _, err := os.Stat(tunnelCredsPath(c.ID)); err != nil {
		// Made elsewhere (or the file was lost): fetch its credentials.
		if out, err := cloudflared(bin, "tunnel", "token", "--cred-file", tunnelCredsPath(c.ID), c.ID); err != nil {
			return fmt.Errorf("cloudflared tunnel token: %s", out)
		}
	}
	if out, err := cloudflared(bin, "tunnel", "route", "dns", c.ID, hostname); err != nil {
		return fmt.Errorf("point %s at the tunnel: %s\n(a DNS record for it exists already? Remove it in Cloudflare's dashboard, or pick another hostname)", hostname, out)
	}

	if err := os.MkdirAll(configDir(), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(tunnelYAMLPath(), []byte(tunnelYAML(c)), 0o600); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(tunnelJSONPath(), append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := loadTunnelAgent(bin); err != nil {
		return err
	}
	fmt.Printf("Running: cloudflared starts at login and restarts if it stops.\n  agent   %s\n  log     %s\n", tunnelPlistPath(), tunnelLogPath())

	fmt.Printf("Waiting for https://%s to answer", hostname)
	for i := 0; i < 30; i++ {
		if err := pingTunnel(hostname, token); err == nil {
			fmt.Printf("\nhttps://%s reaches this Mac.\n", hostname)
			fmt.Println("A phone paired before learns it the next time it reaches this Mac (over Tailscale); else pair it again with herdsman pair.")
			return nil
		}
		fmt.Print(".")
		time.Sleep(2 * time.Second)
	}
	fmt.Printf("\nNot answering yet. A new DNS record can take a few minutes; check with herdsman status. Log: %s\n", tunnelLogPath())
	return nil
}

func loadTunnelAgent(bin string) error {
	if err := os.MkdirAll(filepath.Dir(tunnelPlistPath()), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tunnelPlistPath(), []byte(tunnelPlist(bin)), 0o644); err != nil {
		return err
	}
	launchctl("bootout", launchdTarget()+"/"+tunnelLabel)
	if out, err := launchctl("bootstrap", launchdTarget(), tunnelPlistPath()); err != nil {
		return fmt.Errorf("launchctl bootstrap: %s", out)
	}
	return nil
}

func unloadTunnelAgent() error {
	launchctl("bootout", launchdTarget()+"/"+tunnelLabel)
	if err := os.Remove(tunnelPlistPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func tunnelOff() error {
	c, ok := readTunnel()
	if err := unloadTunnelAgent(); err != nil {
		return err
	}
	os.Remove(tunnelJSONPath())
	os.Remove(tunnelYAMLPath())
	if !ok {
		fmt.Println("There was no tunnel.")
		return nil
	}
	fmt.Printf("Stopped: https://%s no longer reaches this Mac.\n", c.Hostname)
	fmt.Printf("Kept on Cloudflare: the tunnel %s and the DNS record of %s. To remove them:\n  cloudflared tunnel delete %s\n  and the record in Cloudflare's dashboard.\n", c.Name, c.Hostname, c.Name)
	return nil
}

// tunnelStatus prints whether the tunnel runs and answers.
func tunnelStatus(token string) {
	c, ok := readTunnel()
	if !ok {
		fmt.Println("Tunnel: none. Reach this Mac without Tailscale: herdsman tunnel <hostname on your Cloudflare domain>")
		return
	}
	if out, err := launchctl("print", launchdTarget()+"/"+tunnelLabel); err != nil {
		fmt.Printf("Tunnel https://%s: cloudflared isn't loaded: run herdsman tunnel %s again.\n", c.Hostname, c.Hostname)
		return
	} else if pidRe.FindStringSubmatch(out) == nil {
		fmt.Printf("Tunnel https://%s: cloudflared isn't running. Log: %s\n", c.Hostname, tunnelLogPath())
		return
	}
	if err := pingTunnel(c.Hostname, token); err != nil {
		fmt.Printf("Tunnel https://%s: cloudflared runs, but it doesn't answer: %v. Log: %s\n", c.Hostname, err, tunnelLogPath())
		return
	}
	fmt.Printf("Tunnel https://%s: answering.\n", c.Hostname)
}

// pingTunnel asks https://hostname/api/ping, resolving the name through
// 1.1.1.1: right after the record is made, this Mac's resolver may still
// remember that the name didn't exist.
func pingTunnel(hostname, token string) error {
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, "1.1.1.1:53")
	}}
	tr := &http.Transport{DialContext: (&net.Dialer{Timeout: 5 * time.Second, Resolver: resolver}).DialContext, ForceAttemptHTTP2: true}
	defer tr.CloseIdleConnections()
	req, _ := http.NewRequest("GET", "https://"+hostname+"/api/ping", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Transport: tr, Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
