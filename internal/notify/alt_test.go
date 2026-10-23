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
