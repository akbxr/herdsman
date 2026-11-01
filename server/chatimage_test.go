package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// testPNG: a w×h picture, opaque.
func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 200, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func getImage(t *testing.T, query string) *httptest.ResponseRecorder {
	t.Helper()
	s := &server{}
	rec := httptest.NewRecorder()
	s.image(rec, httptest.NewRequest(http.MethodGet, "/api/image?"+query, nil))
	return rec
}

// Pasted pictures are image blocks: tego's (media_type and data in the
// block) and Anthropic's (in a source). The "[Image #n…]" markers that stand
// for them leave the text; the picture is served by its id.
func TestChatImageBlocks(t *testing.T) {
	pic := testPNG(t, 40, 30)
	b64 := base64.StdEncoding.EncodeToString(pic)
	items, _ := parseTree("tego", lines(`
		{"type":"message","id":"u1","message":{"role":"user","content":[{"type":"text","text":"look at this [Image #1: /var/folders/x/T/Screenshot 1.png] please"},{"type":"image","media_type":"image/png","data":"`+b64+`"}]}}
		{"type":"message","id":"a1","parent":"u1","message":{"role":"assistant","content":[{"type":"text","text":"I see it."}]}}
	`))
	if len(items) != 2 || items[0].Text != "look at this please" || len(items[0].Images) != 1 {
		t.Fatalf("tego: %+v", items)
	}
	if items[0].ID != "u1" || items[1].ID != "a1" {
		t.Errorf("ids: %q %q", items[0].ID, items[1].ID)
	}
	id := items[0].Images[0]
	rec := getImage(t, "id="+id)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" || !bytes.Equal(rec.Body.Bytes(), pic) {
		t.Errorf("serve %s: %d %q %d bytes", id, rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
	}

	// Claude Code: the source shape, "[Image #1]" in the text; the same
	// picture has the same id.
	items, _ = parseTree("claude", lines(`
		{"type":"user","uuid":"1","parentUuid":null,"message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"`+b64+`"}},{"type":"text","text":"[Image #1] what is wrong here"}]}}
	`))
	want := []ChatItem{{ID: "1", Kind: "user", Text: "what is wrong here", Images: []string{id}}}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("claude: got %+v\nwant %+v", items, want)
	}

	// Only a picture: an item with no text.
	items, _ = parseTree("claude", lines(`
		{"type":"user","uuid":"1","parentUuid":null,"message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"`+b64+`"}}]}}
	`))
	if len(items) != 1 || items[0].Text != "" || len(items[0].Images) != 1 {
		t.Errorf("picture only: %+v", items)
	}

	// Not a picture, or not base64: the old "[image]".
	notPic := base64.StdEncoding.EncodeToString([]byte("#!/bin/sh\necho hi\n"))
	items, _ = parseTree("tego", lines(`
		{"type":"message","id":"u1","message":{"role":"user","content":[{"type":"text","text":"x"},{"type":"image","media_type":"image/png","data":"`+notPic+`"},{"type":"image","media_type":"image/png","data":"%%%"}]}}
	`))
	if len(items) != 1 || items[0].Text != "x\n\n[image]\n\n[image]" || len(items[0].Images) != 0 {
		t.Errorf("not pictures: %+v", items)
	}

	// Thumbnails: scaled to fit w on the longer side.
	big := testPNG(t, 800, 400)
	bigID := images.addBlock("image/png", base64.StdEncoding.EncodeToString(big))
	rec = getImage(t, "id="+bigID+"&w=200")
	cfg, format, err := image.DecodeConfig(rec.Body)
	if rec.Code != 200 || err != nil || cfg.Width != 200 || cfg.Height != 100 || format != "jpeg" || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("thumbnail: %d %v %dx%d %s %q", rec.Code, err, cfg.Width, cfg.Height, format, rec.Header().Get("Content-Type"))
	}
	// Smaller than w already: as it is.
	if rec := getImage(t, "id="+bigID+"&w=2000"); !bytes.Equal(rec.Body.Bytes(), big) {
		t.Errorf("w larger than the picture: %d bytes", rec.Body.Len())
	}
}

// Paths in your text: the phone's attachments (hc put) and tego's
// "[Image #n: path]". Only regular picture files are served.
func TestChatImagePaths(t *testing.T) {
	dir := t.TempDir()
	pic := testPNG(t, 20, 20)
	good := filepath.Join(dir, "1000061135.png")
	os.WriteFile(good, pic, 0o644)
	spaced := filepath.Join(dir, "Screenshot 2026-10-26 at 11.57.48.png")
	os.WriteFile(spaced, pic, 0o644)

	text, ids := userImagesIn("why is the chat empty "+good+" and [Image #2: "+spaced+"]", nil)
	if text != "why is the chat empty and" || len(ids) != 2 {
		t.Fatalf("got %q %v", text, ids)
	}
	if rec := getImage(t, "id="+ids[0]); rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), pic) || rec.Header().Get("Content-Type") != "image/png" {
		t.Errorf("serve a file: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}

	// What is not a picture file stays text, and is never served.
	os.Mkdir(filepath.Join(dir, "folder.png"), 0o755)
	os.WriteFile(filepath.Join(dir, "script.png"), []byte("#!/bin/sh\nrm -rf /\n"), 0o644)
	os.Symlink(good, filepath.Join(dir, "link.png"))
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644)
	for _, p := range []string{
		filepath.Join(dir, "folder.png"),                      // a folder
		filepath.Join(dir, "script.png"),                      // not a picture inside
		filepath.Join(dir, "link.png"),                        // a link
		filepath.Join(dir, "missing.png"),                     // gone
		dir + "/../" + filepath.Base(dir) + "/1000061135.png", // ..
		"~/../../" + strings.TrimPrefix(good, "/"),            // .. from home
		"https://example.com/a.png",                           // a URL
		"x" + good,                                            // part of a word
	} {
		text, ids := userImagesIn("see "+p, nil)
		if len(ids) != 0 || text != "see "+p {
			t.Errorf("%s: got %q %v", p, text, ids)
		}
		if img, err := imageFile(p); err == nil {
			t.Errorf("%s: accepted as %+v", p, img)
		}
	}
	if _, err := imageFile(filepath.Join(dir, "notes.txt")); err == nil {
		t.Error("a .txt file accepted")
	}

	// A file swapped after it was named: not served.
	id := images.addFile(good)
	os.Remove(good)
	os.Symlink("/etc/hosts", good)
	if rec := getImage(t, "id="+id); rec.Code != http.StatusNotFound {
		t.Errorf("swapped for a link: %d", rec.Code)
	}
}

