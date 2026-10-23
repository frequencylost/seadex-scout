package webui

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDemo serves the dashboard with made-up data, for working on the page
// without a Sonarr library. It is skipped unless WEBUI_DEMO_ADDR is set:
//
//	WEBUI_DEMO_ADDR=127.0.0.1:9119 go test -run TestDemo -timeout 0 ./internal/webui/
func TestDemo(t *testing.T) {
	addr := os.Getenv("WEBUI_DEMO_ADDR")
	if addr == "" {
		t.Skip("set WEBUI_DEMO_ADDR to serve the demo dashboard")
	}
	store := NewStore("")
	log := newLogger(store, &bytes.Buffer{}, slog.LevelInfo)
	now := time.Now()
	pass := now.UnixMilli()

	log.Info("configuration loaded", "sonarr_enabled", true, "radarr_enabled", true, "poll_interval", "1h0m0s",
		"exclude_remux", false, "require_dual_audio", true, "exclude_specials", false, "animebytes", false,
		"include_tags", 0, "exclude_tags", 1, "ignored_findings", 2, "run_mode", "daemon")
	log.Info("seadex-scout started", "poll_interval", "1h0m0s", "indexer", true)
	for i := range 14 {
		d := time.Duration(18+i%9)*time.Minute + 13*time.Second
		if i == 9 {
			log.Warn("cycle degraded", "reason", "anilist-degraded", "duration", d, "library_items", 412, "findings", 15)
		} else {
			log.Info("cycle complete", "duration", d, "library_items", 412, "findings", 15, "specials_unread", 0)
		}
	}
	log.Info("reconcile complete")
	log.Info("library summary", "rows", 538, "have_best", 301, "have_alt", 88, "have_older_revision", 4,
		"have_unlisted", 61, "no_file", 52, "unverified", 9, "unattributed", 12, "withheld", 3, "not_on_seadex", 8,
		"anime_items", 297, "items_with_entry", 281, "items_all_best", 171, "items_all_best_or_alt", 214)

	shows := []struct {
		title, arr, cur, tier, best, status string
		season                              int
		size, change                        int64
	}{
		{"Frieren: Beyond Journey's End", "sonarr", "SubsPlease", "unlisted", "Okay-Subs", "better_release", 1, 38 << 30, 21 << 30},
		{"Vinland Saga", "sonarr", "Judas", "unlisted", "Kametsu", "better_release", 2, 52 << 30, 30 << 30},
		{"Bocchi the Rock!", "sonarr", "smol", "", "smol", "newer_revision", 1, 7 << 30, 1 << 28},
		{"Your Name.", "radarr", "Erai-raws", "unlisted", "Arid", "better_release", 0, 9 << 30, 6 << 30},
		{"Mob Psycho 100", "sonarr", "Beatrice-Raws", "alt", "Vodes", "better_release", 3, 24 << 30, -(2 << 30)},
		{"Odd Taxi", "sonarr", "ASW", "unlisted", "Kawaiika-Raws", "better_release", 1, 0, 0},
		{"Spirited Away", "radarr", "Tsundere-Raws", "alt", "Arid", "better_release", 0, 14 << 30, 3 << 30},
	}
	for i, s := range shows {
		kvs := []any{"title", s.title, "al_id", 1000 + i, "arr", s.arr, "arr_id", 10 + i, "arr_url", "https://example.com/" + s.arr,
			"season", s.season, "status", s.status, "current_group", s.cur, "current_tier", s.tier,
			"recommended_group", s.best, "alt_groups", "", "nyaa_url", fmt.Sprintf("https://nyaa.si/view/%d", 1700000+i),
			"seadex_tags", "best · 1080p · dual-audio", "pass_id", pass,
			"first_seen", now.Add(-time.Duration(i*7) * time.Hour).UnixMilli(), "rank_alt", i + 1, "rank_none", i + 1}
		if s.status == "newer_revision" {
			kvs = append(kvs, "current_revision", "v1", "recommended_revision", "v2")
		}
		if s.tier == "alt" {
			kvs[len(kvs)-3] = 0 // rank_alt: hidden when optional upgrades are hidden
		}
		if s.size > 0 {
			kvs = append(kvs, "recommended_bytes", s.size, "size_change_bytes", s.change, "library_bytes", s.size-s.change)
		}
		if i%3 != 2 {
			kvs = append(kvs, "alt_group", "Vodes", "alt_seadex_tags", "alt · encode · 1080p · dual-audio",
				"seadex_notes", "Okay-Subs is a BD encode with fixed subtitles.\nVodes is a smaller encode with slightly less detail.",
				"alt_nyaa_url", fmt.Sprintf("https://nyaa.si/view/%d", 1800000+i),
				"alt_recommended_bytes", s.size/3)
			if i != 1 { // Vinland Saga's alt shows the approximate change
				kvs = append(kvs, "alt_size_change_bytes", s.change/3-(1<<30))
			}
			for k := 0; k < len(kvs); k += 2 {
				if kvs[k] == "alt_groups" {
					kvs[k+1] = "vodes,judas"
				}
			}
		}
		log.Warn("better release available", kvs...)
	}
	log.Info("series spans multiple release groups, manual review", "title", "Cowboy Bebop", "al_id", 1,
		"arr", "sonarr", "season", 1, "status", "mixed_group_manual", "current_group", "Judas, YURI",
		"recommended_group", "MTBB", "nyaa_url", "https://nyaa.si/view/47", "recommended_bytes", int64(60<<30),
		"size_change_bytes", int64(25<<30), "library_bytes", int64(35<<30), "alt_groups", "", "pass_id", pass,
		"first_seen", pass, "rank_check", 1)
	log.Info("SeaDex lists a theoretical best only", "title", "One Piece", "al_id", 21, "arr", "sonarr",
		"season", 1, "status", "theoretical_best", "no_best_reason", "theoretical", "current_group", "Erai-raws",
		"alt_group", "Judas", "alt_groups", "judas", "alt_nyaa_url", "https://nyaa.si/view/42",
		"alt_recommended_bytes", int64(90<<30), "alt_size_change_bytes", int64(20<<30), "library_bytes", int64(70<<30), "pass_id", pass)
	log.Info("SeaDex entry not downloaded", "title", "Dandadan", "al_id", 171018, "arr", "sonarr",
		"season", 2, "status", "not_downloaded", "recommended_group", "Kawaiika-Raws", "nyaa_url", "https://nyaa.si/view/43",
		"recommended_bytes", int64(16<<30), "alt_group", "Vodes", "alt_groups", "vodes", "alt_nyaa_url", "https://nyaa.si/view/44",
		"alt_recommended_bytes", int64(5<<30), "pass_id", pass, "first_seen", pass)
	log.Info("SeaDex entry not downloaded", "title", "Perfect Blue", "al_id", 437, "arr", "radarr",
		"status", "not_downloaded", "no_best_reason", "unobtainable", "alt_groups", "", "pass_id", pass, "first_seen", pass)
	log.Info("library holds the SeaDex best", "title", "Kaguya-sama: Love Is War", "al_id", 101921, "arr", "sonarr",
		"season", 1, "status", "at_best", "current_group", "Okay-Subs", "recommended_group", "Okay-Subs",
		"nyaa_url", "https://nyaa.si/view/45", "recommended_bytes", int64(22<<30), "size_change_bytes", int64(0),
		"library_bytes", int64(22<<30), "alt_group", "smol", "alt_groups", "smol", "alt_nyaa_url", "https://nyaa.si/view/46",
		"alt_recommended_bytes", int64(6<<30), "alt_size_change_bytes", -int64(16<<30), "pass_id", pass, "first_seen", pass)
	log.Info("SeaDex lists no best you can get", "title", "Akira", "al_id", 47, "arr", "radarr",
		"status", "no_best", "no_best_reason", "unobtainable", "current_group", "Arid", "current_tier", "alt",
		"alt_group", "Arid", "alt_groups", "arid", "library_bytes", int64(40<<30), "pass_id", pass, "first_seen", pass)
	log.Info("upgrade found", "pass_id", pass, "after_start", false, "title", "Frieren: Beyond Journey's End",
		"al_id", 1000, "arr", "sonarr", "season", 1, "current_group", "SubsPlease", "recommended_group", "Okay-Subs")
	log.Info("upgrade resolved", "pass_id", pass, "after_start", false, "title", "Made in Abyss",
		"al_id", 97986, "arr", "sonarr", "season", 1, "current_group", "Erai-raws", "recommended_group", "LostYears")
	log.Info("findings reported", "total", 9, "emitted", 9, "suppressed", 0, "pass_id", pass, "manual_review", 1)
	log.Info("tick complete")
	log.Error("library walk failed", "arr", "radarr", "error", "radarr returned 503")
	log.Error("seadex catalogue fetch failing", "condition", "seadex-catalogue-fetch-failing")
	log.Info("indexer request", "scope", "nyaa", "feed", true, "t", "search", "curated", 4, "returned", 12)
	log.Info("indexer feed snapshot written", "journal_new", 3, "scope", "all", "nyaa_feed", 812, "ab_feed", 0,
		"ab_releases_skipped", 0)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "report-20261022T101500Z.json"), []byte(demoReport), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "report-20261022T101500Z.md"), []byte("# Report\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Vinland Saga's season 2 and Your Name. are unmonitored.
	store.setMonitored([]string{"sonarr:11:2", "radarr:13"}, "")
	// A stand-in qBittorrent that accepts everything.
	fakeQbit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("Ok."))
	}))
	defer fakeQbit.Close()
	qbit, err := NewQbit(QbitConfig{URL: fakeQbit.URL, Categories: map[string]string{"sonarr": "tv-sonarr", "radarr": "radarr"}})
	if err != nil {
		t.Fatal(err)
	}
	// A stand-in Nyaa, Prowlarr and SABnzbd for the → Prowlarr buttons.
	usenet := testUsenet(t, newFakeUsenet(t), "sk")
	// A stand-in library refresh that takes a few seconds.
	refresher := NewLibraryRefresher(context.Background(), func(ctx context.Context) error {
		time.Sleep(4 * time.Second)
		return nil
	}, store)
	srv := NewServer(Options{Addr: addr, ReportDir: dir, Store: store, Qbit: qbit, Usenet: usenet, Refresher: refresher, Log: slog.Default()})
	t.Logf("demo dashboard on http://%s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		t.Fatal(err)
	}
}

