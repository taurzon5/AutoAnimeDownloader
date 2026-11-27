// Package shoko reads a Shoko Server library (its v3 API, measured against v5.3.3): which series
// it has, and which of their episodes have files. The app only reads, apart from signing in; it
// never imports, renames or deletes anything in Shoko (the Shoko spec, step 1).
package shoko

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrUnauthorized is a refused sign-in or API key: Shoko answers 401 to both.
var ErrUnauthorized = errors.New("shoko refused the credentials")

// ErrNotFound is a series (or file) Shoko no longer has.
var ErrNotFound = errors.New("shoko has no such item")

// Device is the name Shoko shows for the app's API key.
const Device = "AutoAnimeDownloader"

// seriesPageSize and filePageSize are the largest pages Shoko serves for each list.
const (
	seriesPageSize = 100
	filePageSize   = 1000
)

// Client talks to one Shoko server with an API key.
type Client struct {
	base   *url.URL
	apiKey string
	http   *http.Client
}

func parseBase(rawURL string) (*url.URL, error) {
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(rawURL), "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("shoko URL must be an http(s) address, like http://127.0.0.1:8111")
	}
	return base, nil
}

// NewClient returns a client for the server at rawURL, signed in with apiKey.
func NewClient(rawURL, apiKey string) (*Client, error) {
	base, err := parseBase(rawURL)
	if err != nil {
		return nil, err
	}
	return &Client{base: base, apiKey: apiKey, http: &http.Client{Timeout: 60 * time.Second}}, nil
}

