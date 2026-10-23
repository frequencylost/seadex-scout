package webui

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLibraryRefresherRunsOneAtATime(t *testing.T) {
	store := NewStore("")
	release := make(chan struct{})
	r := NewLibraryRefresher(context.Background(), func(context.Context) error {
		<-release
		return errors.New("Sonarr could not be read")
	}, store)

	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	if !store.Snapshot().Refresh.Running {
		t.Error("not marked running")
	}
	if err := r.Start(); !errors.Is(err, ErrRefreshRunning) {
		t.Errorf("a second start: %v", err)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for store.Snapshot().Refresh.Running {
		if time.Now().After(deadline) {
			t.Fatal("the refresh did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if job := store.Snapshot().Refresh; job.Error != "Sonarr could not be read" || job.FinishedAt.IsZero() {
		t.Errorf("job = %+v", job)
	}
	if err := r.Start(); err != nil {
		t.Errorf("starting again after it finished: %v", err)
	}
}
