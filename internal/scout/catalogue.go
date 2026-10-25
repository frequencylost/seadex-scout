package scout

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cplieger/seadex-scout/internal/seadex"
	"github.com/cplieger/seadex-scout/internal/seadexapi"
)

// This fork keeps a copy of every SeaDex catalogue a full check downloads, in
// /config, so the scout can keep comparing through a SeaDex outage and the
// data outlives SeaDex itself. A check that cannot reach SeaDex compares
// against the saved copy while it is younger than the limit; the copy is
// never deleted, only no longer used. The SeaDex outage is still recorded
// and alerted on as before: the saved copy only keeps the comparison going.

const (
	// CatalogueFile is the saved copy, and catalogueFilePrev the one before it,
	// kept so one bad download never leaves the only copy broken.
	CatalogueFile     = "seadex-catalogue.json.gz"
	catalogueFilePrev = "seadex-catalogue.prev.json.gz"
	// DefaultCatalogueMaxAge is how old a saved copy may be and still be used.
	DefaultCatalogueMaxAge = 30 * 24 * time.Hour
	catalogueFileMode      = 0o600
	catalogueVersion       = 1
)

// savedCatalogue is the file's content.
type savedCatalogue struct {
	Version   int            `json:"version"`
	FetchedAt time.Time      `json:"fetched_at"`
	Entries   []seadex.Entry `json:"entries"`
}

// errCatalogueTooOld means the saved copy is past the age limit.
var errCatalogueTooOld = errors.New("the saved SeaDex catalogue is older than the limit")

// catalogue is the reconcile's SeaDex read. A full check downloads it, keeps
// it in memory for local refreshes and saves it to disk. When the download
// fails, it falls back to the copy in memory or on disk while that is within
// the age limit. A local refresh always uses the kept copy.
func (s *Scout) catalogue(ctx context.Context) ([]seadex.Entry, error) {
	s.local.fallback, s.local.fallbackErr = false, nil
	if s.local.running {
		s.logCatalogue("local")
		return s.local.entries, nil
	}
	entries, err := s.seadex.FetchEntries(ctx, seadexapi.Options{Mode: seadexapi.FetchFull})
	if err == nil && len(entries) > 0 {
		s.local.entries, s.local.fetchedAt = entries, time.Now().UTC()
		s.saveCatalogue()
		s.logCatalogue("live")
		return entries, nil
	}
	if ctx.Err() != nil {
		return entries, err
	}
	switch loadErr := s.loadCatalogue(); {
	case loadErr == nil:
		s.local.fallback = true
		// The failed download is still recorded as a SeaDex failure (see
		// handlePreCompareGate); a zero-entry answer is not one, as upstream.
		s.local.fallbackErr = err
		s.log.Warn("seadex could not be read; comparing against the saved catalogue",
			"error", fmt.Sprint(cmpOr(err, errors.New("seadex returned zero entries"))),
			"fetched_at", s.local.fetchedAt, "age", time.Since(s.local.fetchedAt).Round(time.Minute).String())
		s.logCatalogue("saved")
		return s.local.entries, nil
	case errors.Is(loadErr, errCatalogueTooOld):
		s.log.Warn("seadex could not be read and the saved catalogue is too old to use; it is kept on disk",
			"fetched_at", s.local.fetchedAt, "max_age", s.local.maxAge.String())
		s.logCatalogue("expired")
	}
	return entries, err
}

func cmpOr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// loadCatalogue makes the kept copy available: the one in memory, else the
// saved file. It reports errCatalogueTooOld past the age limit, leaving the
// copy in place.
func (s *Scout) loadCatalogue() error {
	if len(s.local.entries) == 0 {
		if s.local.dir == "" {
			return errors.New("no saved catalogue")
		}
		saved, err := readCatalogue(filepath.Join(s.local.dir, CatalogueFile))
		if err != nil {
			return err
		}
		s.local.entries, s.local.fetchedAt = saved.Entries, saved.FetchedAt
	}
	if s.local.maxAge > 0 && time.Since(s.local.fetchedAt) > s.local.maxAge {
		return errCatalogueTooOld
	}
	return nil
}

// logCatalogue states which SeaDex data the comparison uses, for the
// dashboard: "live" (downloaded now), "local" (the kept copy, for a local
// refresh), "saved" (the kept copy, SeaDex unreachable) or "expired".
func (s *Scout) logCatalogue(source string) {
	s.log.Info("seadex catalogue in use", "source", source, "fetched_at", s.local.fetchedAt,
		"entries", len(s.local.entries), "max_age", s.local.maxAge.String())
}

// saveCatalogue writes the kept copy to disk, moving the previous one aside.
// A failure is logged and leaves the previous copy in place.
func (s *Scout) saveCatalogue() {
	if s.local.dir == "" {
		return
	}
	path := filepath.Join(s.local.dir, CatalogueFile)
	if err := writeCatalogue(path, &savedCatalogue{Version: catalogueVersion, FetchedAt: s.local.fetchedAt, Entries: s.local.entries}); err != nil {
		s.log.Warn("could not save the SeaDex catalogue to disk; the previous copy is kept", "error", err)
	}
}

func writeCatalogue(path string, c *savedCatalogue) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".seadex-catalogue-*.json.gz")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone after a successful rename
	if err := tmp.Chmod(catalogueFileMode); err != nil {
		tmp.Close() //nolint:errcheck,gosec // the chmod error is the one reported
		return err
	}
	zw := gzip.NewWriter(tmp)
	err = json.NewEncoder(zw).Encode(c)
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(path); statErr == nil {
		if err := os.Rename(path, filepath.Join(filepath.Dir(path), catalogueFilePrev)); err != nil {
			return err
		}
	}
	return os.Rename(tmp.Name(), path)
}

func readCatalogue(path string) (*savedCatalogue, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("the saved SeaDex catalogue is not readable: %w", err)
	}
	defer zr.Close()
	var c savedCatalogue
	if err := json.NewDecoder(zr).Decode(&c); err != nil {
		return nil, fmt.Errorf("the saved SeaDex catalogue is not readable: %w", err)
	}
	if c.Version != catalogueVersion || len(c.Entries) == 0 {
		return nil, errors.New("the saved SeaDex catalogue is empty or from another version")
	}
	return &c, nil
}
