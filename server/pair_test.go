package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tempToken(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "token")
}

func TestCodeWorksOnce(t *testing.T) {
	tf := tempToken(t)
	code, err := newCode(tf)
	if err != nil || len(code) != 6 {
		t.Fatalf("newCode = %q, %v", code, err)
	}
	if err := redeem(tf, code[:3]+" "+code[3:]); err != nil { // spaces as typed
		t.Fatalf("right code refused: %v", err)
	}
	if err := redeem(tf, code); err == nil {
		t.Fatal("code worked twice")
	}
}

func TestWrongTriesKillTheCode(t *testing.T) {
	tf := tempToken(t)
	code, _ := newCode(tf)
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	for i := 0; i < codeTries; i++ {
		if err := redeem(tf, wrong); err == nil {
			t.Fatal("wrong code accepted")
		}
	}
	if err := redeem(tf, code); err == nil {
		t.Fatalf("code still works after %d wrong tries", codeTries)
	}
}

func TestExpiredCode(t *testing.T) {
	tf := tempToken(t)
	b, _ := json.Marshal(pairing{Code: "123456", Expires: time.Now().Add(-time.Second).Unix()})
	os.WriteFile(pairingFile(tf), b, 0o600)
	if err := redeem(tf, "123456"); err == nil {
		t.Fatal("expired code accepted")
	}
}

func TestPairEndpoint(t *testing.T) {
	tf := tempToken(t)
	s := &server{token: []byte(strings.Repeat("a", 48)), tokenFile: tf}
	code, _ := newCode(tf)
	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.pair(rec, httptest.NewRequest(http.MethodPost, "/api/pair", bytes.NewBufferString(body)))
		return rec
	}
	if rec := post(`{"code":"` + code + `"}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), string(s.token)) {
		t.Fatalf("pair = %d %s", rec.Code, rec.Body)
	}
	if rec := post(`{"code":"` + code + `"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("reused code = %d, want 403", rec.Code)
	}
}

func TestPairLinkRoundTrip(t *testing.T) {
	link := pairLink([]string{"http://100.64.1.2:7710"}, "tok&en", "Mac")
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "herdsman" || u.Host != "pair" {
		t.Fatalf("link %q: %v", link, err)
	}
	q := u.Query()
	if q.Get("url") != "http://100.64.1.2:7710" || q.Get("token") != "tok&en" || q.Get("host") != "Mac" {
		t.Fatalf("query = %v", q)
	}
	if q.Has("routes") {
		t.Fatalf("one route: no routes= (%v)", q)
	}
	art, err := qrText(link)
	if err != nil || !strings.Contains(art, "▀") {
		t.Fatalf("qrText: %v", err)
	}
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{
		"IMG 0042.JPG":       "IMG_0042.JPG",
		"../../etc/passwd":   "passwd",
		"a/b\\c;rm -rf.png":  "crm_-rf.png",
		"...":                "",
		"foto liburan ✨.mp4": "foto_liburan_.mp4",
	} {
		got := safeName(in)
		if want == "" {
			if !strings.HasPrefix(got, "file-") {
				t.Errorf("safeName(%q) = %q, want a file-<time> name", in, got)
			}
			continue
		}
		if got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}
