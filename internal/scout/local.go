package scout

import (
	"context"
	"errors"

	"github.com/cplieger/seadex-scout/internal/seadex"
	"github.com/cplieger/seadex-scout/internal/seadexapi"
)

// This fork's local library refresh: re-read Sonarr and Radarr and compare
// them against the SeaDex catalogue the last full check downloaded, without
// fetching anything from SeaDex. It runs the reconcile with three differences:
// the catalogue comes from memory, the Torznab feed is not rebuilt, and the
// SeaDex fetch streak is left alone, since nothing was read from SeaDex.

// ErrNoCatalogue means no full check has finished since the container
// started, so there is no catalogue in memory to refresh against.
var ErrNoCatalogue = errors.New("no full check has finished since the container started, so there is no SeaDex catalogue in memory yet; wait for it to finish")

// localRefresh is the Scout's share of the local refresh: the last full
// catalogue, and whether the reconcile now running is a local one. Both are
// only touched by a reconcile, and reconciles never overlap (the cycle lock).
type localRefresh struct {
	entries []seadex.Entry
	running bool
}

// catalogue is the reconcile's SeaDex read: the full download, kept for later
// local refreshes, or in a local refresh the kept copy.
func (s *Scout) catalogue(ctx context.Context) ([]seadex.Entry, error) {
	if s.local.running {
		return s.local.entries, nil
	}
	entries, err := s.seadex.FetchEntries(ctx, seadexapi.Options{Mode: seadexapi.FetchFull})
	if err == nil && len(entries) > 0 {
		s.local.entries = entries
	}
	return entries, err
}

// RefreshLocal re-reads Sonarr and Radarr and compares them against the kept
// catalogue, reporting whether the library read was healthy. The caller must
// hold the cycle lock, as the scheduled checks do, so it never overlaps one.
func (s *Scout) RefreshLocal(ctx context.Context) (bool, error) {
	if len(s.local.entries) == 0 {
		return false, ErrNoCatalogue
	}
	s.local.running = true
	defer func() { s.local.running = false }()
	return s.reconcile(ctx), nil
}
