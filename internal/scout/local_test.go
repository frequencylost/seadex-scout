package scout

import (
	"errors"
	"testing"

	"github.com/cplieger/arrapi/v3/sonarr"
	"github.com/cplieger/seadex-scout/internal/anilist"
	"github.com/cplieger/seadex-scout/internal/arrwalk"
	"github.com/cplieger/seadex-scout/internal/compare"
	"github.com/cplieger/seadex-scout/internal/match"
	"github.com/cplieger/seadex-scout/internal/notify"
	"github.com/cplieger/seadex-scout/internal/state"
	"github.com/cplieger/slogx/capture"
)

// TestRefreshLocalUsesTheKeptCatalogue pins the local library refresh: it
// re-reads Sonarr and compares against the catalogue the last full check
// downloaded, without fetching from SeaDex, and an upgrade imported since
// then resolves.
func TestRefreshLocalUsesTheKeptCatalogue(t *testing.T) {
	logger, recorder := capture.New()
	store := &fakeStore{st: state.State{Mapping: frierenMappingCache()}}
	son := &fakeSonarr{
		series: []sonarr.Series{{ID: 7, Title: "Frieren", IDs: sonarr.ExternalIDs{TVDB: 123}, Year: 2023}},
		files:  map[int][]sonarr.EpisodeFile{7: {{Season: 1, ReleaseGroup: "Erai-raws"}}},
	}
	sea := &fakeSeaDex{entries: seadexFrierenEntry()}
	s := New(&Deps{
		Logger:       logger,
		Store:        store,
		Library:      arrwalk.NewWalker(&arrwalk.Config{Sonarr: onePass{son}, Logger: scoutTestLogger()}),
		Mapping:      fakeMapping{},
		SeaDex:       sea,
		Matcher:      match.New(notFoundAniList{}, scoutTestLogger()),
		Comparer:     compare.New(compare.Config{}),
		Notifier:     notify.NewNotifier(logger, nil),
		AniListStats: aniStatsFn(anilist.NewClient(noNetworkClient(), "http://unused.invalid/gql", anilist.WithRate(1), anilist.WithLogger(scoutTestLogger()))),
	})

	if _, err := s.RefreshLocal(t.Context()); !errors.Is(err, ErrNoCatalogue) {
		t.Fatalf("before any full check: err = %v, want ErrNoCatalogue", err)
	}
	if len(sea.opts) != 0 {
		t.Fatalf("a refresh with no catalogue fetched %d times", len(sea.opts))
	}

	if !s.Cycle(t.Context()) || recorder.CountExact("better release available") != 1 {
		t.Fatal("the first check did not report the Erai-raws upgrade")
	}

	// The SubsPlease release is imported, and SeaDex goes down: neither matters
	// to a local refresh, which reads nothing from SeaDex.
	son.files[7] = []sonarr.EpisodeFile{{Season: 1, ReleaseGroup: "SubsPlease"}}
	sea.err = errors.New("seadex down")
	healthy, err := s.RefreshLocal(t.Context())
	if err != nil || !healthy {
		t.Fatalf("RefreshLocal = %v, %v", healthy, err)
	}
	if len(sea.opts) != 1 {
		t.Errorf("SeaDex was fetched %d times, want only the first check's", len(sea.opts))
	}
	if recorder.CountExact("upgrade resolved") != 1 {
		t.Error("the imported upgrade did not resolve")
	}
	if recorder.CountExact("local library refresh complete") != 1 || recorder.CountExact("reconcile complete") != 1 ||
		recorder.CountExact("reconcile started") != 1 {
		t.Error("the refresh logged as a full check")
	}
	if store.st.SeadexFailures != 0 {
		t.Errorf("SeaDex failure streak = %d, want untouched", store.st.SeadexFailures)
	}
	if s.local.running {
		t.Error("still marked running")
	}
}
