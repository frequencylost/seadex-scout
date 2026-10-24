package webui

import (
	"strconv"
	"time"
)

// eventBuffer is how many events can wait for the Notifier; past it, events
// are dropped rather than ever blocking the logger.
const eventBuffer = 256

// enableEvents turns on event collection and returns the channel to read.
func (st *Store) enableEvents() <-chan Event {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.events == nil {
		st.events = make(chan Event, eventBuffer)
	}
	return st.events
}

// emit queues ev without blocking; it is called with st.mu held.
func (st *Store) emit(ev Event) {
	if st.events == nil {
		return
	}
	select {
	case st.events <- ev:
	default:
	}
}

// closeFound turns the upgrades found in pass into one upgrades event, with
// the pass's full finding line for each where there is one. Called with st.mu
// held, after the pass's findings were committed.
func (st *Store) closeFound(pass int64) {
	found := st.found[pass]
	for id := range st.found {
		if id <= pass {
			delete(st.found, id)
		}
	}
	if len(found) == 0 {
		return
	}
	byKey := map[string]Line{}
	for _, f := range st.s.Findings {
		if f.Msg == "better release available" {
			byKey[upgradeKey(&f)] = f
		}
	}
	lines := make([]Line, 0, len(found))
	for i := range found {
		if f, ok := byKey[upgradeKey(&found[i])]; ok {
			lines = append(lines, f)
		} else {
			lines = append(lines, found[i])
		}
	}
	st.emit(Event{Kind: EventUpgrades, Lines: lines})
}

func upgradeKey(l *Line) string {
	return strconv.FormatInt(l.Int("al_id"), 10) + "|" + l.Str("arr") + "|" +
		strconv.FormatInt(l.Int("season"), 10) + "|" + l.Str("recommended_group")
}

// sweep finds the problems whose end, and the stalls whose start or end,
// no log line marks: a lasting problem ends when a check goes by without it,
// and checks stall when none finishes within the health check's allowance.
func (st *Store) sweep(now time.Time) []Event {
	st.mu.Lock()
	defer st.mu.Unlock()
	interval := st.pollInterval()
	var events []Event
	for key := range st.s.Notified {
		if key == "stall" {
			continue
		}
		line, ok := st.s.Conditions[key]
		if !ok || now.Sub(line.Time) > max(time.Hour, interval*3/2) {
			delete(st.s.Notified, key)
			events = append(events, Event{Kind: EventProblems, Key: key, Resolved: true})
			st.dirty = true
		}
	}
	if interval < 0 {
		return events // checks are started from outside; nothing can stall here
	}
	last := st.started
	if st.s.LastCheck != nil && st.s.LastCheck.Time.After(last) {
		last = st.s.LastCheck.Time
	}
	if st.s.ReconcileStarted.After(last) {
		last = st.s.ReconcileStarted
	}
	allowance := max(3*time.Hour, 3*interval)
	_, told := st.s.Notified["stall"]
	switch stalled := now.Sub(last) > allowance; {
	case stalled && !told:
		st.s.Notified["stall"] = now
		events = append(events, Event{Kind: EventProblems, Key: "stall",
			Text: "No check has finished or started in " + allowance.String() + ". Restart the container, then look at its log."})
		st.dirty = true
	case !stalled && told:
		delete(st.s.Notified, "stall")
		events = append(events, Event{Kind: EventProblems, Key: "stall", Resolved: true, Text: "Checks are running again."})
		st.dirty = true
	}
	return events
}

// pollInterval is the configured check interval, -1 when checks are started
// from outside, or 0 when the configuration has not been logged yet.
func (st *Store) pollInterval() time.Duration {
	if st.config == nil {
		return 0
	}
	raw := st.config.Str("poll_interval")
	if raw == "external" {
		return -1
	}
	d, _ := time.ParseDuration(raw)
	return d
}

// unmonitoredTest reports whether a finding's series, season or movie is
// unmonitored, as the page does.
func (st *Store) unmonitoredTest() func(*Line) bool {
	st.mu.Lock()
	keys := make(map[string]bool, len(st.monitored.Unmonitored))
	for _, k := range st.monitored.Unmonitored {
		keys[k] = true
	}
	st.mu.Unlock()
	return func(l *Line) bool {
		id := l.Int("arr_id")
		if id <= 0 {
			return false
		}
		base := l.Str("arr") + ":" + strconv.FormatInt(id, 10)
		if keys[base] {
			return true
		}
		// A season's own flag counts only for a row that is that season, or
		// episodes of it: a whole-series row carries season 0, which would
		// read the Specials flag, unmonitored on most shows.
		scope := l.Str("scope")
		seasonRow := scope == "season" || scope == "episodes"
		return l.Str("arr") == "sonarr" && seasonRow && l.Attrs["season"] != nil &&
			keys[base+":"+strconv.FormatInt(l.Int("season"), 10)]
	}
}

func (st *Store) setNotify(update func(*NotifyStatus)) {
	st.mu.Lock()
	update(&st.notify)
	st.mu.Unlock()
}
