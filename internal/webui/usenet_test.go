package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const gunbuster = "[hchcsen] Aim for the Top! GunBuster & DieBuster (Top o Nerae 1 & 2!) [BD 1080p x264 Hi10P FLAC]"

// fakeUsenet stands in for Nyaa, Prowlarr and SABnzbd at once.
type fakeUsenet struct {
	mu       sync.Mutex
	queries  []string
	added    []url.Values
	nyaaHits int
	srv      *httptest.Server
}

func newFakeUsenet(t *testing.T) *fakeUsenet {
	t.Helper()
	f := &fakeUsenet{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case strings.HasPrefix(r.URL.Path, "/view/"):
			f.nyaaHits++
			// The title, then a file list naming a folder that must not be read.
			_, _ = w.Write([]byte(`<html><head><title>[hchcsen] Aim for the Top! GunBuster &amp; DieBuster (Top o Nerae 1 &amp; 2!) [BD 1080p x264 Hi10P FLAC] :: Nyaa</title></head>
<body><h3 class="panel-title">` + gunbuster + `</h3><a class="folder">Aim for The Top [hchcsen]</a></body></html>`))
		case r.URL.Path == "/api/v1/search":
			if r.Header.Get("X-Api-Key") != "pk" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			f.queries = append(f.queries, r.URL.Query().Get("query"))
			_, _ = w.Write([]byte(`[
				{"guid":"t1","title":"` + strings.ReplaceAll(gunbuster, `"`, `\"`) + `","indexer":"Nyaa","size":5,"protocol":"torrent","downloadUrl":"magnet:x"},
				{"guid":"u1","title":"Aim for the Top GunBuster","indexer":"NZBgeek","size":7,"protocol":"usenet","downloadUrl":"http://prowlarr/1/download?link=a","publishDate":"2026-10-20T00:00:00Z"},
				{"guid":"u2","title":"[hchcsen] Aim for the Top! GunBuster & DieBuster (Top o Nerae 1 & 2!) [BD 1080p x264 Hi10P FLAC]","indexer":"AnimeTosho","size":9,"grabs":3,"protocol":"usenet","downloadUrl":"http://prowlarr/2/download?link=b","publishDate":"2020-01-01T00:00:00Z"}
			]`))
		case r.URL.Path == "/api/v1/system/status":
			_, _ = w.Write([]byte(`{"version":"1.30.0"}`))
		case r.URL.Path == "/api":
			q := r.URL.Query()
			if q.Get("apikey") != "sk" {
				_, _ = w.Write([]byte("API Key Incorrect"))
				return
			}
			switch q.Get("mode") {
			case "addurl":
				f.added = append(f.added, q)
				_, _ = w.Write([]byte(`{"status":true,"nzo_ids":["SABnzbd_nzo_1"]}`))
			case "get_cats":
				_, _ = w.Write([]byte(`{"categories":["*","tv","movies"]}`))
			case "version":
				_, _ = w.Write([]byte(`{"version":"4.4.0"}`))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func testUsenet(t *testing.T, f *fakeUsenet, sabKey string) *Usenet {
	t.Helper()
	u, err := NewUsenet(UsenetConfig{ProwlarrURL: f.srv.URL, ProwlarrKey: "pk", SabURL: f.srv.URL + "/", SabKey: sabKey,
		Categories: map[string]string{"sonarr": "tv", "radarr": "movies"}})
	if err != nil {
		t.Fatal(err)
	}
	u.nyaa = f.srv.URL
	return u
}

func TestUsenetSearchesTheNyaaTitle(t *testing.T) {
	f := newFakeUsenet(t)
	u := testUsenet(t, f, "sk")
	ctx := context.Background()

	found, err := u.Search(ctx, "https://nyaa.si/view/1538192")
	if err != nil {
		t.Fatal(err)
	}
	if found.Query != gunbuster || f.queries[0] != gunbuster {
		t.Errorf("searched %q (sent %q), want the page's release title", found.Query, f.queries[0])
	}
	if len(found.Results) != 2 || found.Other != 1 {
		t.Fatalf("results = %+v, other %d; want the two usenet ones", found.Results, found.Other)
	}
	if r := found.Results[0]; !r.Exact || r.Indexer != "AnimeTosho" || found.Results[1].Exact {
		t.Errorf("exact match not first: %+v", found.Results)
	}

	// The title is cached: a second search does not fetch Nyaa again.
	if _, err := u.Search(ctx, "https://nyaa.si/view/1538192"); err != nil || f.nyaaHits != 1 {
		t.Errorf("second search: err %v, Nyaa fetched %d times", err, f.nyaaHits)
	}

	cat, err := u.Send(ctx, found.Token, found.Results[0].ID, "radarr")
	if err != nil || cat != "movies" {
		t.Fatalf("Send = %q, %v", cat, err)
	}
	got := f.added[0]
	if got.Get("name") != "http://prowlarr/2/download?link=b" || got.Get("cat") != "movies" || got.Get("nzbname") != gunbuster {
		t.Errorf("SABnzbd got %v", got)
	}
}

func TestUsenetRefusals(t *testing.T) {
	f := newFakeUsenet(t)
	u := testUsenet(t, f, "sk")
	ctx := context.Background()
	if _, err := u.Search(ctx, "https://example.com/view/1"); err == nil {
		t.Error("searched a page that is not Nyaa")
	}
	found, err := u.Search(ctx, "https://nyaa.si/view/1538192")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.Send(ctx, "made-up", 1, "sonarr"); err == nil {
		t.Error("sent from an unknown search")
	}
	if _, err := u.Send(ctx, found.Token, 99, "sonarr"); err == nil {
		t.Error("sent a result the search did not return")
	}
	if _, err := u.Send(ctx, found.Token, 1, "lidarr"); err == nil {
		t.Error("sent for an unknown app")
	}
	u.now = func() time.Time { return time.Now().Add(time.Hour) }
	if _, err := u.Send(ctx, found.Token, 1, "sonarr"); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("an expired search: %v", err)
	}
	if len(f.added) != 0 {
		t.Errorf("SABnzbd got %v", f.added)
	}
}

func TestUsenetCheck(t *testing.T) {
	f := newFakeUsenet(t)
	p, s, err := testUsenet(t, f, "sk").Check(context.Background())
	if err != nil || p != "1.30.0" || s != "4.4.0" {
		t.Fatalf("Check = %q %q %v", p, s, err)
	}
	if _, _, err := testUsenet(t, f, "wrong").Check(context.Background()); err == nil || !strings.Contains(err.Error(), "API Key Incorrect") {
		t.Errorf("a wrong SABnzbd key: %v", err)
	}
	u := testUsenet(t, f, "sk")
	u.cfg.Categories["sonarr"] = "anime"
	if _, _, err := u.Check(context.Background()); err == nil || !strings.Contains(err.Error(), `no category "anime"`) {
		t.Errorf("a missing category: %v", err)
	}
}

func TestUsenetErrorsHideTheSABnzbdKey(t *testing.T) {
	u, err := NewUsenet(UsenetConfig{ProwlarrURL: "http://127.0.0.1:1", ProwlarrKey: "pk", SabURL: "http://127.0.0.1:1", SabKey: "secretkey"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = u.Check(context.Background())
	if err == nil {
		t.Fatal("reached a closed port")
	}
	u.cfg.ProwlarrURL = newFakeUsenet(t).srv.URL
	_, _, err = u.Check(context.Background())
	if err == nil || strings.Contains(err.Error(), "secretkey") {
		t.Errorf("error = %v", err)
	}
}