// Login signs in with a Shoko user and returns the API key Shoko makes for the app. The password is
// sent once and kept nowhere (the Shoko spec's answer 1).
func Login(ctx context.Context, rawURL, user, pass string) (string, error) {
	base, err := parseBase(rawURL)
	if err != nil {
		return "", err
	}
	body, _ := json.Marshal(map[string]string{"user": user, "pass": pass, "device": Device})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String()+"/api/auth", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("shoko did not answer: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return "", ErrUnauthorized
	case resp.StatusCode/100 != 2:
		return "", fmt.Errorf("shoko sign-in answered HTTP %d", resp.StatusCode)
	}
	var out struct {
		APIKey string `json:"apikey"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.APIKey == "" {
		return "", errors.New("shoko's sign-in answer has no API key")
	}
	return out.APIKey, nil
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	u := c.base.String() + "/api/v3/" + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("apikey", c.apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("shoko did not answer: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: %s", ErrNotFound, path)
	}
	if resp.StatusCode/100 != 2 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("shoko %s answered HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out == nil {
		// An action: Shoko answers 200 with an empty body (ImportNewFiles, measured on v5.3.3).
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("unreadable shoko %s answer: %w", path, err)
	}
	return nil
}

// ImportFolder is one of Shoko's import folders as Shoko sees it: in Docker, Path is inside
// Shoko's container. A drop source ("Source" or "Both") is a folder Shoko imports from and may move
// files out of; Watched is Shoko's folder watcher (WatchForNewFiles).
type ImportFolder struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	DropType string `json:"drop_type"`
	Watched  bool   `json:"watched"`
}

// IsDropSource reports whether Shoko imports from this folder as a drop source.
func (f ImportFolder) IsDropSource() bool { return f.DropType == "Source" || f.DropType == "Both" }

// ImportFolders lists Shoko's import folders: GET /api/v3/ManagedFolder on Shoko 6 (the folders
// were renamed "managed folders", same fields; ImportFolder answers 404 there, measured on
// v6.0.0), else GET /api/v3/ImportFolder (v5).
func (c *Client) ImportFolders(ctx context.Context) ([]ImportFolder, error) {
	var raw []struct {
		ID               int    `json:"ID"`
		Name             string `json:"Name"`
		Path             string `json:"Path"`
		DropFolderType   string `json:"DropFolderType"`
		WatchForNewFiles bool   `json:"WatchForNewFiles"`
	}
	err := c.get(ctx, "ManagedFolder", nil, &raw)
	if errors.Is(err, ErrNotFound) {
		err = c.get(ctx, "ImportFolder", nil, &raw)
	}
	if err != nil {
		return nil, err
	}
	out := make([]ImportFolder, 0, len(raw))
	for _, f := range raw {
		out = append(out, ImportFolder{ID: f.ID, Name: f.Name, Path: f.Path, DropType: f.DropFolderType, Watched: f.WatchForNewFiles})
	}
	return out, nil
}

// ImportNewFiles asks Shoko to import the new files in its import folders now, instead of waiting
// for its folder watcher, which may be off (GET /api/v3/Action/ImportNewFiles: 200 and an empty
// body with a key, 401 without, measured on v5.3.3). Shoko queues the work and answers at once.
func (c *Client) ImportNewFiles(ctx context.Context) error {
	return c.get(ctx, "Action/ImportNewFiles", nil, nil)
}

// SeriesOfAniDB is the Shoko series of an AniDB anime (GET /api/v3/Series/AniDB/{id}/Series, a
// lookup in Shoko's own database: no AniDB request). ErrNotFound when Shoko has none.
func (c *Client) SeriesOfAniDB(ctx context.Context, anidbID int) (int, error) {
	var s struct {
		IDs struct {
			ID int `json:"ID"`
		} `json:"IDs"`
	}
	if err := c.get(ctx, "Series/AniDB/"+strconv.Itoa(anidbID)+"/Series", nil, &s); err != nil {
		return 0, err
	}
	if s.IDs.ID <= 0 {
		return 0, fmt.Errorf("%w: AniDB %d", ErrNotFound, anidbID)
	}
	return s.IDs.ID, nil
}

// Series is a Shoko series (one AniDB anime) as the app keeps it.
type Series struct {
	ID            int    `json:"id"`
	AniDB         int    `json:"anidb"`
	MAL           []int  `json:"mal,omitempty"`
	TopLevelGroup int    `json:"top_level_group,omitempty"`
	Name          string `json:"name"`
	// Episodes is the series' count of regular episodes (Sizes.Total.Episodes).
	Episodes int `json:"episodes"`
}

// File is a file Shoko has for an episode.
type File struct {
	ID       int       `json:"id"`
	Size     int64     `json:"size"`
	Imported time.Time `json:"imported"`
	// Path is the first location's path, relative to its import folder.
	Path string `json:"path"`
	// Accessible: Shoko can reach at least one of its locations right now. One it can't (an
	// unplugged drive) still counts as owned: downloading again would make a duplicate.
	Accessible bool `json:"accessible"`
}

// Episode is a regular episode of a series that has at least one file.
type Episode struct {
	Number int    `json:"number"`
	Files  []File `json:"files"`
}

type apiList[T any] struct {
	Total int `json:"Total"`
	List  []T `json:"List"`
}

type apiSeries struct {
	IDs struct {
		ID            int   `json:"ID"`
		AniDB         int   `json:"AniDB"`
		MAL           []int `json:"MAL"`
		TopLevelGroup int   `json:"TopLevelGroup"`
	} `json:"IDs"`
	Name  string `json:"Name"`
	Sizes struct {
		Total struct {
			Episodes int `json:"Episodes"`
		} `json:"Total"`
	} `json:"Sizes"`
}

func (a apiSeries) series() Series {
	return Series{ID: a.IDs.ID, AniDB: a.IDs.AniDB, MAL: a.IDs.MAL, TopLevelGroup: a.IDs.TopLevelGroup,
		Name: a.Name, Episodes: a.Sizes.Total.Episodes}
}

type apiFile struct {
	ID        int        `json:"ID"`
	Size      int64      `json:"Size"`
	Imported  *time.Time `json:"Imported"`
	Locations []struct {
		RelativePath string `json:"RelativePath"`
		IsAccessible bool   `json:"IsAccessible"`
	} `json:"Locations"`
	SeriesIDs []struct {
		SeriesID struct {
			ID int `json:"ID"`
		} `json:"SeriesID"`
	} `json:"SeriesIDs"`
}

func (a apiFile) file() File {
	f := File{ID: a.ID, Size: a.Size}
	if a.Imported != nil {
		f.Imported = *a.Imported
	}
	for i, l := range a.Locations {
		if i == 0 {
			f.Path = l.RelativePath
		}
		f.Accessible = f.Accessible || l.IsAccessible
	}
	return f
}

// AllSeries reads every series, page by page.
func (c *Client) AllSeries(ctx context.Context) ([]Series, error) {
	var out []Series
	for page := 1; ; page++ {
		var list apiList[apiSeries]
		q := url.Values{"pageSize": {strconv.Itoa(seriesPageSize)}, "page": {strconv.Itoa(page)}}
		if err := c.get(ctx, "Series", q, &list); err != nil {
			return nil, err
		}
		for _, s := range list.List {
			out = append(out, s.series())
		}
		if len(list.List) < seriesPageSize || len(out) >= list.Total {
			return out, nil
		}
	}
}

// SeriesByID reads one series.
func (c *Client) SeriesByID(ctx context.Context, id int) (Series, error) {
	var s apiSeries
	if err := c.get(ctx, "Series/"+strconv.Itoa(id), nil, &s); err != nil {
		return Series{}, err
	}
	return s.series(), nil
}

// OwnedEpisodes lists the regular episodes of a series that have files, with them. Measured on
// v5.3.3: the episode number needs includeDataFrom=AniDB, the type filter's value is "Episode"
// ("Normal" is silently ignored and lists credits and trailers too), and without includeMissing
// only episodes with files come back.
func (c *Client) OwnedEpisodes(ctx context.Context, seriesID int) ([]Episode, error) {
	var list apiList[struct {
		AniDB struct {
			Type          string `json:"Type"`
			EpisodeNumber int    `json:"EpisodeNumber"`
		} `json:"AniDB"`
		Files []apiFile `json:"Files"`
	}]
	q := url.Values{"includeFiles": {"true"}, "includeDataFrom": {"AniDB"}, "type": {"Episode"}, "pageSize": {"0"}}
	if err := c.get(ctx, "Series/"+strconv.Itoa(seriesID)+"/Episode", q, &list); err != nil {
		return nil, err
	}
	var out []Episode
	for _, e := range list.List {
		if e.AniDB.Type != "Episode" || e.AniDB.EpisodeNumber <= 0 || len(e.Files) == 0 {
			continue
		}
		ep := Episode{Number: e.AniDB.EpisodeNumber}
		for _, f := range e.Files {
			ep.Files = append(ep.Files, f.file())
		}
		out = append(out, ep)
	}
	return out, nil
}

// NewestImport is the most recently identified file: when it was imported, and its id. Zero with
// none.
func (c *Client) NewestImport(ctx context.Context) (time.Time, int, error) {
	var list apiList[apiFile]
	q := url.Values{"sortOrder": {"-ImportedAt"}, "exclude": {"Unrecognized"}, "pageSize": {"1"}}
	if err := c.get(ctx, "File", q, &list); err != nil {
		return time.Time{}, 0, err
	}
	if len(list.List) == 0 || list.List[0].Imported == nil {
		return time.Time{}, 0, nil
	}
	return *list.List[0].Imported, list.List[0].ID, nil
}

// importedFile is a file of the incremental read: when Shoko identified it, and its series.
type importedFile struct {
	ID       int
	Imported time.Time
	Series   []int
}

// FilesImportedSince lists the identified files imported at or after since, newest first, page by
// page until it reaches older ones. Measured on v5.3.3: the sort criterion is "ImportedAt" (the
// field is "Imported"), an unknown criterion is ignored without an error, and unidentified files
// sort last, so exclude=Unrecognized keeps them out.
func (c *Client) FilesImportedSince(ctx context.Context, since time.Time) ([]importedFile, error) {
	var out []importedFile
	for page := 1; ; page++ {
		var list apiList[apiFile]
		q := url.Values{"sortOrder": {"-ImportedAt"}, "exclude": {"Unrecognized"}, "include": {"XRefs"},
			"pageSize": {strconv.Itoa(filePageSize)}, "page": {strconv.Itoa(page)}}
		if err := c.get(ctx, "File", q, &list); err != nil {
			return nil, err
		}
		for _, f := range list.List {
			if f.Imported == nil || f.Imported.Before(since) {
				return out, nil
			}
			imported := importedFile{ID: f.ID, Imported: *f.Imported}
			for _, x := range f.SeriesIDs {
				imported.Series = append(imported.Series, x.SeriesID.ID)
			}
			out = append(out, imported)
		}
		if len(list.List) < filePageSize {
			return out, nil
		}
	}
}
