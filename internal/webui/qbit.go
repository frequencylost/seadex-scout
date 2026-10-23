package webui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// QbitConfig configures sending releases to qBittorrent.
type QbitConfig struct {
	// URL is the Web UI address, such as http://192.168.1.10:8080.
	URL                string
	Username, Password string
	// Categories maps "sonarr" and "radarr" to the category a release of each
	// is added under; an empty category adds it with none.
	Categories map[string]string
}

// QbitStatus is what the page needs to know: whether the buttons show, and
// the category each app's releases go to. It never carries a credential.
type QbitStatus struct {
	Enabled    bool              `json:"enabled"`
	Categories map[string]string `json:"categories,omitempty"`
}

// Qbit adds torrents through qBittorrent's Web API (v2), signing in when the
// session is missing or expired.
type Qbit struct {
	cfg    QbitConfig
	base   string
	client *http.Client

	mu sync.Mutex // serializes sign-in and add, so a retry sees one session
}

// NewQbit checks cfg and returns a client.
func NewQbit(cfg QbitConfig) (*Qbit, error) {
	u, err := url.Parse(strings.TrimSpace(cfg.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("the qBittorrent URL must be the http or https address of its Web UI, such as http://192.168.1.10:8080")
	}
	jar, _ := cookiejar.New(nil)
	return &Qbit{
		cfg:    cfg,
		base:   strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/"),
		client: &http.Client{Timeout: 30 * time.Second, Jar: jar},
	}, nil
}

// Status is the page's view of the configuration.
func (q *Qbit) Status() QbitStatus {
	return QbitStatus{Enabled: true, Categories: q.cfg.Categories}
}

// nyaaView is the only release page the button accepts. The torrent qBittorrent
// fetches is built from its number, so the page can never make the container
// or qBittorrent fetch any other address.
var nyaaView = regexp.MustCompile(`^https://nyaa\.si/view/(\d{1,12})/?$`)

// NyaaTorrentURL turns a Nyaa release page into its .torrent download.
func NyaaTorrentURL(view string) (string, bool) {
	m := nyaaView.FindStringSubmatch(strings.TrimSpace(view))
	if m == nil {
		return "", false
	}
	return "https://nyaa.si/download/" + m[1] + ".torrent", true
}

// errQbitAuth means qBittorrent wants a sign-in, or refused the one given.
var errQbitAuth = errors.New("qBittorrent refused the sign-in: check the user name and password, or allow the container's address in qBittorrent's Web UI settings")

// AddNyaa adds the release at a Nyaa page under arr's category.
func (q *Qbit) AddNyaa(ctx context.Context, view, arr string) (string, error) {
	torrent, ok := NyaaTorrentURL(view)
	if !ok {
		return "", errors.New("only nyaa.si release pages can be sent")
	}
	if arr != "sonarr" && arr != "radarr" {
		return "", errors.New("unknown app")
	}
	category := strings.TrimSpace(q.cfg.Categories[arr])

	q.mu.Lock()
	defer q.mu.Unlock()
	err := q.add(ctx, torrent, category)
	if errors.Is(err, errQbitAuth) && q.cfg.Username != "" {
		if err = q.login(ctx); err == nil {
			err = q.add(ctx, torrent, category)
		}
	}
	return category, err
}

func (q *Qbit) add(ctx context.Context, torrent, category string) error {
	if category != "" {
		// Ensure the category exists: qBittorrent answers 409 when it already
		// does, which is fine, and adding under a missing one fails.
		status, _, err := q.post(ctx, "/api/v2/torrents/createCategory", url.Values{"category": {category}})
		if err != nil {
			return err
		}
		if status == http.StatusForbidden {
			return errQbitAuth
		}
	}
	form := url.Values{"urls": {torrent}}
	if category != "" {
		form.Set("category", category)
	}
	status, body, err := q.post(ctx, "/api/v2/torrents/add", form)
	switch {
	case err != nil:
		return err
	case status == http.StatusForbidden:
		return errQbitAuth
	case status != http.StatusOK:
		return fmt.Errorf("qBittorrent answered %d: %s", status, body)
	case strings.HasPrefix(body, "Fails"):
		return errors.New("qBittorrent could not add the torrent; it may already be in the list")
	}
	return nil
}

func (q *Qbit) login(ctx context.Context) error {
	status, body, err := q.post(ctx, "/api/v2/auth/login", url.Values{"username": {q.cfg.Username}, "password": {q.cfg.Password}})
	switch {
	case err != nil:
		return err
	case status == http.StatusForbidden:
		return errors.New("qBittorrent has banned this address after too many failed sign-ins; wait, or restart qBittorrent")
	case status != http.StatusOK || !strings.HasPrefix(body, "Ok"):
		return errQbitAuth
	}
	return nil
}

// post sends a form and returns the status and a short body. The Referer and
// Origin name qBittorrent itself, which its CSRF check requires.
func (q *Qbit) post(ctx context.Context, path string, form url.Values) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.base+path, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, "", errors.New("the qBittorrent URL is not valid")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", q.base+"/")
	req.Header.Set("Origin", q.base)
	res, err := q.client.Do(req)
	if err != nil {
		var uerr interface{ Unwrap() error }
		if errors.As(err, &uerr) && uerr.Unwrap() != nil {
			err = uerr.Unwrap() // drops the URL the client wraps the cause in
		}
		return 0, "", fmt.Errorf("could not reach qBittorrent: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
	return res.StatusCode, strings.TrimSpace(string(body)), nil
}