const demoReport = `{"generated_at":"2026-10-22T10:15:00Z","totals":{"have_best":2,"have_unlisted":1,"no_file":1},
"rows":[
{"title":"Frieren: Beyond Journey's End","arr":"sonarr","seadex_url":"https://seadex.moe/154587/","verdict":"have_unlisted","match_source":"id","current_groups":["SubsPlease"],"releases":[{"tracker":"Nyaa","group":"Okay-Subs","url":"https://nyaa.si/view/1","best":true}],"al_id":154587,"season":1,"scope":"season"},
{"title":"Bocchi the Rock!","arr":"sonarr","seadex_url":"https://seadex.moe/130003/","verdict":"have_best","match_source":"id","current_groups":["smol"],"releases":[{"tracker":"Nyaa","group":"smol","best":true}],"al_id":130003,"season":1,"scope":"season"},
{"title":"Your Name.","arr":"radarr","seadex_url":"https://seadex.moe/21519/","verdict":"have_best","match_source":"id","current_groups":["Arid"],"releases":[{"tracker":"Nyaa","group":"Arid","best":true}],"al_id":21519,"scope":"movie"},
{"title":"Cowboy Bebop","arr":"sonarr","seadex_url":"https://seadex.moe/1/","verdict":"no_file","match_source":"id","al_id":1,"season":2,"scope":"season"}
],"items":{"anime":3,"with_entry":3,"all_best":2,"all_best_or_alt":2}}`
