package webui

import (
	"context"
	"errors"
	"sync"
	"time"
)

// RefreshJob is the state of the local library refresh the dashboard started.
type RefreshJob struct {
	Running    bool      `json:"running"`
	StartedAt  time.Time `json:"started_at,omitzero"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
	Error      string    `json:"error,omitempty"`
}

// ErrRefreshRunning is returned by Start while a refresh is still going.
var ErrRefreshRunning = errors.New("a library refresh is already running")

// LibraryRefresher runs the scout's local library refresh in the background,
// one at a time, recording its progress for the page.
type LibraryRefresher struct {
	ctx   context.Context //nolint:containedctx // the daemon's lifetime bounds every run
	run   func(context.Context) error
	store *Store

	mu      sync.Mutex
	running bool
}

// NewLibraryRefresher returns a refresher calling run, which re-reads Sonarr
// and Radarr and compares them against the kept catalogue.
func NewLibraryRefresher(ctx context.Context, run func(context.Context) error, store *Store) *LibraryRefresher {
	return &LibraryRefresher{ctx: ctx, run: run, store: store}
}

// Start begins a refresh and returns at once.
func (r *LibraryRefresher) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return ErrRefreshRunning
	}
	r.running = true
	r.store.setRefresh(func(j *RefreshJob) { *j = RefreshJob{Running: true, StartedAt: time.Now().UTC()} })
	go func() {
		err := r.run(r.ctx)
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
		r.store.setRefresh(func(j *RefreshJob) {
			j.Running = false
			j.FinishedAt = time.Now().UTC()
			if err != nil {
				j.Error = err.Error()
			}
		})
	}()
	return nil
}

func (st *Store) setRefresh(update func(*RefreshJob)) {
	st.mu.Lock()
	update(&st.refresh)
	st.mu.Unlock()
}
