package compare

import (
	"github.com/cplieger/seadex-scout/internal/align"
	"github.com/cplieger/seadex-scout/internal/classify"
	"github.com/cplieger/seadex-scout/internal/filter"
	"github.com/cplieger/seadex-scout/internal/match"
	"github.com/cplieger/seadex-scout/internal/release"
	"github.com/cplieger/seadex-scout/internal/seadex"
	"github.com/cplieger/seadex-scout/internal/tagfilter"
)

// AltRelease is the headline alt release of an entry: the one alt group's
// release the dashboard shows beside SeaDex's best, sized and linked the way
// the best release is.
type AltRelease struct {
	Group string
	// Links are the obtainable links of Group's release only.
	Links []ReleaseLink
	// ReleaseBytes and CurrentBytes follow Finding's fields of the same names,
	// for the alt's download set, and stay zero when unknown.
	ReleaseBytes int64
	CurrentBytes int64
	// Kind, Resolution and DualAudio describe Group's headline release, as
	// Finding's fields of the same names describe the best one.
	Kind       string
	Resolution string
	DualAudio  bool
	// Headline marks, in Finding.BestOptions and AltOptions, the release the
	// row already shows (the best's own fields, or Alt).
	Headline bool
}

// ReleaseOption is one release in Finding.BestOptions or AltOptions.
type ReleaseOption = AltRelease

// alternatives is recommended's twin for the torrents SeaDex does not mark
// best, under the same tag, content and obtainability filters. It goes by
// SeaDex's per-torrent mark, not by group: one group can have both, such as
// MrManager's unrestored (best) and restored (alt) remuxes of entry 2461.
func (c *Comparer) alternatives(entry *seadex.Entry) []candidate {
	var out []candidate
	for i := range entry.Torrents {
		t := &entry.Torrents[i]
		if t.IsBest || c.tags.Excludes(t.Tags, tagfilter.SurfaceFindings) {
			continue
		}
		rel := classify.Torrent(entry, t)
		if !filter.KeepNonTracker(&rel, c.opts) {
			continue
		}
		if !classify.Obtainable(&rel, t, c.animeBytes) {
			continue
		}
		out = append(out, candidate{rel: rel, torrent: *t})
	}
	return out
}

// fillAlt sets f.Alt on an upgrade from the entry's headline alt release,
// picked as the best release is (representative), with its size and the size
// change it brings worked out as sized does for the best one.
func (c *Comparer) fillAlt(m *match.Match, d *align.Decision, f *Finding) *Finding {
	if f == nil || (f.Status != StatusBetter && f.Status != StatusNewerRevision) {
		return f
	}
	return c.addAlt(m, d, f)
}

// addAlt is fillAlt without its upgrade-only gate, for Extended mode's rows.
func (c *Comparer) addAlt(m *match.Match, d *align.Decision, f *Finding) *Finding {
	pool := c.alternatives(&m.Entry)
	if len(pool) == 0 {
		return f
	}
	rep := representative(pool)
	group := release.NormalizeGroup(rep.rel.Group)
	own := make([]candidate, 0, len(pool))
	for i := range pool {
		if release.NormalizeGroup(pool[i].rel.Group) == group {
			own = append(own, pool[i])
		}
	}
	f.Alt = AltRelease{Group: rep.rel.Group, Links: obtainableLinks(own, group),
		Kind: string(rep.rel.Kind), Resolution: rep.rel.Resolution, DualAudio: rep.rel.DualAudio}
	if set, ok := downloadSet(own); ok {
		if total, ok := downloadBytes(set); ok {
			f.Alt.ReleaseBytes = total
			if current, ok := replacedBytes(m, d, set); ok {
				f.Alt.CurrentBytes = current
			}
		}
	}
	return f
}
