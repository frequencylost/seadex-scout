package webui

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeQbit is a qBittorrent Web API that wants a sign-in.
type fakeQbit struct {
	mu         sync.Mutex
	added      []string // "url|category"
	categories map[string]bool
	logins     int
	fail       bool
	v5         bool // answer like qBittorrent 5: 204 to a sign-in
	csrf401    bool // answer 401 to everything, as a failed Host or CSRF check does
	srv        *httptest.Server
}

func newFakeQbit(t *testing.T) *fakeQbit {
	t.Helper()
	f := &fakeQbit{categories: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.csrf401 || r.Header.Get("Referer") == "" || r.Header.Get("Origin") != f.srv.URL {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/api/v2/app/version" {
			if c, err := r.Cookie("SID"); err != nil || c.Value != "s" {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			_, _ = w.Write([]byte("v5.0.2"))
			return
		}
		if r.Method != http.MethodPost || r.ParseForm() != nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/api/v2/auth/login" {
			if r.PostForm.Get("username") != "admin" || r.PostForm.Get("password") != "pw" {
				_, _ = w.Write([]byte("Fails."))
				return
			}
			f.logins++
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "s", Path: "/"})
			if f.v5 {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			_, _ = w.Write([]byte("Ok."))
			return
		}
		if c, err := r.Cookie("SID"); err != nil || c.Value != "s" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/api/v2/torrents/createCategory":
			name := r.PostForm.Get("category")
			if f.categories[name] {
				http.Error(w, "exists", http.StatusConflict)
				return
			}
			f.categories[name] = true
		case "/api/v2/torrents/add":
			if f.fail || (r.PostForm.Get("category") != "" && !f.categories[r.PostForm.Get("category")]) {
				_, _ = w.Write([]byte("Fails."))
				return
			}
			f.added = append(f.added, r.PostForm.Get("urls")+"|"+r.PostForm.Get("category"))
			_, _ = w.Write([]byte("Ok."))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func testQbit(t *testing.T, f *fakeQbit, password string) *Qbit {
	t.Helper()
	q, err := NewQbit(QbitConfig{URL: f.srv.URL, Username: "admin", Password: password,
		Categories: map[string]string{"sonarr": "tv-sonarr", "radarr": "radarr"}})
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestQbitAddsUnderEachAppsCategory(t *testing.T) {
	f := newFakeQbit(t)
	q := testQbit(t, f, "pw")
	ctx := context.Background()

	if _, err := q.AddNyaa(ctx, "https://nyaa.si/view/1700000", "sonarr"); err != nil {
		t.Fatal(err)
	}
	if cat, err := q.AddNyaa(ctx, "https://nyaa.si/view/42", "radarr"); err != nil || cat != "radarr" {
		t.Fatalf("radarr add: %q %v", cat, err)
	}
	want := []string{"https://nyaa.si/download/1700000.torrent|tv-sonarr", "https://nyaa.si/download/42.torrent|radarr"}
	if strings.Join(f.added, " ") != strings.Join(want, " ") {
		t.Errorf("added %v, want %v", f.added, want)
	}
	if f.logins != 1 {
		t.Errorf("signed in %d times, want once for the session", f.logins)
	}
}

func TestQbitRefusesOtherAddresses(t *testing.T) {
	f := newFakeQbit(t)
	q := testQbit(t, f, "pw")
	for _, u := range []string{
		"https://nyaa.si/download/1.torrent", "http://nyaa.si/view/1", "https://nyaa.si.evil.com/view/1",
		"https://nyaa.si/view/1?x=http://internal/", "magnet:?xt=urn:btih:abc", "https://animebytes.tv/torrents.php?id=1",
	} {
		if _, err := q.AddNyaa(context.Background(), u, "sonarr"); err == nil {
			t.Errorf("AddNyaa(%q) accepted", u)
		}
	}
	if _, err := q.AddNyaa(context.Background(), "https://nyaa.si/view/1", "lidarr"); err == nil {
		t.Error("an unknown app was accepted")
	}
	if len(f.added) != 0 {
		t.Errorf("added %v", f.added)
	}
}

func TestQbitReportsFailures(t *testing.T) {
	f := newFakeQbit(t)
	if _, err := testQbit(t, f, "wrong").AddNyaa(context.Background(), "https://nyaa.si/view/1", "sonarr"); err == nil || !strings.Contains(err.Error(), "user name or password") {
		t.Errorf("wrong password: %v", err)
	}
	f.fail = true
	if _, err := testQbit(t, f, "pw").AddNyaa(context.Background(), "https://nyaa.si/view/1", "sonarr"); err == nil || !strings.Contains(err.Error(), "could not add") {
		t.Errorf("a Fails. answer: %v", err)
	}
}

func TestQbitEndpoint(t *testing.T) {
	f := newFakeQbit(t)
	store := NewStore("")
	srv := httptest.NewServer(NewServer(Options{Store: store, Qbit: testQbit(t, f, "pw"), ReportDir: t.TempDir(), Log: slog.New(slog.DiscardHandler)}).Handler)
	defer srv.Close()

	post := func(header bool, body string) int {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/api/qbit/add", bytes.NewBufferString(body))
		if header {
			req.Header.Set(csrfHeader, "1")
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := post(false, `{"url":"https://nyaa.si/view/5","arr":"sonarr"}`); code != http.StatusForbidden {
		t.Errorf("without the CSRF header: %d", code)
	}
	if code := post(true, `{"url":"https://nyaa.si/view/5","arr":"sonarr"}`); code != http.StatusOK {
		t.Errorf("a valid add: %d", code)
	}
	if code := post(true, `{"url":"https://example.com/","arr":"sonarr"}`); code != http.StatusBadGateway {
		t.Errorf("another address: %d", code)
	}
	res, err := http.Get(srv.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(res.Body)
	res.Body.Close()
	if !strings.Contains(buf.String(), `"qbit":{"enabled":true,"categories":{"radarr":"radarr","sonarr":"tv-sonarr"}}`) || strings.Contains(buf.String(), "pw") {
		t.Errorf("state = %s", buf.String())
	}
}

func TestQbitVersion5AndCheck(t *testing.T) {
	f := newFakeQbit(t)
	f.v5 = true
	q := testQbit(t, f, "pw")
	if v, err := q.Check(context.Background()); err != nil || v != "v5.0.2" {
		t.Fatalf("Check = %q, %v", v, err)
	}
	if _, err := q.AddNyaa(context.Background(), "https://nyaa.si/view/9", "sonarr"); err != nil {
		t.Fatalf("add on qBittorrent 5: %v", err)
	}
}

func TestQbitExplainsSecurityChecks(t *testing.T) {
	f := newFakeQbit(t)
	f.csrf401 = true
	_, err := testQbit(t, f, "pw").Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Host header validation") {
		t.Fatalf("a 401: %v", err)
	}
	q, _ := NewQbit(QbitConfig{URL: f.srv.URL})
	f.csrf401 = false
	if _, err := q.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "Bypass authentication") {
		t.Fatalf("no user name: %v", err)
	}
}
