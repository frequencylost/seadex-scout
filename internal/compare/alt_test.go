package compare

import (
	"fmt"
	"testing"

	"github.com/cplieger/seadex-scout/internal/mapping"
	"github.com/cplieger/seadex-scout/internal/seadex"
)

// altPack is one non-best torrent of group holding S01E01..S01E<last>, each
// file size bytes.
func altPack(group string, last, n int, size int64) seadex.Torrent {
	t := seadex.Torrent{ReleaseGroup: group, Tracker: "Nyaa", URL: fmt.Sprintf("https://nyaa.si/view/alt%d", n), InfoHash: hash(900 + n)}
	for e := 1; e <= last; e++ {
		t.Files = append(t.Files, seadex.File{Name: fmt.Sprintf("Show S01/[%s] Show - S01E%02d (1080p x265).mkv", group, e), Length: size})
	}
	return t
}

// TestAltReleaseIsSizedAndLinked pins Finding.Alt on an upgrade: the headline
// alt group, its own Nyaa link only, and its download and replaced sizes
// worked out as the best release's are.
func TestAltReleaseIsSizedAndLinked(t *testing.T) {
	item := sizedSeries()
	torrents := []seadex.Torrent{pack(1, 12, 1), altPack("Judas", 12, 1, gib/2), altPack("Vodes", 6, 2, gib/4)}
	f := sizeOne(t, item, mapping.Record{SeasonTvdb: 1}, torrents)

	if f.Alt.Group == "" {
		t.Fatal("Alt.Group is empty, want the headline alt")
	}
	var want seadex.Torrent
	for _, tor := range torrents[1:] {
		if tor.ReleaseGroup == f.Alt.Group {
			want = tor
		}
	}
	if len(f.Alt.Links) != 1 || f.Alt.Links[0].URL != want.URL {
		t.Errorf("Alt.Links = %+v, want only %s", f.Alt.Links, want.URL)
	}
	var wantBytes int64
	for _, file := range want.Files {
		wantBytes += file.Length
	}
	if f.Alt.ReleaseBytes != wantBytes {
		t.Errorf("Alt.ReleaseBytes = %d, want %d", f.Alt.ReleaseBytes, wantBytes)
	}
	if wantCurrent := bytesOf(item, files(1, len(want.Files))...); f.Alt.CurrentBytes != wantCurrent {
		t.Errorf("Alt.CurrentBytes = %d, want %d", f.Alt.CurrentBytes, wantCurrent)
	}
	// The best release's own fields are unchanged by the alt.
	if f.ReleaseBytes != 12*newBytes || f.RecommendedGroup != "SubsPlease" {
		t.Errorf("best release = %s, %d bytes, want SubsPlease, %d", f.RecommendedGroup, f.ReleaseBytes, 12*newBytes)
	}
}

// TestAltReleaseAbsent pins an empty Finding.Alt when the entry has no alt the
// operator can obtain: none listed, or only one the filters drop.
func TestAltReleaseAbsent(t *testing.T) {
	season1 := mapping.Record{SeasonTvdb: 1}
	if f := sizeOne(t, sizedSeries(), season1, []seadex.Torrent{pack(1, 12, 1)}); f.Alt.Group != "" {
		t.Errorf("no alt listed: Alt = %+v, want zero", f.Alt)
	}
	ab := seadex.Torrent{ReleaseGroup: "PMR", Tracker: "AB", URL: "/torrents.php?id=1&torrentid=2"}
	if f := sizeOne(t, sizedSeries(), season1, []seadex.Torrent{pack(1, 12, 1), ab}); f.Alt.Group != "" {
		t.Errorf("AnimeBytes alt with the toggle off: Alt = %+v, want zero", f.Alt)
	}
}
