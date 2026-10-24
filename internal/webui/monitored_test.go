package webui

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestMonitoredPollerReadsUnmonitored(t *testing.T) {
	var gotKey string
	sonarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Api-Key")
		if r.URL.Path != "/base/api/v3/series" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[{"id":1,"monitored":true,"seasons":[{"seasonNumber":1,"monitored":true},{"seasonNumber":2,"monitored":false}]},
			{"id":2,"monitored":false,"seasons":[]}]`))
	}))
	defer sonarr.Close()
	// A Radarr serving only API v5, to exercise the fallback.
	radarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v5/movie" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[{"id":7,"monitored":false},{"id":8,"monitored":true}]`))
	}))
	defer radarr.Close()

	store := NewStore("")
	p := NewMonitoredPoller(Arr{URL: sonarr.URL + "/base/", APIKey: "k1"}, Arr{URL: radarr.URL, APIKey: "k2"}, store, slog.New(slog.DiscardHandler))
	p.Refresh(context.Background())

	m := store.Snapshot().Monitored
	slices.Sort(m.Unmonitored)
	if want := []string{"radarr:7", "sonarr:1:2", "sonarr:2"}; !slices.Equal(m.Unmonitored, want) || m.Error != "" {
		t.Fatalf("Monitored = %+v, want %v", m, want)
	}
	if gotKey != "k1" {
		t.Errorf("API key header = %q", gotKey)
	}

	// A failed read keeps the last flags and reports the error.
	sonarr.Close()
	p.Refresh(context.Background())
	m = store.Snapshot().Monitored
	if len(m.Unmonitored) != 3 || m.Error == "" {
		t.Fatalf("after a failed read: %+v, want the last flags and an error", m)
	}
}
