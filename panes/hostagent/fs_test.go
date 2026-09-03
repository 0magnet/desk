//go:build !js

package hostagent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0magnet/desk/panes/hostproto"
)

func fsAgentServer(t *testing.T, root string) (*httptest.Server, Config) {
	t.Helper()
	tok, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	cfg := Config{Token: tok}
	srv := httptest.NewServer(nil)
	cfg.Origins = []string{srv.URL}
	srv.Config.Handler = cfg.FSHandler(FSConfig{Root: root})
	t.Cleanup(srv.Close)
	return srv, cfg
}

// do makes one request the way the browser client does.
func do(t *testing.T, srv *httptest.Server, cfg Config, method, endpoint string, q url.Values, body io.Reader) (int, []byte) {
	t.Helper()
	if q == nil {
		q = url.Values{}
	}
	q.Set(hostproto.TokenParam, cfg.Token)
	req, err := http.NewRequest(method, srv.URL+endpoint+"?"+q.Encode(), body)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Origin", srv.URL)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()         //nolint:errcheck
	out, _ := io.ReadAll(resp.Body) //nolint:errcheck // a short read shows up as a failed assertion
	return resp.StatusCode, out
}

func pv(p string) url.Values { return url.Values{hostproto.PathParam: []string{p}} }

func TestFilesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	srv, cfg := fsAgentServer(t, dir)

	target := "/note.txt"
	if code, body := do(t, srv, cfg, "POST", hostproto.FSWrite, pv(target), strings.NewReader("hello")); code != 200 {
		t.Fatalf("write: %d %s", code, body)
	}
	// The file is on the real disk, which is the only claim worth making.
	got, err := os.ReadFile(filepath.Join(dir, "note.txt")) //nolint:gosec // a path this test just made
	if err != nil || string(got) != "hello" {
		t.Fatalf("on disk: %q %v", got, err)
	}

	code, body := do(t, srv, cfg, "GET", hostproto.FSRead, pv(target), nil)
	if code != 200 || string(body) != "hello" {
		t.Fatalf("read: %d %q", code, body)
	}

	code, body = do(t, srv, cfg, "GET", hostproto.FSList, pv("/"), nil)
	if code != 200 {
		t.Fatalf("list: %d %s", code, body)
	}
	var ents []hostproto.FileInfo
	if err := json.Unmarshal(body, &ents); err != nil {
		t.Fatalf("list json: %v", err)
	}
	if len(ents) != 1 || ents[0].Name != "note.txt" || ents[0].Size != 5 {
		t.Fatalf("list gave %+v", ents)
	}

	moved := "/moved.txt"
	q := pv(target)
	q.Set(hostproto.ToParam, moved)
	if code, body := do(t, srv, cfg, "POST", hostproto.FSRename, q, nil); code != 200 {
		t.Fatalf("rename: %d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(dir, "moved.txt")); err != nil {
		t.Fatalf("after rename: %v", err)
	}

	if code, body := do(t, srv, cfg, "POST", hostproto.FSRemove, pv(moved), nil); code != 200 {
		t.Fatalf("remove: %d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(dir, "moved.txt")); !os.IsNotExist(err) {
		t.Fatalf("still there after remove: %v", err)
	}
}

func TestMissingFileReportsAKindTheClientCanTest(t *testing.T) {
	// The whole reason the reply carries a kind: afero's callers do not read
	// error strings, they call os.IsNotExist.
	dir := t.TempDir()
	srv, cfg := fsAgentServer(t, dir)

	code, body := do(t, srv, cfg, "GET", hostproto.FSStat, pv("/nope"), nil)
	if code != http.StatusNotFound {
		t.Fatalf("status %d, want 404: %s", code, body)
	}
	var rep hostproto.ErrorReply
	if err := json.Unmarshal(body, &rep); err != nil {
		t.Fatalf("error json: %v", err)
	}
	if rep.Kind != hostproto.ErrNotExist {
		t.Errorf("kind %q, want %q", rep.Kind, hostproto.ErrNotExist)
	}
}

// --- confinement, which is the part worth being sure about ---

func TestRootConfinesTraversal(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("classified"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, cfg := fsAgentServer(t, dir)

	for _, p := range []string{
		outside,
		filepath.Join(dir, "..", filepath.Base(filepath.Dir(outside)), "secret.txt"),
		dir + "/../../etc/passwd",
		"/etc/passwd",
	} {
		if code, body := do(t, srv, cfg, "GET", hostproto.FSRead, pv(p), nil); code == 200 {
			t.Errorf("read %q was allowed: %s", p, body)
		}
	}
}

func TestRootConfinesSymlinksOutOfIt(t *testing.T) {
	// The case a string prefix check would miss: the path stays textually
	// inside the root and the kernel would still follow it out.
	dir := t.TempDir()
	other := t.TempDir()
	secret := filepath.Join(other, "secret.txt")
	if err := os.WriteFile(secret, []byte("classified"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "escape")
	if err := os.Symlink(other, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	srv, cfg := fsAgentServer(t, dir)

	code, body := do(t, srv, cfg, "GET", hostproto.FSRead, pv("/escape/secret.txt"), nil)
	if code == 200 {
		t.Fatalf("a symlink led out of the root: %s", body)
	}
	if strings.Contains(string(body), "classified") {
		t.Fatal("the contents leaked in the error")
	}
}

func TestWritingThroughASymlinkOutOfTheRootIsRefused(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()
	link := filepath.Join(dir, "escape")
	if err := os.Symlink(other, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	srv, cfg := fsAgentServer(t, dir)

	victim := "/escape/planted.txt"
	if code, _ := do(t, srv, cfg, "POST", hostproto.FSWrite, pv(victim), strings.NewReader("x")); code == 200 {
		t.Fatal("a write escaped the root through a symlink")
	}
	if _, err := os.Stat(filepath.Join(other, "planted.txt")); err == nil {
		t.Fatal("the file was created outside the root")
	}
}

func TestSameOriginFetchWithNoOriginHeaderIsAllowed(t *testing.T) {
	// The case that broke this in a browser and cannot be reproduced with an
	// Origin header, because the whole point is that there is not one: a
	// browser omits Origin on a same-origin GET, so the page fetching from the
	// server that served it arrives bare. Sec-Fetch-Site is what identifies it.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, cfg := fsAgentServer(t, dir)

	get := func(site string) int {
		q := pv("/note.txt")
		q.Set(hostproto.TokenParam, cfg.Token)
		req, err := http.NewRequest("GET", srv.URL+hostproto.FSRead+"?"+q.Encode(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if site != "" {
			req.Header.Set("Sec-Fetch-Site", site)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close() //nolint:errcheck
		return resp.StatusCode
	}

	if code := get("same-origin"); code != 200 {
		t.Errorf("same-origin fetch with no Origin: %d, want 200", code)
	}
	// And the ones that must still be refused, token or no token.
	for _, site := range []string{"cross-site", "same-site", "none"} {
		if code := get(site); code != http.StatusForbidden {
			t.Errorf("Sec-Fetch-Site %q: %d, want 403", site, code)
		}
	}
}

func TestRootedClientSeesTheRootAsSlash(t *testing.T) {
	// Not a cosmetic choice. The shell in the tab has a working directory and
	// it starts at "/". If that meant the machine's "/" while the root was
	// elsewhere, every path would be refused and a confined filesystem would
	// be indistinguishable from a broken one.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "inside.txt"), []byte("in"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, cfg := fsAgentServer(t, dir)

	for _, p := range []string{"/", "/..", "/../../..", "."} {
		code, body := do(t, srv, cfg, "GET", hostproto.FSList, pv(p), nil)
		if code != 200 {
			t.Fatalf("list %q: %d %s", p, code, body)
		}
		var ents []hostproto.FileInfo
		if err := json.Unmarshal(body, &ents); err != nil {
			t.Fatalf("list %q json: %v", p, err)
		}
		if len(ents) != 1 || ents[0].Name != "inside.txt" {
			t.Errorf("list %q gave %+v; every one of these is the root", p, ents)
		}
	}
}

func TestNoRootMeansTheWholeFilesystem(t *testing.T) {
	// Documented behavior rather than an oversight: with --shell already on,
	// confining the file endpoints would be a fence beside an open gate. The
	// test exists so that changing it is a deliberate act.
	dir := t.TempDir()
	f := filepath.Join(dir, "anywhere.txt")
	if err := os.WriteFile(f, []byte("reachable"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, cfg := fsAgentServer(t, "") // no root

	code, body := do(t, srv, cfg, "GET", hostproto.FSRead, pv(f), nil)
	if code != 200 || string(body) != "reachable" {
		t.Fatalf("unrooted read: %d %q", code, body)
	}
}

func TestFilesystemNeedsTheTokenAndTheOrigin(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := "/note.txt"
	srv, cfg := fsAgentServer(t, dir)

	// No token.
	resp, err := srv.Client().Get(srv.URL + hostproto.FSRead + "?" + pv(f).Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close() //nolint:errcheck,gosec
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("without a token: %d, want 403", resp.StatusCode)
	}

	// Right token, foreign Origin — the case that matters, because a page you
	// visited cannot change its own Origin but might learn the token.
	q := pv(f)
	q.Set(hostproto.TokenParam, cfg.Token)
	req, _ := http.NewRequest("GET", srv.URL+hostproto.FSRead+"?"+q.Encode(), nil) //nolint:errcheck
	req.Header.Set("Origin", "http://evil.example")
	resp2, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close() //nolint:errcheck
	if resp2.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp2.Body) //nolint:errcheck // only used in the failure message
		t.Errorf("from a foreign origin: %d, want 403 (%s)", resp2.StatusCode, body)
	}
}

func TestMkdirAndTruncate(t *testing.T) {
	dir := t.TempDir()
	srv, cfg := fsAgentServer(t, dir)

	nested := "/a/b/c"
	q := pv(nested)
	q.Set(hostproto.AllParam, "1")
	if code, body := do(t, srv, cfg, "POST", hostproto.FSMkdir, q, nil); code != 200 {
		t.Fatalf("mkdirall: %d %s", code, body)
	}
	if fi, err := os.Stat(filepath.Join(dir, "a", "b", "c")); err != nil || !fi.IsDir() {
		t.Fatalf("nested dir: %v", err)
	}

	f := "/big.txt"
	if code, _ := do(t, srv, cfg, "POST", hostproto.FSWrite, pv(f), strings.NewReader("0123456789")); code != 200 {
		t.Fatal("write failed")
	}
	tq := pv(f)
	tq.Set(hostproto.SizeParam, "4")
	if code, body := do(t, srv, cfg, "POST", hostproto.FSTruncate, tq, nil); code != 200 {
		t.Fatalf("truncate: %d %s", code, body)
	}
	got, err := os.ReadFile(filepath.Join(dir, "big.txt")) //nolint:gosec // a path this test just made
	if err != nil || string(got) != "0123" {
		t.Fatalf("after truncate: %q %v", got, err)
	}
}
