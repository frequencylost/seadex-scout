package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newLogger returns a logger whose lines reach store and are written as JSON
// to buf, at level.
func newLogger(store *Store, buf *bytes.Buffer, level slog.Level) *slog.Logger {
	inner := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: level})
	return slog.New(NewHandler(inner, store))
}

func logPass(log *slog.Logger, pass int64, titles ...string) {
	for i, t := range titles {
		log.Warn("better release available", "title", t, "al_id", 100+i, "arr", "sonarr",
			"season", 1, "status", "better_release", "current_group", "Old", "current_tier", "unlisted",
			"recommended_group", "New", "pass_id", pass, "first_seen", int64(1), "rank_alt", i+1, "rank_none", i+1,
			"recommended_bytes", int64(1<<30), "size_change_bytes", int64(-1<<20))
	}
	log.Info("series spans multiple release groups, manual review", "title", "Mixed", "al_id", 9,
		"arr", "sonarr", "status", "mixed_group_manual", "pass_id", pass, "rank_check", 1)
	log.Info("upgrade sizes", "pass_id", pass, "hidden_tier", "alt", "upgrades", len(titles))
	log.Info("findings reported", "total", len(titles)+1, "emitted", len(titles)+1, "suppressed", 0,
		"pass_id", pass, "manual_review", 1)
}

func TestPassCommitsOnFindingsReported(t *testing.T) {
	store := NewStore("")
	var buf bytes.Buffer
	log := newLogger(store, &buf, slog.LevelInfo)

	logPass(log, 1, "A", "B")
	// A second pass that has not closed yet must not replace the first.
	log.Warn("better release available", "title", "C", "al_id", 1, "pass_id", int64(2))

	snap := store.Snapshot()
	if snap.Pass.ID != 1 || len(snap.Findings) != 3 {
		t.Fatalf("pass %d with %d findings, want pass 1 with 3", snap.Pass.ID, len(snap.Findings))
	}
	if snap.Pass.ManualReview != 1 || snap.Views["alt"] != 2 {
		t.Fatalf("pass = %+v views = %v", snap.Pass, snap.Views)
	}

	logPass(log, 3, "Z")
	snap = store.Snapshot()
	if snap.Pass.ID != 3 || len(snap.Findings) != 2 || snap.Findings[0].Str("title") != "Z" {
		t.Fatalf("after pass 3: id %d, findings %+v", snap.Pass.ID, snap.Findings)
	}
	if len(store.pending) != 0 {
		t.Fatalf("pending passes left behind: %v", store.pending)
	}
	// Every line still reaches the real handler.
	if got := strings.Count(buf.String(), "\n"); got != 10 {
		t.Fatalf("inner handler wrote %d lines, want 10", got)
	}
}

func TestInfoLinesSeenAtWarnLevel(t *testing.T) {
	store := NewStore("")
	var buf bytes.Buffer
	log := newLogger(store, &buf, slog.LevelWarn)

	log.Info("library summary", "rows", 10, "have_best", 7)
	log.Info("cycle complete", "duration", 90*time.Second, "library_items", 3)
	log.Debug("noise")

	snap := store.Snapshot()
	if snap.Library == nil || snap.Library.Int("have_best") != 7 {
		t.Fatalf("library summary not recorded: %+v", snap.Library)
	}
	if snap.LastFullCheck == nil || snap.LastFullCheck.Str("duration") != "1m30s" {
		t.Fatalf("cycle not recorded: %+v", snap.LastFullCheck)
	}
	if buf.Len() != 0 {
		t.Fatalf("info lines leaked past the warn level: %s", buf.String())
	}
	for _, l := range store.Logs() {
		if l.Msg == "noise" {
			t.Fatal("debug line recorded")
		}
	}
}

func TestErrorsAndConditions(t *testing.T) {
	store := NewStore("")
	log := newLogger(store, &bytes.Buffer{}, slog.LevelInfo)

	log.Error("library walk failed", "arr", "sonarr", "error", os.ErrDeadlineExceeded)
	log.Error("seadex unreachable", "condition", "seadex-unreachable")

	snap := store.Snapshot()
	if len(snap.Errors) != 1 || snap.Errors[0].Str("error") != os.ErrDeadlineExceeded.Error() {
		t.Fatalf("errors = %+v", snap.Errors)
	}
	if _, ok := snap.Conditions["seadex-unreachable"]; !ok {
		t.Fatalf("condition not recorded: %+v", snap.Conditions)
	}
}

func TestWithAttrsAndGroups(t *testing.T) {
	store := NewStore("")
	log := newLogger(store, &bytes.Buffer{}, slog.LevelInfo)

	log.With("arr", "radarr").WithGroup("g").Info("tick complete", "n", 1, slog.Group("sub", "x", "y"))
	l := store.Snapshot().LastCheck
	if l == nil || l.Str("arr") != "radarr" || l.Int("g.n") != 1 || l.Str("g.sub.x") != "y" {
		t.Fatalf("attrs = %+v", l)
	}
}

func TestSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webui-state.json")
	store := NewStore(path)
	log := newLogger(store, &bytes.Buffer{}, slog.LevelInfo)
	logPass(log, 5, "Persisted")
	log.Info("library summary", "rows", 4)
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != stateFileMode {
		t.Fatalf("mode %v, want %v", info.Mode().Perm(), os.FileMode(stateFileMode))
	}

	again := NewStore(path)
	if err := again.Load(); err != nil {
		t.Fatal(err)
	}
	snap := again.Snapshot()
	if snap.Pass.ID != 5 || len(snap.Findings) != 2 || snap.Findings[0].Str("title") != "Persisted" ||
		snap.Library.Int("rows") != 4 || snap.Findings[0].Int("recommended_bytes") != 1<<30 {
		t.Fatalf("reloaded snapshot = %+v", snap)
	}
}

func TestLoadRejectsOtherVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webui-state.json")
	if err := os.WriteFile(path, []byte(`{"version":99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(path).Load(); err == nil {
		t.Fatal("want an error for an unknown version")
	}
	if err := NewStore(filepath.Join(t.TempDir(), "missing.json")).Load(); err != nil {
		t.Fatalf("missing file: %v", err)
	}
}

func TestRingsAreBounded(t *testing.T) {
	store := NewStore("")
	log := newLogger(store, &bytes.Buffer{}, slog.LevelInfo)
	for range maxErrors + 50 {
		log.Error("boom")
	}
	for i := range maxPending + 3 {
		log.Warn("better release available", "pass_id", int64(i))
	}
	if n := len(store.Snapshot().Errors); n != maxErrors {
		t.Fatalf("%d errors kept, want %d", n, maxErrors)
	}
	if n := len(store.pending); n != maxPending {
		t.Fatalf("%d pending passes kept, want %d", n, maxPending)
	}
}

func TestReportFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"report-20260101T000000Z.json", "report-20260101T000000Z.md", "report-20260102T000000Z.json", "state.json", "notes.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	list, err := ListReports(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Stem != "report-20260102T000000Z" || !list[1].Markdown || list[0].Markdown {
		t.Fatalf("list = %+v", list)
	}
	for _, bad := range []string{"../state.json", "state.json", "report-x/../../y.json", "report-.json/x"} {
		if f, err := OpenReport(dir, bad); err == nil {
			f.Close()
			t.Fatalf("OpenReport accepted %q", bad)
		}
	}
	missing, err := ListReports(filepath.Join(dir, "nope"))
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing dir: %v %v", missing, err)
	}
}

func newTestServer(t *testing.T, password string) (*httptest.Server, *Store) {
	t.Helper()
	store := NewStore("")
	logPass(newLogger(store, &bytes.Buffer{}, slog.LevelInfo), 1, "Served")
	srv := NewServer(Options{
		Password: password, Username: "admin", ReportDir: t.TempDir(), Store: store,
		Log: slog.New(slog.DiscardHandler),
	})
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)
	return ts, store
}

func TestServerServesStateAndPage(t *testing.T) {
	ts, _ := newTestServer(t, "")
	res, err := http.Get(ts.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var snap Snapshot
	if err := json.NewDecoder(res.Body).Decode(&snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Findings) != 2 || snap.Findings[0].Str("title") != "Served" {
		t.Fatalf("state = %+v", snap)
	}
	for _, path := range []string{"/", "/app.js", "/app.css", "/favicon.svg"} {
		r, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusOK || r.Header.Get("Content-Security-Policy") == "" {
			t.Fatalf("%s: %d, CSP %q", path, r.StatusCode, r.Header.Get("Content-Security-Policy"))
		}
	}
}

func TestServerAuth(t *testing.T) {
	ts, _ := newTestServer(t, "s3cret")
	check := func(user, pass string, want int) {
		t.Helper()
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/api/state", http.NoBody)
		if user != "" {
			req.SetBasicAuth(user, pass)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("%s/%s: %d, want %d", user, pass, res.StatusCode, want)
		}
	}
	check("", "", http.StatusUnauthorized)
	check("admin", "wrong", http.StatusUnauthorized)
	check("other", "s3cret", http.StatusUnauthorized)
	check("admin", "s3cret", http.StatusOK)

	res, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("healthz behind auth: %d", res.StatusCode)
	}
}

func TestReportRunNeedsCSRFHeader(t *testing.T) {
	ts, _ := newTestServer(t, "")
	res, err := http.Post(ts.URL+"/api/reports/run", "text/plain", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("POST without header: %d, want 403", res.StatusCode)
	}
}

func TestReportRunnerRunsChild(t *testing.T) {
	// A stand-in for the binary: a shell script that logs like a report run.
	dir := t.TempDir()
	exe := filepath.Join(dir, "fake")
	script := "#!/bin/sh\necho '{\"level\":\"INFO\",\"msg\":\"report written\",\"anime\":3}'\n"
	if err := os.WriteFile(exe, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	store := NewStore("")
	rr := NewReportRunner(context.Background(), exe, store)
	var out bytes.Buffer
	rr.out = &out
	if err := rr.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for store.reportJob().Running {
		if time.Now().After(deadline) {
			t.Fatal("report run did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	job := store.reportJob()
	if job.Error != "" || job.Result != "report written" || !strings.Contains(out.String(), "report written") {
		t.Fatalf("job = %+v, out = %q", job, out.String())
	}
}
