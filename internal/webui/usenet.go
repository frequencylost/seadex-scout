package webui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// UsenetConfig configures the → Prowlarr button: Prowlarr searches usenet
// for a Nyaa release's title, and a picked result goes straight to SABnzbd.
type UsenetConfig struct {
	ProwlarrURL, ProwlarrKey string
	SabURL, SabKey           string
	// Categories maps "sonarr" and "radarr" to a SABnzbd category; an empty
	// one adds with SABnzbd's default.
	Categories map[string]string
}

// UsenetStatus is what the page needs. It never carries a key.
type UsenetStatus struct {
	Enabled    bool              `json:"enabled"`
	Categories map[string]string `json:"categories,omitempty"`
}

// UsenetResult is one Prowlarr search result as the page shows it. The
// download link stays on the server: the page picks a result by ID.
type UsenetResult struct {
	ID      int       `json:"id"`
	Title   string    `json:"title"`
	Indexer string    `json:"indexer"`
	Size    int64     `json:"size"`
	Grabs   int       `json:"grabs"`
	Posted  time.Time `json:"posted,omitzero"`
	Exact   bool      `json:"exact"`
}

// UsenetSearch is a search's answer: the Nyaa title searched, its usenet
// results, and the token that picks one of them.
type UsenetSearch struct {
	Query   string         `json:"query"`
	Token   string         `json:"token"`
	Results []UsenetResult `json:"results"`
	// Other counts the results dropped as not usenet (torrents).
	Other int `json:"other"`
}

const (
	maxUsenetResults = 40
	searchKeep       = 30 * time.Minute // how long a search's results can be sent
	maxSearchesKept  = 50
	maxNyaaPageBytes = 2 << 20
	maxTitleCache    = 500
)

// Usenet runs the Nyaa → Prowlarr → SABnzbd flow.
type Usenet struct {
	cfg    UsenetConfig
	client *http.Client
	nyaa   string // Nyaa's address, swapped in tests
	now    func() time.Time

	mu       sync.Mutex
	titles   map[string]string // Nyaa view ID -> release title
	searches map[string]*keptSearch
}

type keptSearch struct {
	at    time.Time
	links map[int]usenetLink
}

type usenetLink struct{ title, url string }

// NewUsenet checks cfg and returns the flow.
func NewUsenet(cfg UsenetConfig) (*Usenet, error) {
	for _, u := range []struct{ name, addr string }{{"Prowlarr", cfg.ProwlarrURL}, {"SABnzbd", cfg.SabURL}} {
		p, err := url.Parse(strings.TrimSpace(u.addr))
		if err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
			return nil, fmt.Errorf("the %s URL must be an http or https address, such as http://192.168.1.10:9696", u.name)
		}
	}
	if strings.TrimSpace(cfg.ProwlarrKey) == "" || strings.TrimSpace(cfg.SabKey) == "" {
		return nil, errors.New("both the Prowlarr API key and the SABnzbd API key are needed")
	}
	cfg.ProwlarrURL = strings.TrimRight(strings.TrimSpace(cfg.ProwlarrURL), "/")
	cfg.SabURL = strings.TrimRight(strings.TrimSpace(cfg.SabURL), "/")
	return &Usenet{
		cfg: cfg, client: &http.Client{Timeout: 90 * time.Second}, nyaa: "https://nyaa.si", now: time.Now,
		titles: map[string]string{}, searches: map[string]*keptSearch{},
	}, nil
}

// Status is the page's view of the configuration.
func (u *Usenet) Status() UsenetStatus {
	return UsenetStatus{Enabled: true, Categories: u.cfg.Categories}
}

var (
	nyaaTitleTag = regexp.MustCompile(`(?is)<title>(.*?)</title>`)
	notAlnum     = regexp.MustCompile(`[^\p{L}\p{N}]+`)
)

