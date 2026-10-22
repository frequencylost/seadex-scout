package compare

import (
	"slices"

	"github.com/cplieger/seadex-scout/internal/align"
	"github.com/cplieger/seadex-scout/internal/classify"
	"github.com/cplieger/seadex-scout/internal/library"
	"github.com/cplieger/seadex-scout/internal/match"
	"github.com/cplieger/seadex-scout/internal/release"
	"github.com/cplieger/seadex-scout/internal/seadex"
)

// This file holds the fork's Extended mode (Config.Extended), which reports
// every entry the built-in dashboard lists rather than only the exceptions
// upstream alerts on. Without Extended, Compare behaves exactly as upstream,
// apart from the headline alt on upgrades (alt.go).

// No-best reasons for Finding.NoBestReason.
const (
	noBestTheoretical  = "theoretical"
	noBestIncomplete   = "incomplete"
	noBestUnobtainable = "unobtainable"
)

// extendedRow reports an outcome upstream stays silent on: no file on disk
// (StatusNotDownloaded) or already at SeaDex's best (StatusAtBest). A withheld
// comparison, and a no-file item whose walk failed, stay silent.
func (c *Comparer) extendedRow(m *match.Match, d *align.Decision, recommended []candidate, recGroups []string) *Finding {
	if !c.extended || d.Outcome == align.OutcomeWithheld {
		return nil
	}
	if d.Outcome == align.OutcomeNoFile && (m.Item == nil || m.Item.Failed) {
		return nil
	}
	base := baseFinding(m, d)
	_, base.AltGroups = c.listedGroups(&m.Entry)
	base.EntryIncomplete = m.Entry.Incomplete
	if len(recommended) > 0 {
		fillBest(&base, recommended, recGroups)
	} else {
		base.NoBestReason = noBestReason(&m.Entry)
	}
	status := StatusAtBest
	if d.Outcome == align.OutcomeNoFile {
		status = StatusNotDownloaded
	}
	return c.enrich(m, d, recommended, finalize(&base, status))
}

// noBestRow replaces emptyResult in Extended mode: an entry with no best the
// operator can obtain is still reported, so its alt shows, and it is marked as
// held at an alt when every group on disk is one SeaDex lists as an alt.
func noBestRow(entry *seadex.Entry, base *Finding, d *align.Decision) *Finding {
	base.EntryIncomplete = entry.Incomplete
	base.NoBestReason = noBestReason(entry)
	status := StatusNoBest
	switch base.NoBestReason {
	case noBestTheoretical:
		status = StatusTheoretical
	case noBestIncomplete:
		status = StatusIncomplete
	}
	if heldAlt(d.Groups, base.AltGroups) {
		base.Tier = TierAlt
	}
	return finalize(base, status)
}

func noBestReason(entry *seadex.Entry) string {
	switch classify.Fallback(entry) {
	case classify.FallbackTheoretical:
		return noBestTheoretical
	case classify.FallbackIncomplete:
		return noBestIncomplete
	default:
		return noBestUnobtainable
	}
}

// heldAlt reports whether the unit holds files and every group of them is
// one of the entry's alt groups.
func heldAlt(held, alt []string) bool {
	if len(held) == 0 || len(alt) == 0 {
		return false
	}
	for _, g := range held {
		if !slices.Contains(alt, release.NormalizeGroup(g)) {
			return false
		}
	}
	return true
}

// keepUpgrade undoes upstream's downgrade of an upgrade to StatusIncomplete
// for an incomplete entry in Extended mode: the best release is still the one
// to get, so the row stays an upgrade and EntryIncomplete carries the flag.
func (c *Comparer) keepUpgrade(entry *seadex.Entry, f *Finding, upgrade Status) *Finding {
	if !c.extended || f == nil {
		return f
	}
	f.EntryIncomplete = entry.Incomplete
	if f.Status == StatusIncomplete && entry.Incomplete {
		f.Status = upgrade
	}
	return f
}

// enrich adds what the dashboard shows beside a row. Without Extended it is
// fillAlt alone. In Extended mode it adds the size on disk, the best release's
// sizes when pool is given and they are not set yet, and the headline alt on
// every row.
func (c *Comparer) enrich(m *match.Match, d *align.Decision, pool []candidate, f *Finding) *Finding {
	if f == nil {
		return nil
	}
	if !c.extended {
		return c.fillAlt(m, d, f)
	}
	f.LibraryBytes = libraryBytes(m.Item, d)
	if f.ReleaseBytes == 0 && len(pool) > 0 {
		if set, ok := downloadSet(pool); ok {
			if total, ok := downloadBytes(set); ok {
				f.ReleaseBytes = total
				if current, ok := replacedBytes(m, d, set); ok {
					f.CurrentBytes = current
				}
			}
		}
	}
	return c.addAlt(m, d, f)
}

// libraryBytes is the size of the files the decided unit holds: a movie's
// file, a season's files, the files of the episodes an episodes scope names,
// or every file of a whole series. It is zero when nothing is held or any
// size is unknown.
func libraryBytes(item *library.Item, d *align.Decision) int64 {
	if item == nil {
		return 0
	}
	files := map[int]bool{}
	switch d.Kind {
	case align.ScopeMovie, align.ScopeWholeSeries:
		for id := range item.FileBytes {
			files[id] = true
		}
	case align.ScopeSeason:
		for _, ep := range item.SeasonEpisodes[d.Season] {
			files[ep.File] = true
		}
	case align.ScopeEpisodes:
		for _, e := range d.Episodes {
			if ep, ok := item.SeasonEpisodes[d.Season][e]; ok {
				files[ep.File] = true
			}
		}
	default:
		return 0
	}
	var total int64
	for id := range files {
		size := item.FileBytes[id]
		if id <= 0 || size <= 0 {
			return 0
		}
		sum, ok := addBytes(total, size)
		if !ok {
			return 0
		}
		total = sum
	}
	return total
}
