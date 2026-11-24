package torrents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// qbitMinWebAPI is the oldest qBittorrent Web API the backend speaks: 2.11 is qBittorrent 5.0, which
// renamed torrents/pause|resume to torrents/stop|start (the qBittorrent spec's answer 1).
const qbitMinWebAPI = "2.11"

// qbitRequestTimeout bounds one Web API call. qBittorrent answers locally in milliseconds; a call
// that takes longer means the WebUI is down or unreachable, and the caller should hear so.
const qbitRequestTimeout = 15 * time.Second

// errQBitUnauthorized is what a call returns when qBittorrent refused the credentials (403 after a
// fresh login, or 401 for a bad API key).
var errQBitUnauthorized = errors.New("qBittorrent refused the credentials")

// errQBitLoginRefused is a refused login. A right password is refused too when qBittorrent's Host
// header check rejects the request: a WebUI reached through a different port than the one it listens
// on (Docker mapping host port 18080 to the container's 8080) answers 401 to everything.
var errQBitLoginRefused = fmt.Errorf("%w: check the user and password; if qBittorrent runs in Docker, give its WebUI the same port inside and outside the container, or turn off \"Host header validation\" in its WebUI settings", errQBitUnauthorized)

// errQBitConflict is qBittorrent's 409: "already exists" for createCategory, "queueing is off" for
// topPrio, a rename onto a taken path.
var errQBitConflict = errors.New("qBittorrent answered 409 Conflict")

// errQBitNotFound is qBittorrent's 404: an unknown torrent hash.
var errQBitNotFound = errors.New("qBittorrent answered 404 Not Found")

// qbitClient speaks qBittorrent's Web API v2. With an API key every request carries it
// (Authorization: Bearer); otherwise it logs in with the user and password and the cookie jar keeps
// the session cookie, whatever qBittorrent names it (QBT_SID_<port> since 5.0, SID before).
type qbitClient struct {
	base     *url.URL
	http     *http.Client
	username string
	password string
	apiKey   string
}

func newQBitClient(rawURL, username, password, apiKey string) (*qbitClient, error) {
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(rawURL), "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("the qBittorrent URL %q is not an http(s) address", rawURL)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &qbitClient{
		base:     base,
		http:     &http.Client{Jar: jar, Timeout: qbitRequestTimeout},
		username: username,
		password: password,
		apiKey:   apiKey,
	}, nil
}

// endpoint is the absolute URL of /api/v2/<path>, keeping a base path the WebUI may sit under
// behind a reverse proxy ("https://host/qbittorrent").
func (c *qbitClient) endpoint(path string) string {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v2/" + path
	return u.String()
}

// login opens a cookie session. With an API key there is nothing to open.
func (c *qbitClient) login(ctx context.Context) error {
	if c.apiKey != "" {
		return nil
	}
	form := url.Values{"username": {c.username}, "password": {c.password}}
	body, status, err := c.send(ctx, http.MethodPost, "auth/login", form)
	if err != nil {
		return err
	}
	// Web API 2.11.8+ answers a successful login 204 with no body and, from 2.14, a refused one 401;
	// before that both were 200, with "Ok." or "Fails.".
	text := strings.TrimSpace(string(body))
	switch {
	case status == http.StatusForbidden:
		return fmt.Errorf("qBittorrent banned this address after too many failed logins")
	case status == http.StatusUnauthorized || text == "Fails.":
		return errQBitLoginRefused
	case status/100 != 2:
		return fmt.Errorf("qBittorrent login answered HTTP %d", status)
	case text != "" && text != "Ok.":
		return fmt.Errorf("qBittorrent login answered %q", text)
	}
	return nil
}

func (c *qbitClient) logout(ctx context.Context) {
	if c.apiKey == "" {
		_, _, _ = c.send(ctx, http.MethodPost, "auth/logout", nil)
	}
}

