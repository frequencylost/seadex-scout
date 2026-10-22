package webui

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
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

	log.Info("configuration loaded", "sonarr_enabled", true, "radarr_enabled", true, "poll_interval", "15m0s",
		"exclude_remux", false, "require_dual_audio", true, "exclude_specials", false, "animebytes", false,
		"include_tags", 0, "exclude_tags", 1, "ignored_findings", 2, "run_mode", "daemon")
	log.Info("seadex-scout started", "poll_interval", "15m0s", "indexer", true)
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
		kvs := []any{"title", s.title, "al_id", 1000 + i, "arr", s.arr, "arr_url", "https://example.com/" + s.arr,
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
			kvs = append(kvs, "recommended_bytes", s.size, "size_change_bytes", s.change)
		}
		log.Warn("better release available", kvs...)
	}
	log.Info("series spans multiple release groups, manual review", "title", "Cowboy Bebop", "al_id", 1,
		"arr", "sonarr", "season", 1, "status", "mixed_group_manual", "current_group", "Judas, YURI",
		"recommended_group", "MTBB", "alt_groups", "", "pass_id", pass, "first_seen", pass, "rank_check", 1)
	log.Info("SeaDex entry is incomplete", "title", "One Piece", "al_id", 21, "arr", "sonarr",
		"season", 1, "status", "incomplete", "pass_id", pass)
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
	srv := NewServer(Options{Addr: addr, ReportDir: dir, Store: store, Log: slog.Default()})
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
