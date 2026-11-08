package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestRoutesOrder(t *testing.T) {
	rs := routeSource{
		lan:       true,
		tailscale: func() string { return "100.101.1.2" },
		lanIPs:    func() []string { return []string{"192.168.1.5"} },
		tunnel:    func() string { return "herdsman.example.com" },
	}
	want := []string{"http://192.168.1.5:7710", "http://100.101.1.2:7710", "https://herdsman.example.com"}
	if got := rs.routes(); !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %v, want %v", got, want)
	}
	// Without -lan the local network isn't listened on, so not offered.
	rs.lan = false
	if got := rs.routes(); !reflect.DeepEqual(got, want[1:]) {
		t.Errorf("no lan: %v", got)
	}
	// Tailscale off, a tunnel: the tunnel alone.
	rs.tailscale = func() string { return "" }
	if got := rs.routes(); !reflect.DeepEqual(got, want[2:]) {
		t.Errorf("tunnel only: %v", got)
	}
	// -addr: only there.
	if got := (routeSource{fixed: ":9000", tunnel: rs.tunnel}).routes(); !reflect.DeepEqual(got, []string{"http://127.0.0.1:9000"}) {
		t.Errorf("fixed: %v", got)
	}
}

func TestPairLinkRoutes(t *testing.T) {
	routes := []string{"http://192.168.1.5:7710", "http://100.101.1.2:7710", "https://herdsman.example.com"}
	u, _ := url.Parse(pairLink(routes, "tok", "Mac"))
	q := u.Query()
	// An older app reads url= only: Tailscale's, which it knows how to reach.
	if q.Get("url") != "http://100.101.1.2:7710" || q.Get("routes") != strings.Join(routes, ",") {
		t.Errorf("query = %v", q)
	}
	if got := preferredForOldApps([]string{"https://herdsman.example.com"}); got != "https://herdsman.example.com" {
		t.Errorf("tunnel only: %q", got)
	}
	if routeText("https://herdsman.example.com") != "herdsman.example.com" || routeText("http://100.1.2.3:7710") != "100.1.2.3:7710" {
		t.Error("routeText")
	}
}

func TestPingRoutesAndTunnelLog(t *testing.T) {
	s := &server{token: []byte("tok"), routes: routeSource{tailscale: func() string { return "100.101.1.2" }, tunnel: func() string { return "h.example.com" }}}
	req := httptest.NewRequest("GET", "/api/ping", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	s.auth(s.ping)(rec, req)
	var out struct{ Routes []string }
	json.Unmarshal(rec.Body.Bytes(), &out)
	if !reflect.DeepEqual(out.Routes, []string{"http://100.101.1.2:7710", "https://h.example.com"}) {
		t.Errorf("ping routes = %v (%s)", out.Routes, rec.Body)
	}

	// Through the tunnel (from loopback): who Cloudflare saw. From elsewhere the header is anyone's.
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	r.Header.Set("Cf-Connecting-Ip", "203.0.113.9")
	if got := clientIP(r); got != "203.0.113.9 (tunnel)" {
		t.Errorf("clientIP = %q", got)
	}
	r.RemoteAddr = "100.89.1.1:5555"
	if got := clientIP(r); got != "100.89.1.1:5555" {
		t.Errorf("clientIP not loopback = %q", got)
	}
}

func TestTunnelFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if tunnelHostname() != "" {
		t.Fatal("no tunnel yet")
	}
	c := tunnelConfig{Hostname: "herdsman.example.com", ID: "6ff42ae2-765d-4adf-8112-31c55c1551ef", Name: tunnelName("herdsman.example.com")}
	if c.Name != "herdsman-herdsman-example-com" {
		t.Errorf("name = %q", c.Name)
	}
	os.MkdirAll(configDir(), 0o700)
	b, _ := json.Marshal(c)
	os.WriteFile(tunnelJSONPath(), b, 0o600)
	if tunnelHostname() != "herdsman.example.com" {
		t.Errorf("hostname = %q", tunnelHostname())
	}
	y := tunnelYAML(c)
	for _, want := range []string{"tunnel: " + c.ID, filepath.Join(home, ".cloudflared", c.ID+".json"), "hostname: herdsman.example.com", "service: http://127.0.0.1:7710", "service: http_status:404"} {
		if !strings.Contains(y, want) {
			t.Errorf("yaml lacks %q:\n%s", want, y)
		}
	}
	p := tunnelPlist("/opt/homebrew/bin/cloudflared")
	for _, want := range []string{"<string>" + tunnelLabel + "</string>", "<string>--no-autoupdate</string>", "<string>" + tunnelYAMLPath() + "</string>", "<string>run</string>"} {
		if !strings.Contains(p, want) {
			t.Errorf("plist lacks %q", want)
		}
	}
	for _, bad := range []string{"localhost", "herdsman", "a..b.com", "-a.example.com", "a.example.com/x"} {
		if hostnameRe.MatchString(bad) {
			t.Errorf("%q taken for a hostname", bad)
		}
	}
	var made struct{ ID string }
	if err := jsonIn("2026-11-08T10:00:00Z INF Created tunnel\n{\"id\":\"abc\",\"name\":\"n\"}", &made); err != nil || made.ID != "abc" {
		t.Errorf("jsonIn = %v %v", made, err)
	}
}

// wsKeepAlive pings the phone's socket, so it doesn't sit quiet.
func TestWSKeepAlive(t *testing.T) {
	old := wsPingEvery
	wsPingEvery = 20 * time.Millisecond
	defer func() { wsPingEvery = old }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go wsKeepAlive(ctx, c)
		c.Read(ctx) // until the client goes; reads the pongs meanwhile
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pings := make(chan struct{}, 10)
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), &websocket.DialOptions{
		OnPingReceived: func(context.Context, []byte) bool { pings <- struct{}{}; return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	go c.Read(ctx)
	for i := 0; i < 2; i++ {
		select {
		case <-pings:
		case <-ctx.Done():
			t.Fatalf("%d pings, want 2", i)
		}
	}
	c.Close(websocket.StatusNormalClosure, "")
}