// NyaaTitle is the release title of a Nyaa page: the heading at its top,
// which the page's <title> repeats before " :: Nyaa". The file list further
// down, which can name a folder, is not read.
func (u *Usenet) NyaaTitle(ctx context.Context, view string) (string, error) {
	m := nyaaView.FindStringSubmatch(strings.TrimSpace(view))
	if m == nil {
		return "", errors.New("only nyaa.si release pages can be searched")
	}
	id := m[1]
	u.mu.Lock()
	title, ok := u.titles[id]
	u.mu.Unlock()
	if ok {
		return title, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.nyaa+"/view/"+id, http.NoBody)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "seadex-scout")
	res, err := u.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not reach Nyaa: %w", unwrapURL(err))
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Nyaa answered %d for that release", res.StatusCode)
	}
	page, _ := io.ReadAll(io.LimitReader(res.Body, maxNyaaPageBytes))
	t := nyaaTitleTag.FindSubmatch(page)
	if t == nil {
		return "", errors.New("the Nyaa page has no title")
	}
	title = strings.TrimSpace(html.UnescapeString(string(t[1])))
	title = strings.TrimSpace(strings.TrimSuffix(title, ":: Nyaa"))
	if title == "" || strings.EqualFold(title, "Nyaa") {
		return "", errors.New("the Nyaa page has no release title")
	}
	u.mu.Lock()
	if len(u.titles) >= maxTitleCache {
		clear(u.titles)
	}
	u.titles[id] = title
	u.mu.Unlock()
	return title, nil
}

// Search looks the Nyaa release up on Prowlarr and returns its usenet
// results, exact title matches first.
func (u *Usenet) Search(ctx context.Context, view string) (*UsenetSearch, error) {
	title, err := u.NyaaTitle(ctx, view)
	if err != nil {
		return nil, err
	}
	q := url.Values{"query": {title}, "type": {"search"}, "limit": {"100"}}
	var found []struct {
		GUID        string    `json:"guid"`
		Title       string    `json:"title"`
		Indexer     string    `json:"indexer"`
		Size        int64     `json:"size"`
		Grabs       int       `json:"grabs"`
		PublishDate time.Time `json:"publishDate"`
		Protocol    string    `json:"protocol"`
		DownloadURL string    `json:"downloadUrl"`
	}
	if err := u.prowlarrGet(ctx, "/api/v1/search?"+q.Encode(), &found); err != nil {
		return nil, err
	}

	want := matchKey(title)
	out := &UsenetSearch{Query: title, Results: []UsenetResult{}}
	links := map[int]usenetLink{}
	for _, r := range found {
		if !strings.EqualFold(r.Protocol, "usenet") || r.DownloadURL == "" {
			out.Other++
			continue
		}
		id := len(links) + 1
		links[id] = usenetLink{title: r.Title, url: r.DownloadURL}
		out.Results = append(out.Results, UsenetResult{ID: id, Title: r.Title, Indexer: r.Indexer, Size: r.Size,
			Grabs: r.Grabs, Posted: r.PublishDate, Exact: matchKey(r.Title) == want})
	}
	slices.SortStableFunc(out.Results, func(a, b UsenetResult) int {
		if a.Exact != b.Exact {
			if a.Exact {
				return -1
			}
			return 1
		}
		return b.Posted.Compare(a.Posted)
	})
	if len(out.Results) > maxUsenetResults {
		out.Results = out.Results[:maxUsenetResults]
	}
	out.Token = u.keep(links)
	return out, nil
}

// matchKey compares titles the way a person would: case, spacing and
// punctuation ("." for " " on usenet) aside.
func matchKey(s string) string {
	return strings.ToLower(notAlnum.ReplaceAllString(s, ""))
}

func (u *Usenet) keep(links map[int]usenetLink) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	now := u.now()
	u.mu.Lock()
	defer u.mu.Unlock()
	for k, s := range u.searches {
		if now.Sub(s.at) > searchKeep || len(u.searches) >= maxSearchesKept {
			delete(u.searches, k)
		}
	}
	u.searches[token] = &keptSearch{at: now, links: links}
	return token
}