func TestImageEndpointRefusesTricks(t *testing.T) {
	for _, q := range []string{
		"id=../../etc/passwd",
		"id=" + escSlashes("/etc/hosts"),
		"id=f../../../../etc/hosts",
		"id=",
		"id=b" + strings.Repeat("0", 23) + "/",
		"id=B" + strings.Repeat("a", 24),
	} {
		if rec := getImage(t, q); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
	if rec := getImage(t, "id=f"+strings.Repeat("a", 24)); rec.Code != http.StatusNotFound {
		t.Errorf("unknown id: %d", rec.Code)
	}
}

func escSlashes(s string) string { return strings.ReplaceAll(s, "/", "%2F") }

// The phone asks for older messages with a larger limit: the file is read
// further back, the items it had keep their ids, and the start is reported
// once reached. A growing file has only its new lines read.
func TestChatHistoryPaging(t *testing.T) {
	pad := strings.Repeat("y", 16<<10) // 500 messages of 16 KB: more than the tail
	var sb strings.Builder
	sb.WriteString(`{"type":"header","id":"s"}` + "\n")
	prev := "null"
	for i := 0; i < 500; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		id := fmt.Sprintf("m%03d", i)
		fmt.Fprintf(&sb, `{"type":"message","id":"%s","parent":%s,"message":{"role":"%s","content":[{"type":"text","text":"message %d %s"}]}}`+"\n", id, prev, role, i, pad)
		prev = `"` + id + `"`
	}
	p := filepath.Join(t.TempDir(), "s.jsonl")
	os.WriteFile(p, []byte(sb.String()), 0o644)
	st, _ := os.Stat(p)

	first, start, err := readChat("tego", p, st, chatMaxItems)
	if err != nil || len(first) != chatMaxItems || start {
		t.Fatalf("limit 120: %d items, start %v, %v", len(first), start, err)
	}
	if first[len(first)-1].ID != "m499" || first[0].ID != "m380" {
		t.Errorf("limit 120: from %s to %s", first[0].ID, first[len(first)-1].ID)
	}
	more, start, _ := readChat("tego", p, st, 300)
	if len(more) != 300 || start || more[0].ID != "m200" {
		t.Errorf("limit 300: %d items from %s, start %v", len(more), more[0].ID, start)
	}
	if !reflect.DeepEqual(more[180:], first) {
		t.Error("limit 300: the last 120 differ from what limit 120 gave")
	}
	all, start, _ := readChat("tego", p, st, 800)
	if len(all) != 500 || !start || all[0].ID != "m000" || !strings.HasPrefix(all[0].Text, "message 0 ") {
		t.Errorf("limit 800: %d items from %s, start %v", len(all), all[0].ID, start)
	}

	// The file grows: a half-written line is left for later.
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"type":"message","id":"m500","parent":"m499","message":{"role":"user","content":[{"type":"text","text":"new one"}]}}` + "\n")
	f.WriteString(`{"type":"message","id":"m501","parent":"m500","message":{"role":"assis`)
	f.Close()
	future := time.Now().Add(time.Second)
	os.Chtimes(p, future, future)
	st, _ = os.Stat(p)
	grown, start, _ := readChat("tego", p, st, 800)
	if len(grown) != 501 || !start || grown[500].Text != "new one" {
		t.Errorf("grown: %d items, last %+v", len(grown), grown[len(grown)-1])
	}
	f, _ = os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`tant","content":[{"type":"text","text":"done"}]}}` + "\n")
	f.Close()
	future = future.Add(time.Second)
	os.Chtimes(p, future, future)
	st, _ = os.Stat(p)
	grown, _, _ = readChat("tego", p, st, chatMaxItems)
	if len(grown) != chatMaxItems || grown[len(grown)-1].Text != "done" || grown[len(grown)-1].ID != "m501" {
		t.Errorf("finished line: last %+v", grown[len(grown)-1])
	}
	// Cached until the file changes, per limit.
	if c, ok := cachedChat(p, chatMaxItems, st); !ok || len(c.items) != chatMaxItems {
		t.Error("not cached")
	}
}

func TestReadLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	os.WriteFile(p, []byte("first\nsecond\nthird\npartial"), 0o644)
	ls, first, end, _ := readLines(p, 0)
	if len(ls) != 3 || first != 0 || end != 19 {
		t.Errorf("from 0: %q %d %d", ls, first, end)
	}
	ls, first, _, _ = readLines(p, 6) // the start of "second": kept
	if len(ls) != 2 || string(ls[0]) != "second" || first != 6 {
		t.Errorf("from a line's start: %q %d", ls, first)
	}
	ls, first, _, _ = readLines(p, 8) // inside "second": dropped
	if len(ls) != 1 || string(ls[0]) != "third" || first != 13 {
		t.Errorf("from inside a line: %q %d", ls, first)
	}
}
