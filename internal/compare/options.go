package compare

import (
	"slices"

	"github.com/cplieger/seadex-scout/internal/align"
	"github.com/cplieger/seadex-scout/internal/match"
	"github.com/cplieger/seadex-scout/internal/release"
)

// maxReleaseOptions caps Finding.BestOptions and AltOptions: every option
// rides each finding line of every check.
const maxReleaseOptions = 6

// optionKey is what makes two torrents one release: the same group's same
// kind, resolution and dual audio. The same release on Nyaa and AnimeBytes
// is one option with both links.
type optionKey struct {
	group, kind, resolution string
	dualAudio               bool
}

func keyOf(c *candidate) optionKey {
	return optionKey{release.NormalizeGroup(c.rel.Group), string(c.rel.Kind), c.rel.Resolution, c.rel.DualAudio}
}

// releaseOptions splits pool into its distinct releases, each sized and linked
// as the headline best or alt is, ordered as representative ranks them, so
// the first is the headline the row already shows. headline marks the option
// that is.
func (c *Comparer) releaseOptions(m *match.Match, d *align.Decision, pool []candidate, headline optionKey) []ReleaseOption {
	byKey := map[optionKey][]candidate{}
	for i := range pool {
		k := keyOf(&pool[i])
		byKey[k] = append(byKey[k], pool[i])
	}
	type ranked struct {
		key optionKey
		rep candidate
	}
	order := make([]ranked, 0, len(byKey))
	for k, cands := range byKey {
		order = append(order, ranked{k, representative(cands)})
	}
	slices.SortFunc(order, func(a, b ranked) int {
		switch {
		case a.key == headline:
			return -1
		case b.key == headline:
			return 1
		case betterCandidate(&a.rep, &b.rep):
			return -1
		case betterCandidate(&b.rep, &a.rep):
			return 1
		}
		return 0
	})
	if len(order) > maxReleaseOptions {
		order = order[:maxReleaseOptions]
	}
	out := make([]ReleaseOption, 0, len(order))
	for _, o := range order {
		own := byKey[o.key]
		opt := ReleaseOption{
			Group: o.rep.rel.Group, Links: obtainableLinks(own, o.key.group),
			Kind: o.key.kind, Resolution: o.key.resolution, DualAudio: o.key.dualAudio,
			Headline: o.key == headline,
		}
		if set, ok := downloadSet(own); ok {
			if total, ok := downloadBytes(set); ok {
				opt.ReleaseBytes = total
				if current, ok := replacedBytes(m, d, set); ok {
					opt.CurrentBytes = current
				}
			}
		}
		out = append(out, opt)
	}
	return out
}