// Send adds a result of an earlier search to SABnzbd under arr's category.
// Only a result Prowlarr returned can be sent: the page names it by token
// and ID, never by address.
func (u *Usenet) Send(ctx context.Context, token string, id int, arr string) (string, error) {
	if arr != "sonarr" && arr != "radarr" {
		return "", errors.New("unknown app")
	}
	u.mu.Lock()
	s, ok := u.searches[token]
	var link usenetLink
	if ok && u.now().Sub(s.at) <= searchKeep {
		link, ok = s.links[id]
	} else {
		ok = false
	}
	u.mu.Unlock()
	if !ok {
		return "", errors.New("that search has expired; search again")
	}
	category := strings.TrimSpace(u.cfg.Categories[arr])
	q := url.Values{"mode": {"addurl"}, "name": {link.url}, "nzbname": {link.title}, "output": {"json"}}
	if category != "" {
		q.Set("cat", category)
	}
	var answer struct {
		Status bool   `json:"status"`
		Error  string `json:"error"`
	}
	if err := u.sabGet(ctx, q, &answer); err != nil {
		return "", err
	}
	if !answer.Status {
		return "", fmt.Errorf("SABnzbd refused it: %s", cmpOr(answer.Error, "no reason given"))
	}
	return category, nil
}

// Check reaches both services and returns their versions, for the
// dashboard's Test connection button.
func (u *Usenet) Check(ctx context.Context) (prowlarr, sab string, err error) {
	var status struct {
		Version string `json:"version"`
	}
	if err := u.prowlarrGet(ctx, "/api/v1/system/status", &status); err != nil {
		return "", "", err
	}
	// mode=version needs no key, so the key is proved by listing categories.
	var cats struct {
		Categories []string `json:"categories"`
		Error      string   `json:"error"`
	}
	if err := u.sabGet(ctx, url.Values{"mode": {"get_cats"}, "output": {"json"}}, &cats); err != nil {
		return status.Version, "", err
	}
	if cats.Error != "" {
		return status.Version, "", fmt.Errorf("SABnzbd: %s", cats.Error)
	}
	var version struct {
		Version string `json:"version"`
	}
	_ = u.sabGet(ctx, url.Values{"mode": {"version"}, "output": {"json"}}, &version)
	for arr, cat := range u.cfg.Categories {
		if cat != "" && !slices.Contains(cats.Categories, cat) {
			return status.Version, version.Version, fmt.Errorf("SABnzbd has no category %q (set for %s); add it in SABnzbd or change the setting", cat, arr)
		}
	}
	return status.Version, version.Version, nil
}

func (u *Usenet) prowlarrGet(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.cfg.ProwlarrURL+path, http.NoBody)
	if err != nil {
		return errors.New("the Prowlarr URL is not valid")
	}
	req.Header.Set("X-Api-Key", u.cfg.ProwlarrKey)
	req.Header.Set("Accept", "application/json")
	res, err := u.client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach Prowlarr: %w", unwrapURL(err))
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return errors.New("Prowlarr refused the API key")
	case res.StatusCode != http.StatusOK:
		detail, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		return fmt.Errorf("Prowlarr answered %d%s", res.StatusCode, bodyNote(strings.TrimSpace(string(detail))))
	}
	return json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(out)
}

// sabGet calls SABnzbd's API. The key has to travel in the query, which is
// SABnzbd's only way, so errors never quote the URL.
func (u *Usenet) sabGet(ctx context.Context, q url.Values, out any) error {
	q.Set("apikey", u.cfg.SabKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.cfg.SabURL+"/api?"+q.Encode(), http.NoBody)
	if err != nil {
		return errors.New("the SABnzbd URL is not valid")
	}
	res, err := u.client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach SABnzbd: %w", unwrapURL(err))
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("SABnzbd answered %d", res.StatusCode)
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err := json.Unmarshal(raw, out); err != nil {
		// A bad key answers plain text such as "API Key Incorrect".
		return fmt.Errorf("SABnzbd: %s", clip(strings.TrimSpace(string(raw)), 200))
	}
	return nil
}

// unwrapURL drops the request URL a client error wraps, which for SABnzbd
// would carry the API key.
func unwrapURL(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return uerr.Err
	}
	return err
}