// call runs one Web API request. A 403 with a cookie session means the session expired (qBittorrent
// restarted, or the session timed out): it logs in once and retries.
func (c *qbitClient) call(ctx context.Context, method, path string, form url.Values) ([]byte, error) {
	body, status, err := c.send(ctx, method, path, form)
	if err != nil {
		return nil, err
	}
	if status == http.StatusForbidden && c.apiKey == "" {
		if err := c.login(ctx); err != nil {
			return nil, err
		}
		if body, status, err = c.send(ctx, method, path, form); err != nil {
			return nil, err
		}
	}
	if status/100 == 2 {
		// 200, or 204 when there is nothing to answer (Web API 2.11.8+), or 202 (torrents/add with a
		// magnet still pending).
		return body, nil
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, errQBitUnauthorized
	case http.StatusConflict:
		return nil, fmt.Errorf("%w: %s", errQBitConflict, strings.TrimSpace(string(body)))
	case http.StatusNotFound:
		return nil, errQBitNotFound
	default:
		return nil, fmt.Errorf("qBittorrent %s answered HTTP %d: %s", path, status, strings.TrimSpace(string(body)))
	}
}

func (c *qbitClient) send(ctx context.Context, method, path string, form url.Values) ([]byte, int, error) {
	target := c.endpoint(path)
	var reqBody io.Reader
	if method == http.MethodGet && len(form) > 0 {
		target += "?" + form.Encode()
	} else if form != nil {
		reqBody = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reqBody)
	if err != nil {
		return nil, 0, err
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	// qBittorrent's CSRF check wants Referer (or Origin) to match the host it is reached at.
	req.Header.Set("Referer", c.base.Scheme+"://"+c.base.Host)
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("qBittorrent is unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read qBittorrent's answer: %w", err)
	}
	return body, resp.StatusCode, nil
}

func (c *qbitClient) text(ctx context.Context, path string) (string, error) {
	body, err := c.call(ctx, http.MethodGet, path, nil)
	return strings.TrimSpace(string(body)), err
}

func (c *qbitClient) webAPIVersion(ctx context.Context) (string, error) {
	return c.text(ctx, "app/webapiVersion")
}

func (c *qbitClient) appVersion(ctx context.Context) (string, error) {
	return c.text(ctx, "app/version")
}

// createCategory creates the app's category. One that already exists is fine (409).
func (c *qbitClient) createCategory(ctx context.Context, name string) error {
	_, err := c.call(ctx, http.MethodPost, "torrents/createCategory", url.Values{"category": {name}})
	if errors.Is(err, errQBitConflict) {
		return nil
	}
	return err
}

// qbitAddRequest is a torrents/add call for one magnet.
type qbitAddRequest struct {
	Magnet   string
	SavePath string // qBittorrent's view
	Category string
	// StopAtMetadata adds with stopCondition=MetadataReceived: the torrent stops as soon as its file
	// list is known, before it writes anything, so the backend can rename the files first.
	StopAtMetadata bool
	// UploadLimit adds it with that upload limit in bytes per second (disable_seeding); 0 = none.
	UploadLimit int64
	// DownloadPath (qBittorrent's view) is where it downloads before qBittorrent moves it to
	// SavePath: the placement's staging folder (incomplete_path). Empty = straight into SavePath.
	DownloadPath string
}

func (c *qbitClient) add(ctx context.Context, r qbitAddRequest) error {
	form := url.Values{
		"urls":          {r.Magnet},
		"savepath":      {r.SavePath},
		"category":      {r.Category},
		"autoTMM":       {"false"},
		"contentLayout": {"Original"},
		// Always per torrent, so qBittorrent's global "Keep incomplete torrents in" (on by default in
		// its official Docker image) can't send the download to a folder the app doesn't know.
		"useDownloadPath": {"false"},
		// No share limit: qBittorrent's global one would stop the torrent, or remove it with its
		// files, which are the library (decisions/144).
		"ratioLimit":               {qbitNoLimit},
		"seedingTimeLimit":         {qbitNoLimit},
		"inactiveSeedingTimeLimit": {qbitNoLimit},
	}
	if r.StopAtMetadata {
		form.Set("stopCondition", "MetadataReceived")
	}
	if r.UploadLimit > 0 {
		form.Set("upLimit", strconv.FormatInt(r.UploadLimit, 10))
	}
	if r.DownloadPath != "" {
		form.Set("useDownloadPath", "true")
		form.Set("downloadPath", r.DownloadPath)
	}
	body, err := c.call(ctx, http.MethodPost, "torrents/add", form)
	if errors.Is(err, errQBitConflict) {
		// Web API 2.14+: 409 when every torrent of the call failed.
		return fmt.Errorf("qBittorrent did not add the torrent: %w", err)
	}
	if err != nil {
		return err
	}
	return qbitAddResult(body)
}

