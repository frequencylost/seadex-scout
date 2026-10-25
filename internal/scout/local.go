package scout

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cplieger/seadex-scout/internal/seadex"
)

// This fork's local library refresh: re-read Sonarr and Radarr and compare
// them against the SeaDex catalogue the last full check downloaded, without
// fetching anything from SeaDex. It runs the reconcile with three differences:
// the catalogue comes from memory, the Torznab feed is not rebuilt, and the
// SeaDex fetch streak is left alone, since nothing was read from SeaDex.

// ErrNoCatalogue means no full check has finished since the container
// started, so there is no catalogue in memory to refresh against.
var ErrNoCatalogue = errors.New("there is no SeaDex catalogue yet, in memory or saved; wait for the first full check to finish")

// localRefresh is the Scout's share of the local refresh: the last full
// catalogue, and whether the reconcile now running is a local one. Both are
// only touched by a reconcile, and reconciles never overlap (the cycle lock).
type localRefresh struct {
	entries   []seadex.Entry
	fetchedAt time.Time
	running   bool

	// dir and maxAge place and limit the saved copy (catalogue.go); fallback
	// marks a full check comparing against it, and fallbackErr is the
	// SeaDex failure that made it, still recorded as one.
	dir         string
	maxAge      time.Duration
	fallback    bool
	fallbackErr error
}

// RefreshLocal re-reads Sonarr and Radarr and compares them against the kept
// catalogue, reporting whether the library read was healthy. The caller must
// hold the cycle lock, as the scheduled checks do, so it never overlaps one.
func (s *Scout) RefreshLocal(ctx context.Context) (bool, error) {
	switch err := s.loadCatalogue(); {
	case errors.Is(err, errCatalogueTooOld):
		return false, fmt.Errorf("the saved SeaDex catalogue is from %s, older than the %s limit; it is kept on disk, but a full check needs SeaDex to refresh it",
			s.local.fetchedAt.Format("2 Jan 2006"), s.local.maxAge)
	case err != nil:
		return false, ErrNoCatalogue
	}
	s.local.running = true
	defer func() { s.local.running = false }()
	return s.reconcile(ctx), nil
}
