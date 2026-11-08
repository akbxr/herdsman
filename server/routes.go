package main

// The ways a phone reaches this Mac, fastest first: the local network (with
// -lan), Tailscale, and a Cloudflare tunnel (tunnel.go). The phone gets them
// all in the QR code and from /api/ping, and uses the first that answers.
//
// Without -addr the server listens on 127.0.0.1 (where the tunnel's
// cloudflared reaches it) and on the Tailscale address while Tailscale is up;
// with -lan on every interface instead.

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const port = "7710"

// listenPlan says where to listen: fixed (-addr), every interface (-lan), or
// loopback plus Tailscale.
type listenPlan struct {
	fixed string
	lan   bool
}

// serve listens as planned and serves until a listener it can't do without
// fails. Tailscale's address is followed as it comes, goes and changes.
func (p listenPlan) serve(srv *http.Server) error {
	if p.fixed != "" || p.lan {
		addr := p.fixed
		if addr == "" {
			addr = ":" + port
		}
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		log.Printf("herdsman %s listening on %s", version, addr)
		return srv.Serve(ln)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		return err
	}
	log.Printf("herdsman %s listening on 127.0.0.1:%s", version, port)
	go followTailscale(srv)
	return srv.Serve(ln)
}

// followTailscale keeps a listener on the Tailscale address while there is one.
func followTailscale(srv *http.Server) {
	var (
		ip   string
		ln   net.Listener
		seen string // the CGNAT address last seen: the CLI is asked only when it changes
	)
	for {
		now := cgnatIP()
		if now != seen || (ln == nil && now != "") {
			seen = now
			next := ""
			if now != "" {
				next = tailscaleIP()
			}
			if next != ip {
				if ln != nil {
					ln.Close()
					ln = nil
					log.Printf("stopped listening on %s (Tailscale went away)", ip)
				}
				ip = next
				if ip != "" {
					l, err := net.Listen("tcp", net.JoinHostPort(ip, port))
					if err != nil {
						log.Printf("listen on Tailscale %s: %v", ip, err)
						ip = "" // try again next round
					} else {
						ln = l
						log.Printf("listening on %s:%s (Tailscale)", ip, port)
						go func() {
							if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
								log.Printf("serve on Tailscale: %v", err)
							}
						}()
					}
				}
			}
		}
		time.Sleep(5 * time.Second)
	}
}

// lanIPs returns this Mac's private IPv4 addresses on the local network
// (not loopback, Tailscale or other VPNs in 100.64.0.0/10).
func lanIPs() []string {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 || strings.HasPrefix(i.Name, "utun") {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if ok && n.IP.To4() != nil && n.IP.IsPrivate() {
				out = append(out, n.IP.String())
			}
		}
	}
	return out
}

// routes lists the addresses a phone can reach this server at, fastest first.
type routeSource struct {
	fixed     string
	lan       bool
	tailscale func() string
	lanIPs    func() []string
	tunnel    func() string // the tunnel's hostname, "" without one
}

func (rs routeSource) routes() []string {
	if rs.fixed != "" {
		return []string{"http://" + hostPort(rs.fixed)}
	}
	out := []string{} // JSON [] when there is none
	if rs.lan && rs.lanIPs != nil {
		for _, ip := range rs.lanIPs() {
			out = append(out, "http://"+net.JoinHostPort(ip, port))
		}
	}
	if rs.tailscale != nil {
		if ip := rs.tailscale(); ip != "" {
			out = append(out, "http://"+net.JoinHostPort(ip, port))
		}
	}
	if rs.tunnel != nil {
		if h := rs.tunnel(); h != "" {
			out = append(out, "https://"+h)
		}
	}
	return out
}

// hostPort fills in an -addr's missing host (":7710") with loopback.
func hostPort(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1" + addr
	}
	return addr
}

// localRoutes is where this process's server is reachable, as configured.
func localRoutes(fixed string, lan bool) routeSource {
	return routeSource{fixed: fixed, lan: lan, tailscale: cachedTailscaleIP, lanIPs: lanIPs, tunnel: tunnelHostname}
}

// cachedTailscaleIP asks the tailscale CLI at most every 10 s (/api/ping
// reports routes, and phones ping often).
var (
	tsMu   sync.Mutex
	tsIP   string
	tsWhen time.Time
)

func cachedTailscaleIP() string {
	tsMu.Lock()
	defer tsMu.Unlock()
	if time.Since(tsWhen) > 10*time.Second {
		tsIP, tsWhen = "", time.Now()
		if cgnatIP() != "" {
			tsIP = tailscaleIP()
		}
	}
	return tsIP
}

// preferredForOldApps is the one address an app from before routes reads
// (the QR code's url=): Tailscale's, else the first.
func preferredForOldApps(routes []string) string {
	for _, r := range routes {
		if h := strings.TrimPrefix(r, "http://"); h != r {
			if ip := net.ParseIP(strings.Split(h, ":")[0]); ip != nil && cgnat.Contains(ip) {
				return r
			}
		}
	}
	if len(routes) > 0 {
		return routes[0]
	}
	return ""
}

var cgnat = func() *net.IPNet { _, n, _ := net.ParseCIDR("100.64.0.0/10"); return n }()

// clientIP is who sent a request: behind the tunnel (from loopback), the
// address Cloudflare saw.
func clientIP(r *http.Request) string {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if cf := r.Header.Get("Cf-Connecting-Ip"); cf != "" {
			return cf + " (tunnel)"
		}
	}
	return r.RemoteAddr
}

// wsKeepAlive pings a WebSocket every 30 s until ctx ends: Cloudflare closes
// one that stays quiet for 100 s (a terminal nobody types in, showing nothing new).
var wsPingEvery = 30 * time.Second

func wsKeepAlive(ctx context.Context, conn *websocket.Conn) {
	t := time.NewTicker(wsPingEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			conn.Ping(pctx)
			cancel()
		}
	}
}