// qbitAddResult reads torrents/add's answer: "Ok." / "Fails." before Web API 2.14, and from 2.14 a
// JSON object with the counts.
func qbitAddResult(body []byte) error {
	text := strings.TrimSpace(string(body))
	if text == "Ok." {
		return nil
	}
	var counts struct {
		Success int `json:"success_count"`
		Pending int `json:"pending_count"`
		Failure int `json:"failure_count"`
	}
	if json.Unmarshal(body, &counts) == nil && strings.HasPrefix(text, "{") {
		if counts.Success+counts.Pending > 0 {
			return nil
		}
		return fmt.Errorf("qBittorrent did not add the torrent (%d failed)", counts.Failure)
	}
	return fmt.Errorf("qBittorrent did not add the torrent (%s)", text)
}

// qbitTorrent is the part of a torrents/info (or sync/maindata) entry the backend reads.
type qbitTorrent struct {
	Hash         string  `json:"hash"`
	Name         string  `json:"name"`
	State        string  `json:"state"`
	Progress     float64 `json:"progress"`
	Size         int64   `json:"size"`
	TotalSize    int64   `json:"total_size"`
	Completed    int64   `json:"completed"`
	Uploaded     int64   `json:"uploaded"`
	DlSpeed      int     `json:"dlspeed"`
	UpSpeed      int     `json:"upspeed"`
	NumSeeds     int     `json:"num_seeds"`
	NumLeechs    int     `json:"num_leechs"`
	ETA          int64   `json:"eta"`
	AddedOn      int64   `json:"added_on"`
	SavePath     string  `json:"save_path"`
	DownloadPath string  `json:"download_path"`
	Category     string  `json:"category"`
	Priority     int     `json:"priority"`
	SeedingTime  int64   `json:"seeding_time"`
	HasMetadata  *bool   `json:"has_metadata"`
	ErrorMessage string  `json:"error"`
	// The limits are pointers: a field qBittorrent did not send must not read as a limit of 0, or
	// the backend would set it again on every poll.
	RatioLimit               *float64 `json:"ratio_limit"`
	SeedingTimeLimit         *int64   `json:"seeding_time_limit"`
	InactiveSeedingTimeLimit *int64   `json:"inactive_seeding_time_limit"`
	UpLimit                  *int64   `json:"up_limit"`
}

// hasShareLimit reports whether qBittorrent could stop or remove the torrent for its ratio or seeding
// time: any of the three limits other than "none" (-1), including "use the global one" (-2).
func (t qbitTorrent) hasShareLimit() bool {
	if t.RatioLimit != nil && *t.RatioLimit != -1 {
		return true
	}
	for _, v := range []*int64{t.SeedingTimeLimit, t.InactiveSeedingTimeLimit} {
		if v != nil && *v != -1 {
			return true
		}
	}
	return false
}

// metadataKnown reports whether qBittorrent has the torrent's file list. has_metadata only exists
// in newer Web APIs; without it, a total size above zero means the same.
func (t qbitTorrent) metadataKnown() bool {
	if t.HasMetadata != nil {
		return *t.HasMetadata
	}
	return t.TotalSize > 0
}

func (c *qbitClient) info(ctx context.Context, category string, hashes ...string) ([]qbitTorrent, error) {
	form := url.Values{}
	if category != "" {
		form.Set("category", category)
	}
	if len(hashes) > 0 {
		form.Set("hashes", strings.Join(hashes, "|"))
	}
	body, err := c.call(ctx, http.MethodGet, "torrents/info", form)
	if err != nil {
		return nil, err
	}
	var out []qbitTorrent
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("unreadable torrents/info answer: %w", err)
	}
	return out, nil
}

// qbitMainData is one sync/maindata answer. Torrents holds partial entries: only the fields that
// changed since rid, unless FullUpdate.
type qbitMainData struct {
	RID             int64                                 `json:"rid"`
	FullUpdate      bool                                  `json:"full_update"`
	Torrents        map[string]map[string]json.RawMessage `json:"torrents"`
	TorrentsRemoved []string                              `json:"torrents_removed"`
}

func (c *qbitClient) mainData(ctx context.Context, rid int64) (qbitMainData, error) {
	body, err := c.call(ctx, http.MethodGet, "sync/maindata", url.Values{"rid": {strconv.FormatInt(rid, 10)}})
	if err != nil {
		return qbitMainData{}, err
	}
	var out qbitMainData
	if err := json.Unmarshal(body, &out); err != nil {
		return qbitMainData{}, fmt.Errorf("unreadable sync/maindata answer: %w", err)
	}
	return out, nil
}

