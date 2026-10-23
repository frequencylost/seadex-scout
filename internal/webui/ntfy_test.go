package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeNtfy records what is published to it.
type fakeNtfy struct {
	mu   sync.Mutex
	msgs []ntfyMessage
	auth []string
	srv  *httptest.Server
}

func newFakeNtfy(t *testing.T) *fakeNtfy {
	t.Helper()
	f := &fakeNtfy{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m ntfyMessage
		if r.URL.Path != "/sub/" || json.NewDecoder(r.Body).Decode(&m) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.msgs = append(f.msgs, m)
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.mu.Unlock()
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeNtfy) messages() []ntfyMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ntfyMessage(nil), f.msgs...)
}

func newTestNotifier(t *testing.T, f *fakeNtfy, cfg NtfyConfig) (*Notifier, *Store, *slog.Logger) {
	t.Helper()
	store := NewStore("")
	cfg.TopicURL = f.srv.URL + "/sub/scout"
	if cfg.Events == nil {
		cfg.Events = AllEvents
	}
	n, err := NewNotifier(cfg, store, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return n, store, newLogger(store, &bytes.Buffer{}, slog.LevelInfo)
}

// drain hands every queued event to the notifier.
func drain(ctx context.Context, n *Notifier, events <-chan Event) {
	for {
		select {
		case ev := <-events:
			n.handle(ctx, ev)
		default:
			return
		}
	}
}

func logUpgrade(log *slog.Logger, pass int64, title string, afterStart bool, extra ...any) {
	log.Info("upgrade found", "pass_id", pass, "after_start", afterStart, "title", title, "al_id", 7,
		"arr", "sonarr", "season", 1, "recommended_group", "Okay-Subs")
	kvs := append([]any{"title", title, "al_id", 7, "arr", "sonarr", "arr_id", 3, "season", 1,
		"status", "better_release", "current_group", "SubsPlease", "current_tier", "unlisted",
		"recommended_group", "Okay-Subs", "nyaa_url", "https://nyaa.si/view/1", "arr_url", "http://sonarr/series/x",
		"recommended_bytes", int64(38 << 30), "size_change_bytes", int64(21 << 30),
		"alt_group", "Vodes", "alt_recommended_bytes", int64(12 << 30), "library_bytes", int64(34 << 30), "pass_id", pass}, extra...)
	log.Warn("better release available", kvs...)
	log.Info("findings reported", "pass_id", pass, "total", 1, "emitted", 1)
}

func TestNtfyNewUpgrade(t *testing.T) {
	f := newFakeNtfy(t)
	n, store, log := newTestNotifier(t, f, NtfyConfig{Token: "tk_secret", DashboardURL: "http://tower:9119/"})
	events := store.enableEvents()
	ctx := context.Background()

	logUpgrade(log, 1, "Restart Show", true) // after a start: not news
	logUpgrade(log, 2, "Frieren", false)
	drain(ctx, n, events)

	msgs := f.messages()
	if len(msgs) != 1 {
		t.Fatalf("sent %d messages, want 1: %+v", len(msgs), msgs)
	}
	m := msgs[0]
	if m.Topic != "scout" || m.Title != "Frieren · Season 1" || m.Click != "https://seadex.moe/7/" {
		t.Errorf("message = %+v", m)
	}
	for _, want := range []string{"SubsPlease → Okay-Subs", "Best: 38.0 GB (+21.0 GB)", "Alt: Vodes 12.0 GB (≈ −22.0 GB)"} {
		if !strings.Contains(m.Message, want) {
			t.Errorf("message %q lacks %q", m.Message, want)
		}
	}
	if len(m.Actions) != 3 || m.Actions[0].Label != "Nyaa" || m.Actions[1].Label != "Sonarr" || m.Actions[2].Label != "Dashboard" {
		t.Errorf("actions = %+v", m.Actions)
	}
	if f.auth[0] != "Bearer tk_secret" {
		t.Errorf("auth = %q", f.auth[0])
	}
	if st := store.Snapshot().Notify; !st.Enabled || st.Sent != 1 || strings.Contains(st.Target, "tk_") {
		t.Errorf("status = %+v", st)
	}
}

func TestNtfySkipsOptionalAndUnmonitored(t *testing.T) {
	f := newFakeNtfy(t)
	n, store, log := newTestNotifier(t, f, NtfyConfig{})
	events := store.enableEvents()
	ctx := context.Background()

	logUpgrade(log, 1, "Optional", false, "current_tier", "alt")
	store.setMonitored([]string{"sonarr:3:1"}, "")
	logUpgrade(log, 2, "Unmonitored", false)
	drain(ctx, n, events)
	if msgs := f.messages(); len(msgs) != 0 {
		t.Fatalf("sent %+v, want nothing", msgs)
	}

	n.cfg.Optional, n.cfg.Unmonitored = true, true
	logUpgrade(log, 3, "Both", false, "current_tier", "alt")
	drain(ctx, n, events)
	if msgs := f.messages(); len(msgs) != 1 {
		t.Fatalf("with both on, sent %d, want 1", len(msgs))
	}
}

func TestNtfyProblemStartAndEnd(t *testing.T) {
	f := newFakeNtfy(t)
	n, store, log := newTestNotifier(t, f, NtfyConfig{})
	events := store.enableEvents()
	ctx := context.Background()
	start := time.Now()
	store.now = func() time.Time { return start }
	log.Info("configuration loaded", "poll_interval", "1h0m0s")

	log.Error("seadex unreachable", "condition", "seadex-unreachable", "error", "timeout")
	log.Error("seadex unreachable", "condition", "seadex-unreachable", "error", "timeout") // the next check
	drain(ctx, n, events)
	if msgs := f.messages(); len(msgs) != 1 || !strings.Contains(msgs[0].Title, "SeaDex unreachable") || msgs[0].Priority != 4 {
		t.Fatalf("after the problem: %+v, want one warning", msgs)
	}

	// Two hours with no further line: one and a half intervals have passed.
	for _, ev := range store.sweep(start.Add(2 * time.Hour)) {
		n.handle(ctx, ev)
	}
	if msgs := f.messages(); len(msgs) != 2 || !strings.HasPrefix(msgs[1].Title, "Resolved: SeaDex unreachable") {
		t.Fatalf("after it cleared: %+v", msgs)
	}
}

func TestNtfyStallAndErrorsAreHeldBack(t *testing.T) {
	f := newFakeNtfy(t)
	n, store, log := newTestNotifier(t, f, NtfyConfig{})
	events := store.enableEvents()
	ctx := context.Background()
	log.Info("configuration loaded", "poll_interval", "1h0m0s")

	log.Error("library walk failed", "error", "sonarr returned 503")
	log.Error("library walk failed", "error", "sonarr returned 503")
	drain(ctx, n, events)
	if msgs := f.messages(); len(msgs) != 1 {
		t.Fatalf("a repeated error sent %d messages, want 1", len(msgs))
	}

	evs := store.sweep(store.started.Add(4 * time.Hour))
	if len(evs) != 1 || evs[0].Key != "stall" || evs[0].Resolved {
		t.Fatalf("sweep after 4h = %+v, want a stall", evs)
	}
	if evs := store.sweep(store.started.Add(5 * time.Hour)); len(evs) != 0 {
		t.Fatalf("a stall announced twice: %+v", evs)
	}
	log.Info("tick complete")
	if evs := store.sweep(time.Now()); len(evs) != 1 || !evs[0].Resolved {
		t.Fatalf("after a check: %+v, want the stall resolved", evs)
	}
}

func TestNtfyRejectsBadURLs(t *testing.T) {
	for _, u := range []string{"", "ntfy.sh/topic", "ftp://ntfy.sh/topic", "https://ntfy.sh/", "https://ntfy.sh"} {
		if _, err := NewNotifier(NtfyConfig{TopicURL: u}, NewStore(""), slog.New(slog.DiscardHandler)); err == nil {
			t.Errorf("NewNotifier(%q) accepted", u)
		}
	}
	n, err := NewNotifier(NtfyConfig{TopicURL: "https://ntfy.example.com/base/alerts"}, NewStore(""), slog.New(slog.DiscardHandler))
	if err != nil || n.server != "https://ntfy.example.com/base" || n.topic != "alerts" {
		t.Errorf("server %q topic %q err %v", n.server, n.topic, err)
	}
}
