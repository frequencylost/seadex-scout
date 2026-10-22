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
}

// alternatives is recommended's twin for the non-best torrents of the groups
// the entry lists only as alts, under the same tag, content and
// obtainability filters.
func (c *Comparer) alternatives(entry *seadex.Entry) []candidate {
	best := map[string]bool{}
	for i := range entry.Torrents {
		if entry.Torrents[i].IsBest {
			best[release.NormalizeGroup(classify.Torrent(entry, &entry.Torrents[i]).Group)] = true
		}
	}
	var out []candidate
	for i := range entry.Torrents {
		t := &entry.Torrents[i]
		if t.IsBest || c.tags.Excludes(t.Tags, tagfilter.SurfaceFindings) {
			continue
		}
		rel := classify.Torrent(entry, t)
		if best[release.NormalizeGroup(rel.Group)] || !filter.KeepNonTracker(&rel, c.opts) {
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
	f.Alt = AltRelease{Group: rep.rel.Group, Links: obtainableLinks(own, group)}
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