// qbitFile is one entry of torrents/files.
type qbitFile struct {
	Index    int     `json:"index"`
	Name     string  `json:"name"`
	Size     int64   `json:"size"`
	Progress float64 `json:"progress"`
}

func (c *qbitClient) files(ctx context.Context, hash string) ([]qbitFile, error) {
	body, err := c.call(ctx, http.MethodGet, "torrents/files", url.Values{"hash": {hash}})
	if err != nil {
		return nil, err
	}
	var out []qbitFile
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("unreadable torrents/files answer: %w", err)
	}
	return out, nil
}

// hashesCall posts one of the hash-list actions (start, stop, delete, topPrio, reannounce, recheck).
func (c *qbitClient) hashesCall(ctx context.Context, path string, hashes []string, extra url.Values) error {
	form := url.Values{"hashes": {strings.Join(hashes, "|")}}
	for k, v := range extra {
		form[k] = v
	}
	_, err := c.call(ctx, http.MethodPost, path, form)
	return err
}

func (c *qbitClient) start(ctx context.Context, hashes ...string) error {
	return c.hashesCall(ctx, "torrents/start", hashes, nil)
}

func (c *qbitClient) stop(ctx context.Context, hashes ...string) error {
	return c.hashesCall(ctx, "torrents/stop", hashes, nil)
}

func (c *qbitClient) remove(ctx context.Context, hash string, deleteFiles bool) error {
	return c.hashesCall(ctx, "torrents/delete", []string{hash}, url.Values{"deleteFiles": {strconv.FormatBool(deleteFiles)}})
}

// topPrio moves the torrents to the front of qBittorrent's queue. With queueing off qBittorrent
// answers 409 and there is no queue to move in: that is not an error here.
func (c *qbitClient) topPrio(ctx context.Context, hashes ...string) error {
	err := c.hashesCall(ctx, "torrents/topPrio", hashes, nil)
	if errors.Is(err, errQBitConflict) {
		return nil
	}
	return err
}

func (c *qbitClient) reannounce(ctx context.Context, hashes ...string) error {
	return c.hashesCall(ctx, "torrents/reannounce", hashes, nil)
}

func (c *qbitClient) recheck(ctx context.Context, hashes ...string) error {
	return c.hashesCall(ctx, "torrents/recheck", hashes, nil)
}

// qbitNoLimit is qBittorrent's "no limit" for a torrent's share limits and its queue caps; -2 means
// "use the global setting".
const qbitNoLimit = "-1"

// qbitDefaultUploadFloorKiB is disable_seeding's upload limit with qBittorrent when the config has
// none (qbittorrent_disable_seeding_upload_kib). qBittorrent has no "upload nothing": 0 means
// unlimited there, and so do 0 upload slots. And the limit also carries the download's own
// requests, so 1 B/s stalls it before it even has its file list (decisions/144).
const qbitDefaultUploadFloorKiB = 16

// setNoShareLimit removes every share limit from the torrents. qBittorrent 5.3.0 (Web API 2.16.2,
// measured) refuses the call without shareLimitAction and shareLimitsMode; versions that don't have
// them don't read them.
func (c *qbitClient) setNoShareLimit(ctx context.Context, hashes ...string) error {
	return c.hashesCall(ctx, "torrents/setShareLimits", hashes, url.Values{
		"ratioLimit": {qbitNoLimit}, "seedingTimeLimit": {qbitNoLimit}, "inactiveSeedingTimeLimit": {qbitNoLimit},
		"shareLimitAction": {"Default"}, "shareLimitsMode": {"Default"},
	})
}

// setUploadLimit sets the torrents' upload limit in bytes per second; 0 means unlimited.
func (c *qbitClient) setUploadLimit(ctx context.Context, limit int64, hashes ...string) error {
	return c.hashesCall(ctx, "torrents/setUploadLimit", hashes, url.Values{"limit": {strconv.FormatInt(limit, 10)}})
}

// qbitQueuePrefs is qBittorrent's download queue in its preferences. -1 is "no limit".
type qbitQueuePrefs struct {
	QueueingEnabled    bool `json:"queueing_enabled"`
	MaxActiveDownloads int  `json:"max_active_downloads"`
	MaxActiveUploads   int  `json:"max_active_uploads"`
	MaxActiveTorrents  int  `json:"max_active_torrents"`
}

