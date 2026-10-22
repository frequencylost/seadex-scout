package notify

import "github.com/cplieger/seadex-scout/internal/compare"

// altKVs renders an upgrade's headline alt release for the built-in
// dashboard: its group, its links split the way the best release's are, and
// its sizes, each only when known. Nothing is rendered when the entry lists
// no obtainable alt. These attributes are diagnostic, outside the log
// contract the alert rules read.
func altKVs(f *compare.Finding) []any {
	a := &f.Alt
	if a.Group == "" {
		return nil
	}
	publicLink, abLink := trackerURLs(a.Links)
	kvs := []any{
		"alt_group", capAttr(a.Group),
		"alt_nyaa_url", capURLAttr(publicLink.nyaaURL()),
		"alt_public_url", capURLAttr(publicLink.otherURL()),
		"alt_public_tracker", capAlertTextAttr(publicLink.otherTracker()),
		"alt_ab_url", capURLAttr(abLink.url),
		"alt_ab_tracker", capAlertTextAttr(abLink.abTracker()),
	}
	if a.ReleaseBytes > 0 {
		kvs = append(kvs, "alt_recommended_bytes", a.ReleaseBytes)
		if a.CurrentBytes > 0 {
			kvs = append(kvs, "alt_size_change_bytes", a.ReleaseBytes-a.CurrentBytes)
		}
	}
	return kvs
}

// boundRetainedAlt is boundRetained for the alt release's untrusted strings.
func boundRetainedAlt(f *compare.Finding) {
	f.Alt.Group = capAttr(f.Alt.Group)
	f.Alt.Links = capRetainedList(f.Alt.Links)
	for i := range f.Alt.Links {
		f.Alt.Links[i].Tracker = capRetainedElem(f.Alt.Links[i].Tracker)
		f.Alt.Links[i].URL = capRetainedElem(f.Alt.Links[i].URL)
	}
}
