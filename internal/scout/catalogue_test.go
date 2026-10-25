package scout

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v3/sonarr"
	"github.com/cplieger/seadex-scout/internal/arrwalk"
	"github.com/cplieger/seadex-scout/internal/compare"
	"github.com/cplieger/seadex-scout/internal/match"
	"github.com/cplieger/seadex-scout/internal/notify"
	"github.com/cplieger/seadex-scout/internal/state"
	"github.com/cplieger/slogx/capture"
)

// catalogueScout is a scout over one Frieren series saving its catalogue in dir.
func catalogueScout(dir string, sea *fakeSeaDex) (*Scout, *fakeStore, *capture.Recorder) {
	logger, recorder := capture.New()
	store := &fakeStore{st: state.State{Mapping: frierenMappingCache()}}
	son := &fakeSonarr{
		series: []sonarr.Series{{ID: 7, Title: "Frieren", IDs: sonarr.ExternalIDs{TVDB: 123}, Year: 2023}},
		files:  map[int][]sonarr.EpisodeFile{7: {{Season: 1, ReleaseGroup: "Erai-raws"}}},
	}
	s := New(&Deps{
		Logger:          logger,
		Store:           store,
		Library:         arrwalk.NewWalker(&arrwalk.Config{Sonarr: onePass{son}, Logger: scoutTestLogger()}),
		Mapping:         fakeMapping{},
		SeaDex:          sea,
		Matcher:         match.New(notFoundAniList{}, scoutTestLogger()),
		Comparer:        compare.New(compare.Config{}),
		Notifier:        notify.NewNotifier(logger, nil),
		CatalogueDir:    dir,
		CatalogueMaxAge: DefaultCatalogueMaxAge,
	})
	return s, store, recorder
}

// TestSavedCatalogueKeepsComparingThroughAnOutage pins the fallback: a fresh
// start with SeaDex down compares against the copy the last full check saved,
// ends degraded, and still counts the SeaDex failure.
func TestSavedCatalogueKeepsComparingThroughAnOutage(t *testing.T) {
	dir := t.TempDir()
	first, _, _ := catalogueScout(dir, &fakeSeaDex{entries: seadexFrierenEntry()})
	if !first.Cycle(t.Context()) {
		t.Fatal("the first check failed")
	}
	if _, err := os.Stat(filepath.Join(dir, CatalogueFile)); err != nil {
		t.Fatalf("no saved catalogue: %v", err)
	}

	// A restart while SeaDex is down: nothing in memory, the saved copy on disk.
	s, store, recorder := catalogueScout(dir, &fakeSeaDex{err: errors.New("seadex down")})
	if !s.Cycle(t.Context()) {
		t.Fatal("the check during the outage was unhealthy")
	}
	if recorder.CountExact("better release available") != 1 || recorder.CountExact("findings reported") != 1 {
		t.Error("the saved catalogue was not compared against")
	}
	if recorder.CountExact("cycle degraded") != 1 || recorder.CountExact("cycle complete") != 0 {
		t.Error("the pass did not end degraded")
	}
	if store.st.SeadexFailures != 1 {
		t.Errorf("SeaDex failure streak = %d, want 1: the outage still counts", store.st.SeadexFailures)
	}
	found := false
	for _, r := range recorder.Records() {
		r.Attrs(func(a slog.Attr) bool {
			if r.Message == "seadex catalogue in use" && a.Key == "source" && a.Value.String() == "saved" {
				found = true
			}
			return true
		})
	}
	if !found {
		t.Error("no 'seadex catalogue in use' line with source saved")
	}
}

// TestSavedCatalogueTooOldIsKeptNotUsed pins the age limit: an old copy stays
// on disk, but neither a full check nor a local refresh uses it.
func TestSavedCatalogueTooOldIsKeptNotUsed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, CatalogueFile)
	if err := writeCatalogue(path, &savedCatalogue{Version: catalogueVersion,
		FetchedAt: time.Now().Add(-40 * 24 * time.Hour), Entries: seadexFrierenEntry()}); err != nil {
		t.Fatal(err)
	}
	s, _, recorder := catalogueScout(dir, &fakeSeaDex{err: errors.New("seadex down")})
	s.Cycle(t.Context())
	if recorder.CountExact("better release available") != 0 {
		t.Error("a 40-day-old catalogue was compared against")
	}
	if _, err := s.RefreshLocal(t.Context()); err == nil || !strings.Contains(err.Error(), "older than") {
		t.Errorf("RefreshLocal = %v, want the age limit named", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the old catalogue was removed: %v", err)
	}
}

// TestSaveKeepsThePreviousCopy pins the rotation: a new save moves the old
// file aside rather than overwriting the only copy.
func TestSaveKeepsThePreviousCopy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, CatalogueFile)
	for range 2 {
		if err := writeCatalogue(path, &savedCatalogue{Version: catalogueVersion, FetchedAt: time.Now(), Entries: seadexFrierenEntry()}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := readCatalogue(filepath.Join(dir, catalogueFilePrev)); err != nil {
		t.Errorf("previous copy: %v", err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != catalogueFileMode {
		t.Errorf("saved copy: %v %v", info, err)
	}
}