// qbitManagedPrefs is the part of qBittorrent's preferences the app sets when it manages them
// (decisions/144): the queue, the ".!qB" extension and the disk I/O type.
type qbitManagedPrefs struct {
	qbitQueuePrefs
	IncompleteFilesExt bool `json:"incomplete_files_ext"`
	DiskIOType         int  `json:"disk_io_type"`
}

// qBittorrent's disk_io_type values (its WebUI's "Disk IO type", which needs a restart). The app
// moves Default and MMap to SimplePread when it manages the settings: libtorrent 2's memory-mapped
// I/O crashes the client when a disk goes away under it (a USB drive) and is slow on network
// shares. POSIX and the newer pread mode are left as the user chose.
const (
	qbitDiskIODefault     = 0
	qbitDiskIOMMap        = 1
	qbitDiskIOSimplePread = 3
)

func (c *qbitClient) managedPrefs(ctx context.Context) (qbitManagedPrefs, error) {
	body, err := c.call(ctx, http.MethodGet, "app/preferences", nil)
	if err != nil {
		return qbitManagedPrefs{}, err
	}
	var out qbitManagedPrefs
	if err := json.Unmarshal(body, &out); err != nil {
		return qbitManagedPrefs{}, fmt.Errorf("unreadable app/preferences answer: %w", err)
	}
	return out, nil
}

// setPrefs sends the keys of want that differ from have, and nothing when none does: qBittorrent
// changes only the keys it is sent.
func (c *qbitClient) setPrefs(ctx context.Context, have, want qbitManagedPrefs) (bool, error) {
	var haveMap, wantMap map[string]json.RawMessage
	for _, v := range []struct {
		from any
		to   *map[string]json.RawMessage
	}{{have, &haveMap}, {want, &wantMap}} {
		data, err := json.Marshal(v.from)
		if err != nil {
			return false, err
		}
		if err := json.Unmarshal(data, v.to); err != nil {
			return false, err
		}
	}
	changed := map[string]json.RawMessage{}
	for k, v := range wantMap {
		if string(haveMap[k]) != string(v) {
			changed[k] = v
		}
	}
	if len(changed) == 0 {
		return false, nil
	}
	data, err := json.Marshal(changed)
	if err != nil {
		return false, err
	}
	_, err = c.call(ctx, http.MethodPost, "app/setPreferences", url.Values{"json": {string(data)}})
	return err == nil, err
}

// setDownloadPath changes where the torrent downloads before it is moved to its save path.
// qBittorrent moves the files already there with it (measured on 5.3.0; a folder the user already
// moved by hand is kept, and a recheck finds its data). An empty path stops using one: the files go
// back to the save path at once.
func (c *qbitClient) setDownloadPath(ctx context.Context, hash, path string) error {
	_, err := c.call(ctx, http.MethodPost, "torrents/setDownloadPath", url.Values{"id": {hash}, "path": {path}})
	return err
}

// renameFile moves one file of a torrent. Both paths are relative to the torrent's save path and
// use "/"; the new path may change folders.
func (c *qbitClient) renameFile(ctx context.Context, hash, oldPath, newPath string) error {
	_, err := c.call(ctx, http.MethodPost, "torrents/renameFile", url.Values{
		"hash": {hash}, "oldPath": {oldPath}, "newPath": {newPath},
	})
	return err
}

// directoryContent lists the files in dir as qBittorrent sees it (the connection test's probe).
func (c *qbitClient) directoryContent(ctx context.Context, dir string) ([]string, error) {
	body, err := c.call(ctx, http.MethodGet, "app/getDirectoryContent", url.Values{"dirPath": {dir}, "mode": {"files"}})
	if err != nil {
		return nil, err
	}
	var out []string
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("unreadable getDirectoryContent answer: %w", err)
	}
	return out, nil
}

// webAPIAtLeast compares dotted Web API versions ("2.11.3" >= "2.11").
func webAPIAtLeast(version, minimum string) bool {
	parse := func(s string) []int {
		var out []int
		for _, part := range strings.Split(strings.TrimPrefix(strings.TrimSpace(s), "v"), ".") {
			n, err := strconv.Atoi(part)
			if err != nil {
				return out
			}
			out = append(out, n)
		}
		return out
	}
	have, want := parse(version), parse(minimum)
	for i := 0; i < len(want); i++ {
		h := 0
		if i < len(have) {
			h = have[i]
		}
		if h != want[i] {
			return h > want[i]
		}
	}
	return true
}
