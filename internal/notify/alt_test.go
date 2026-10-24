package notify

import (
	"strings"
	"testing"

	"github.com/cplieger/seadex-scout/internal/compare"
)

// kv finds key's value in a key-value list.
func kv(kvs []any, key string) (any, bool) {
	for i := 0; i+1 < len(kvs); i += 2 {
		if kvs[i] == key {
			return kvs[i+1], true
		}
	}
	return nil, false
}

func TestForkKVsCarryAltTagsAndNotes(t *testing.T) {
	f := &compare.Finding{
		Status: compare.StatusAtBest,
		Notes:  "MrManager is 2022 Unrestored USA BD Remux\nMrManager is also 2022 Restored " + strings.Repeat("x", 3000),
		Alt:    compare.AltRelease{Group: "MrManager", Kind: "remux", Resolution: "1080p", DualAudio: true},
	}
	kvs := forkKVs(f)
	if v, _ := kv(kvs, "alt_seadex_tags"); v != "alt · remux · 1080p · dual-audio" {
		t.Errorf("alt_seadex_tags = %v", v)
	}
	v, ok := kv(kvs, "seadex_notes")
	notes, _ := v.(string)
	if !ok || !strings.HasPrefix(notes, "MrManager is 2022 Unrestored") || !strings.Contains(notes, "\n") || len(notes) > maxNotesBytes+16 {
		t.Errorf("seadex_notes = %q (%d bytes)", notes, len(notes))
	}

	if _, ok := kv(forkKVs(&compare.Finding{Status: compare.StatusAtBest}), "seadex_notes"); ok {
		t.Error("an entry without notes logs seadex_notes")
	}
}

func TestOptionsAttrOnlyWithMoreThanTheHeadline(t *testing.T) {
	one := []compare.ReleaseOption{{Group: "Okay-Subs", Headline: true}}
	if got := optionsAttr(one, "best"); got != "" {
		t.Errorf("headline only: %q, want nothing", got)
	}
	two := append(one, compare.ReleaseOption{Group: "Beatrice-Raws", Kind: "remux", Resolution: "1080p", DualAudio: true,
		Links: []compare.ReleaseLink{{Tracker: "Nyaa", URL: "https://nyaa.si/view/5"}}, ReleaseBytes: 10, CurrentBytes: 4})
	got := optionsAttr(two, "best")
	for _, want := range []string{`"group":"Beatrice-Raws"`, `"tags":"best · remux · 1080p · dual-audio"`,
		`"nyaa_url":"https://nyaa.si/view/5"`, `"bytes":10`, `"change":6`, `"headline":true`} {
		if !strings.Contains(got, want) {
			t.Errorf("optionsAttr = %s, lacks %s", got, want)
		}
	}
}
