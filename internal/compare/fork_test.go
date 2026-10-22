package compare

import (
	"testing"

	"github.com/cplieger/seadex-scout/internal/library"
	"github.com/cplieger/seadex-scout/internal/mapping"
	"github.com/cplieger/seadex-scout/internal/match"
	"github.com/cplieger/seadex-scout/internal/seadex"
)

// extendedOne compares one season-1 match in Extended mode and returns its
// findings.
func extendedOne(t *testing.T, item *library.Item, entry seadex.Entry) []Finding {
	t.Helper()
	m := match.Match{Item: item, Arr: item.Arr, Entry: entry, Record: mapping.Record{SeasonTvdb: 1}}
	return New(Config{Extended: true}).Compare([]match.Match{m})
}

// heldSeries is sizedSeries holding group's files instead of erai-raws'.
func heldSeries(group string) *library.Item {
	item := sizedSeries()
	item.Groups = []string{group}
	item.SeasonGroups = map[int][]string{1: {group}}
	return item
}

func TestExtendedNotDownloaded(t *testing.T) {
	item := &library.Item{Title: "Show", Arr: library.ArrSonarr, ArrID: 7}
	entry := seadex.Entry{AniListID: 1, Torrents: []seadex.Torrent{pack(1, 12, 1), altPack("Judas", 12, 1, gib/2)}}

	got := extendedOne(t, item, entry)
	if len(got) != 1 || got[0].Status != StatusNotDownloaded {
		t.Fatalf("Compare = %+v, want one not_downloaded row", got)
	}
	f := got[0]
	if f.RecommendedGroup != "SubsPlease" || f.ReleaseBytes != 12*newBytes || f.CurrentBytes != 0 {
		t.Errorf("best = %s %d/%d, want SubsPlease %d/0", f.RecommendedGroup, f.ReleaseBytes, f.CurrentBytes, 12*newBytes)
	}
	if f.Alt.Group != "Judas" || f.Alt.ReleaseBytes != 6*gib {
		t.Errorf("Alt = %+v, want Judas at %d", f.Alt, 6*gib)
	}

	if got := New(Config{}).Compare([]match.Match{{Item: item, Arr: item.Arr, Entry: entry, Record: mapping.Record{SeasonTvdb: 1}}}); len(got) != 0 {
		t.Errorf("without Extended a no-file item is reported: %+v", got)
	}
}

func TestExtendedAtBestCarriesTheAlt(t *testing.T) {
	item := heldSeries("subsplease")
	entry := seadex.Entry{AniListID: 1, Torrents: []seadex.Torrent{pack(1, 12, 1), altPack("Judas", 12, 1, gib/2)}}

	got := extendedOne(t, item, entry)
	if len(got) != 1 || got[0].Status != StatusAtBest {
		t.Fatalf("Compare = %+v, want one at_best row", got)
	}
	f := got[0]
	if f.LibraryBytes != 12*heldBytes {
		t.Errorf("LibraryBytes = %d, want %d", f.LibraryBytes, 12*heldBytes)
	}
	if f.Alt.Group != "Judas" || f.Alt.ReleaseBytes != 6*gib || f.Alt.CurrentBytes != 12*heldBytes {
		t.Errorf("Alt = %+v, want Judas replacing the whole season", f.Alt)
	}
}

func TestExtendedNoBestShowsTheAlt(t *testing.T) {
	alt := altPack("Judas", 12, 1, gib/2)
	theoretical := seadex.Entry{AniListID: 1, TheoreticalBest: "Someone", Torrents: []seadex.Torrent{alt}}

	got := extendedOne(t, heldSeries("erai-raws"), theoretical)
	if len(got) != 1 || got[0].Status != StatusTheoretical || got[0].NoBestReason != noBestTheoretical {
		t.Fatalf("Compare = %+v, want one theoretical_best row", got)
	}
	if got[0].Alt.Group != "Judas" || got[0].Tier == TierAlt {
		t.Errorf("Alt = %+v tier %q, want Judas and not held", got[0].Alt, got[0].Tier)
	}

	got = extendedOne(t, heldSeries("judas"), seadex.Entry{AniListID: 1, Torrents: []seadex.Torrent{alt}})
	if len(got) != 1 || got[0].Status != StatusNoBest || got[0].Tier != TierAlt {
		t.Fatalf("Compare = %+v, want one no_best row held at the alt", got)
	}
}

func TestExtendedIncompleteEntryStaysAnUpgrade(t *testing.T) {
	entry := seadex.Entry{AniListID: 1, Incomplete: true, Torrents: []seadex.Torrent{pack(1, 12, 1)}}

	got := extendedOne(t, sizedSeries(), entry)
	if len(got) != 1 || got[0].Status != StatusBetter || !got[0].EntryIncomplete {
		t.Fatalf("Compare = %+v, want one better_release row flagged incomplete", got)
	}
	m := match.Match{Item: sizedSeries(), Arr: library.ArrSonarr, Entry: entry, Record: mapping.Record{SeasonTvdb: 1}}
	if got := New(Config{}).Compare([]match.Match{m}); len(got) != 1 || got[0].Status != StatusIncomplete {
		t.Errorf("without Extended = %+v, want upstream's incomplete row", got)
	}
}
