package webui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ReportJob is the state of the report run the dashboard started, if any.
type ReportJob struct {
	Running    bool      `json:"running"`
	StartedAt  time.Time `json:"started_at,omitzero"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
	// Result is the last notable line the run logged, such as "report written".
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// ErrReportRunning is returned by Start while a run is still going.
var ErrReportRunning = errors.New("a report is already running")

// reportWaitDelay is how long a cancelled report run gets to write its files
// after SIGTERM before it is killed.
const reportWaitDelay = 30 * time.Second

// ReportRunner starts `seadex-scout report` as a child process, exactly as
// `docker exec seadex-scout /seadex-scout report` would. The child's log lines
// are copied to the container's stdout, so Loki still sees them if you run it.
type ReportRunner struct {
	ctx   context.Context //nolint:containedctx // the daemon's lifetime bounds every run
	exe   string
	store *Store
	out   io.Writer

	mu      sync.Mutex
	running bool
}

// NewReportRunner returns a runner whose children stop when ctx ends. exe is
// the seadex-scout binary, normally os.Executable().
func NewReportRunner(ctx context.Context, exe string, store *Store) *ReportRunner {
	return &ReportRunner{ctx: ctx, exe: exe, store: store, out: os.Stdout}
}

// Start launches a report run and returns at once.
func (rr *ReportRunner) Start() error {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	if rr.running {
		return ErrReportRunning
	}
	cmd := exec.CommandContext(rr.ctx, rr.exe, "report")
	cmd.Env = os.Environ()
	out := &lockedWriter{w: rr.out}
	cmd.Stderr = out
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = reportWaitDelay
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	rr.running = true
	rr.store.setJob(func(j *ReportJob) {
		*j = ReportJob{Running: true, StartedAt: time.Now().UTC()}
	})
	go rr.follow(cmd, stdout, out)
	return nil
}

// follow copies the child's log to stdout, notes its notable lines, and
// records how it ended.
func (rr *ReportRunner) follow(cmd *exec.Cmd, stdout io.Reader, out io.Writer) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		raw := sc.Bytes()
		// A copy: appending to raw could overwrite the scanner's buffered input.
		line := make([]byte, 0, len(raw)+1)
		out.Write(append(append(line, raw...), '\n')) //nolint:errcheck,gosec // stdout loss is not actionable here
		var rec struct {
			Level string `json:"level"`
			Msg   string `json:"msg"`
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &rec) != nil {
			continue
		}
		if notable(rec.Level, rec.Msg) {
			result := rec.Msg
			if rec.Error != "" {
				result += ": " + rec.Error
			}
			rr.store.setJob(func(j *ReportJob) { j.Result = result })
		}
	}
	err := cmd.Wait()
	rr.mu.Lock()
	rr.running = false
	rr.mu.Unlock()
	var text string
	rr.store.setJob(func(j *ReportJob) {
		j.Running = false
		j.FinishedAt = time.Now().UTC()
		if err != nil {
			j.Error = err.Error()
		}
		text = j.Result
		if j.Error != "" {
			text = "It failed: " + j.Error
		}
	})
	rr.store.mu.Lock()
	rr.store.emit(Event{Kind: EventReports, Text: cmpOr(text, "The report run ended.")})
	rr.store.mu.Unlock()
	rr.store.markDirty()
}

// lockedWriter serializes the child's stdout and stderr copies, which run on
// different goroutines, onto one writer.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func notable(level, msg string) bool {
	switch {
	case strings.EqualFold(level, "error"), strings.EqualFold(level, "warn"):
		return msg != "better release available"
	default:
		return msg == "report written"
	}
}

// reportName matches the files audit.WriteFiles writes, and nothing else, so a
// request can never name a path outside the report folder.
var reportName = regexp.MustCompile(`^report-[0-9A-Za-z._-]+\.(json|md)$`)

// ReportFile is one report pair in the report folder.
type ReportFile struct {
	Stem     string    `json:"stem"`
	Modified time.Time `json:"modified"`
	JSON     bool      `json:"json"`
	Markdown bool      `json:"markdown"`
	Size     int64     `json:"size"`
}

// ListReports returns the report pairs in dir, newest first.
func ListReports(dir string) ([]ReportFile, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []ReportFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	byStem := map[string]*ReportFile{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !reportName.MatchString(name) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		ext := filepath.Ext(name)
		stem := strings.TrimSuffix(name, ext)
		rf := byStem[stem]
		if rf == nil {
			rf = &ReportFile{Stem: stem}
			byStem[stem] = rf
		}
		if info.ModTime().After(rf.Modified) {
			rf.Modified = info.ModTime().UTC()
		}
		rf.Size += info.Size()
		switch ext {
		case ".json":
			rf.JSON = true
		case ".md":
			rf.Markdown = true
		}
	}
	out := make([]ReportFile, 0, len(byStem))
	for _, rf := range byStem {
		out = append(out, *rf)
	}
	slices.SortFunc(out, func(a, b ReportFile) int { return strings.Compare(b.Stem, a.Stem) })
	return out, nil
}

// OpenReport opens one report file by name, refusing any name that is not a
// report file directly inside dir.
func OpenReport(dir, name string) (*os.File, error) {
	if !reportName.MatchString(name) {
		return nil, os.ErrNotExist
	}
	return os.Open(filepath.Join(dir, name))
}
