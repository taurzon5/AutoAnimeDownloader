package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/daemon"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/shoko"
)

func shokoAuthServer(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ User, Pass, Device string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/api/auth" || body.User != "admin" || body.Pass != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		io.WriteString(w, `{"apikey":"shoko-key-1"}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func post(t *testing.T, h http.HandlerFunc, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(data)))
	return w
}

// The sign-in keeps only the key Shoko returns: never the password (the Shoko spec's answer 1).
func TestShokoLoginSavesOnlyTheKey(t *testing.T) {
	srv := shokoAuthServer(t)
	mock := &mockFileManager{configs: files.DefaultConfig()}
	server := &Server{State: daemon.NewState(), FileManager: mock}

	if w := post(t, handleShokoLogin(server), shokoLoginRequest{URL: srv.URL + "/", Username: "admin", Password: "wrong"}); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "SHOKO_LOGIN_REFUSED") || mock.configs.ShokoAPIKey != "" {
		t.Fatalf("a refused sign-in = %d %s, key %q", w.Code, w.Body.String(), mock.configs.ShokoAPIKey)
	}
	w := post(t, handleShokoLogin(server), shokoLoginRequest{URL: srv.URL + "/", Username: "admin", Password: "pw"})
	if w.Code != http.StatusOK || mock.configs.ShokoAPIKey != "shoko-key-1" || mock.configs.ShokoURL != srv.URL {
		t.Fatalf("sign-in = %d %s; saved url %q key %q", w.Code, w.Body.String(), mock.configs.ShokoURL, mock.configs.ShokoAPIKey)
	}
	saved, _ := json.Marshal(mock.configs)
	if strings.Contains(string(saved), `"pw"`) {
		t.Fatal("the password was saved")
	}
	if w := post(t, handleShokoLogin(server), shokoLoginRequest{URL: "localhost:8111", Username: "admin", Password: "pw"}); w.Code != http.StatusBadRequest {
		t.Fatalf("a URL without a scheme = %d", w.Code)
	}
}

// The key is a secret like the qBittorrent password: masked by GET /config, kept by a PUT that
// sends the placeholder only for the same address (decisions/143).
func TestShokoKeyIsMaskedAndKeptOnlyForTheSameAddress(t *testing.T) {
	c := files.DefaultConfig()
	c.CompletedAnimePath = t.TempDir()
	c.ShokoURL, c.ShokoAPIKey = "http://127.0.0.1:8111", "shoko-key-1"
	if masked := c.MaskSecrets(); masked.ShokoAPIKey != files.SecretPlaceholder {
		t.Fatalf("masked key = %q", masked.ShokoAPIKey)
	}
	edit := *c
	edit.ShokoAPIKey = files.SecretPlaceholder
	edit.ShokoURL = "http://127.0.0.1:8111/"
	if err := edit.KeepSecrets(c); err != nil || edit.ShokoAPIKey != "shoko-key-1" {
		t.Fatalf("same address: %v, key %q", err, edit.ShokoAPIKey)
	}
	// The address cleared with the key still saved: signing out, the key goes too.
	edit = *c
	edit.ShokoAPIKey = files.SecretPlaceholder
	edit.ShokoURL = " "
	if err := edit.KeepSecrets(c); err != nil || edit.ShokoAPIKey != "" {
		t.Fatalf("an emptied address: %v, key %q; want signed out", err, edit.ShokoAPIKey)
	}
	edit = *c
	edit.ShokoAPIKey = files.SecretPlaceholder
	edit.ShokoURL = "http://evil.example:8111"
	if err := edit.KeepSecrets(c); err != files.ErrShokoNeedsSignIn {
		t.Fatalf("another address: %v, want ErrShokoNeedsSignIn", err)
	}
}

func TestShokoStatusAndSync(t *testing.T) {
	c := files.DefaultConfig()
	c.ShokoEnabled, c.ShokoURL, c.ShokoAPIKey = true, "http://127.0.0.1:8111", "k"
	state := daemon.NewState()
	server := &Server{State: state, FileManager: &mockFileManager{configs: c}}

	w := httptest.NewRecorder()
	handleShokoStatus(server)(w, httptest.NewRequest(http.MethodGet, "/", nil))
	var resp struct {
		Data ShokoStatus `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	st := resp.Data
	if w.Code != http.StatusOK || !st.Enabled || !st.SignedIn || st.URL != c.ShokoURL {
		t.Fatalf("status = %d %+v", w.Code, st)
	}
	if strings.Contains(w.Body.String(), `"k"`) {
		t.Fatal("the status shows the key")
	}
	for _, absent := range []string{"last_sync", "last_full_sync", "last_event", "0001-01-01"} {
		if strings.Contains(w.Body.String(), absent) {
			t.Errorf("a status never synced carries %q: %s", absent, w.Body.String())
		}
	}

	// Without the integration (no service), sync says so instead of starting a pass.
	if w := post(t, handleShokoSync(server), map[string]bool{"full": true}); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("sync without the service = %d", w.Code)
	}
}

func TestAnimeSettingsShokoMatch(t *testing.T) {
	mock := &mockFileManager{configs: files.DefaultConfig()}
	server := &Server{State: daemon.NewState(), FileManager: mock}
	put := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/v1/animes/7/settings", strings.NewReader(body))
		req.SetPathValue("id", "7")
		handleAnimeSettings(server)(w, req)
		return w
	}
	if w := put(`{"progress":3,"shoko":{"series_id":12,"offset":24}}`); w.Code != http.StatusOK {
		t.Fatalf("set = %d %s", w.Code, w.Body.String())
	}
	if got := mock.animeSettings[7]; got.Progress != 3 || got.Shoko == nil || *got.Shoko != (files.ShokoMatch{SeriesID: 12, Offset: 24}) {
		t.Fatalf("saved = %+v", got)
	}
	if w := put(`{"progress":4}`); w.Code != http.StatusOK || mock.animeSettings[7].Shoko == nil {
		t.Fatal("a PUT without shoko dropped the match")
	}
	if w := put(`{"shoko":{"none":true}}`); w.Code != http.StatusOK || !mock.animeSettings[7].Shoko.None {
		t.Fatalf("none = %+v", mock.animeSettings[7].Shoko)
	}
	if w := put(`{"shoko":{"auto":true}}`); w.Code != http.StatusOK || mock.animeSettings[7].Shoko != nil {
		t.Fatalf("auto = %+v", mock.animeSettings[7].Shoko)
	}
	for _, bad := range []string{`{"shoko":{}}`, `{"shoko":{"series_id":3,"none":true}}`, `{"shoko":{"offset":2}}`, `{"shoko":{"series_id":-1}}`} {
		if w := put(bad); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", bad, w.Code)
		}
	}
}

func TestUpdateConfig_ValidatesShoko(t *testing.T) {
	// shokoServer has a Shoko service that read one import folder, "/import/" as Shoko sees it.
	shokoServer := func(t *testing.T) *Server {
		svc := daemon.NewShokoService(t.TempDir())
		if err := svc.Store().SetImportFolders([]shoko.ImportFolder{{ID: 1, Name: "drop", Path: "/import/", DropType: "Source"}}); err != nil {
			t.Fatal(err)
		}
		state := daemon.NewState()
		state.SetShoko(svc)
		return &Server{State: state, FileManager: &mockFileManager{configs: files.DefaultConfig()}}
	}
	// inShoko makes a download folder Shoko's: Shoko sees it as /import.
	inShoko := func(c *files.Config, dir string) {
		c.ShokoEnabled, c.ShokoURL, c.ShokoAPIKey = true, "http://x:8111", "k"
		c.LibraryMode = files.LibraryModeDirect
		c.DownloadRoots = []files.DownloadRoot{{Name: "shoko", Path: dir}}
		c.ShokoPathMap = []files.ShokoPathMapping{{App: dir, Shoko: "/import"}}
	}
	for name, edit := range map[string]func(c *files.Config){
		"refresh under an hour": func(c *files.Config) {
			c.ShokoEnabled, c.ShokoURL, c.ShokoAPIKey, c.ShokoRefreshHours = true, "http://x:8111", "k", 0
		},
		"on without a sign-in":  func(c *files.Config) { c.ShokoEnabled, c.ShokoAPIKey = true, "" },
		"on without an address": func(c *files.Config) { c.ShokoEnabled, c.ShokoURL, c.ShokoAPIKey = true, "", "k" },
		"negative full resync":  func(c *files.Config) { c.ShokoFullResyncDays = -1 },
		"path map with a relative folder": func(c *files.Config) {
			c.ShokoPathMap = []files.ShokoPathMapping{{App: "anime", Shoko: "/import"}}
		},
		"path map nesting Shoko's folders": func(c *files.Config) {
			c.ShokoPathMap = []files.ShokoPathMapping{{App: "/mnt/a", Shoko: "/import"}, {App: "/mnt/b", Shoko: "/import/sub"}}
		},
		"shoko folder writing in place": func(c *files.Config) {
			inShoko(c, t.TempDir())
			c.IncompleteFiles = files.IncompleteFilesInPlace
		},
		"shoko folder staged by qBittorrent": func(c *files.Config) {
			inShoko(c, t.TempDir())
			c.IncompletePath = t.TempDir()
			c.TorrentClient, c.QBittorrentURL = files.TorrentClientQBittorrent, "http://qbit:8080"
		},
	} {
		c := files.DefaultConfig()
		c.CompletedAnimePath = t.TempDir()
		edit(c)
		if w := putConfig(t, shokoServer(t), c); w.Code != http.StatusBadRequest {
			t.Errorf("%s: PUT = %d, want 400", name, w.Code)
		}
	}
	server := shokoServer(t)
	c := files.DefaultConfig()
	c.CompletedAnimePath = t.TempDir()
	c.ShokoEnabled, c.ShokoURL, c.ShokoAPIKey = true, "http://127.0.0.1:8111", "k"
	if w := putConfig(t, server, c); w.Code != http.StatusOK {
		t.Fatalf("a signed-in Shoko = %d %s", w.Code, w.Body.String())
	}
	// A download folder that is one of Shoko's, in direct mode: with .part names, or writing in
	// place into a staging folder, Shoko never sees a half-written file (decisions/152).
	inShoko(c, t.TempDir())
	c.DownloadRoots = append(c.DownloadRoots, files.DownloadRoot{Name: "other", Path: t.TempDir()})
	if w := putConfig(t, server, c); w.Code != http.StatusOK {
		t.Fatalf("a Shoko folder with .part names = %d %s", w.Code, w.Body.String())
	}
	c.IncompleteFiles, c.IncompletePath = files.IncompleteFilesInPlace, t.TempDir()
	if w := putConfig(t, server, c); w.Code != http.StatusOK {
		t.Fatalf("a Shoko folder staging elsewhere = %d %s", w.Code, w.Body.String())
	}
	// No folder of Shoko's (the map gone, the paths differ): writing in place is fine.
	c.IncompletePath = ""
	c.ShokoPathMap = nil
	if w := putConfig(t, server, c); w.Code != http.StatusOK {
		t.Fatalf("no Shoko folder, writing in place = %d %s", w.Code, w.Body.String())
	}
	// Shoko off: no folder is Shoko's.
	inShoko(c, c.DownloadRoots[0].Path)
	c.ShokoEnabled = false
	if w := putConfig(t, server, c); w.Code != http.StatusOK {
		t.Fatalf("a Shoko folder with Shoko off = %d %s", w.Code, w.Body.String())
	}
}

// Episodes in Shoko are not "still to download": the progress bar and the "behind" count read them
// as there (decisions/088, decisions/147).
func TestWithShokoOwnedCountsShokoEpisodesAsThere(t *testing.T) {
	t.Cleanup(anilist.MockAniListDo(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"data":{"Page":{"media":[{"id":154587,"idMal":52991,"episodes":28}]}}}`))}, nil
	}))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/Series":
			io.WriteString(w, `{"Total":1,"List":[{"IDs":{"ID":1,"MAL":[52991]},"Name":"Frieren","Sizes":{"Total":{"Episodes":28}}}]}`)
		case "/api/v3/File":
			io.WriteString(w, `{"Total":0,"List":[]}`)
		case "/api/v3/Series/1/Episode":
			io.WriteString(w, `{"Total":2,"List":[{"AniDB":{"Type":"Episode","EpisodeNumber":1},"Files":[{"ID":1,"Locations":[{"RelativePath":"a.mkv"}]}]},{"AniDB":{"Type":"Episode","EpisodeNumber":2},"Files":[{"ID":2,"Locations":[{"RelativePath":"b.mkv"}]}]}]}`)
		}
	}))
	t.Cleanup(srv.Close)
	svc := daemon.NewShokoService(t.TempDir())
	eps := 28
	cfg := &files.Config{ShokoEnabled: true, ShokoURL: srv.URL, ShokoAPIKey: "k", ShokoRefreshHours: 24}
	svc.ForPass(context.Background(), cfg, []anilist.MediaList{{Media: anilist.Media{Id: 154587, Episodes: &eps}}}, nil, time.Now())

	downloaded := map[int]bool{3: true}
	present := withShokoOwned(downloaded, svc, 154587)
	if !present[1] || !present[2] || !present[3] || len(downloaded) != 1 {
		t.Fatalf("present = %v (downloaded %v), want 1–3 and the input untouched", present, downloaded)
	}
	if got := countPendingEpisodes(0, 4, present); got != 1 {
		t.Fatalf("pending = %d, want only episode 4", got)
	}
	if got := withShokoOwned(downloaded, nil, 154587); len(got) != 1 {
		t.Fatalf("without Shoko: %v", got)
	}
}

// The status lists Shoko's import folders as Shoko and the app see them, the last import request,
// and which download folders are Shoko's (decisions/152).
func TestShokoStatusCarriesShokosFolders(t *testing.T) {
	configs := files.DefaultConfig()
	var st ShokoStatus
	st.setImport(configs, nil, daemon.ImportStatus{})
	if body, _ := json.Marshal(st); strings.Contains(string(body), "import") || strings.Contains(string(body), "download_roots") {
		t.Fatalf("nothing asked and nothing read must leave the import fields out: %s", body)
	}
	folders := []shoko.ImportFolder{
		{ID: 1, Name: "drop", Path: "/import/", DropType: "Both"},
		{ID: 2, Name: "library", Path: "/anime/", DropType: "Destination", Watched: true},
	}
	svc := daemon.NewShokoService(t.TempDir())
	if err := svc.Store().SetImportFolders(folders); err != nil {
		t.Fatal(err)
	}
	configs.CompletedAnimePath = "/mnt/lib"
	configs.LibraryMode, configs.ShokoEnabled = files.LibraryModeDirect, true
	configs.DownloadRoots = []files.DownloadRoot{{Name: "shoko", Path: "/mnt/drop/Seasonal"}, {Name: "hdd", Path: "/mnt/hdd"}}
	configs.ShokoPathMap = []files.ShokoPathMapping{{App: "/mnt/drop", Shoko: "/import"}}
	at := time.Date(2026, 11, 24, 18, 0, 0, 0, time.UTC)
	st = ShokoStatus{}
	st.setImport(configs, svc, daemon.ImportStatus{LastRequest: at, Error: "refused", Folders: folders})
	if st.LastImportRequest == nil || !st.LastImportRequest.Equal(at) || st.ImportError != "refused" || len(st.ImportFolders) != 2 {
		t.Fatalf("status = %+v", st)
	}
	if !st.ImportFolders[0].DropSource || st.ImportFolders[1].DropSource || !st.ImportFolders[1].Watched {
		t.Errorf("folders = %+v, want the first a drop source and the second a watched destination", st.ImportFolders)
	}
	if st.ImportFolders[0].AppPath != "/mnt/drop" || st.ImportFolders[1].AppPath != "/anime/" {
		t.Errorf("app paths = %q, %q; want through the map, else as Shoko sees it", st.ImportFolders[0].AppPath, st.ImportFolders[1].AppPath)
	}
	want := []ShokoDownloadFolder{{Name: "shoko", Path: "/mnt/drop/Seasonal", InShoko: true, FolderID: 1, FolderName: "drop"}, {Name: "hdd", Path: "/mnt/hdd"}}
	if !reflect.DeepEqual(st.DownloadRoots, want) {
		t.Errorf("download folders = %+v, want %+v", st.DownloadRoots, want)
	}
}

// An anime keeps or removes the episodes in Shoko a pack brings, or follows shoko_full_packs again
// with "default"; anything else is a 400 (decisions/153).
func TestAnimeSettingsShokoPackEpisodes(t *testing.T) {
	mock := &mockFileManager{configs: files.DefaultConfig()}
	server := &Server{State: daemon.NewState(), FileManager: mock}
	put := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/v1/animes/7/settings", strings.NewReader(body))
		req.SetPathValue("id", "7")
		handleAnimeSettings(server)(w, req)
		return w
	}
	if w := put(`{"shoko_pack_episodes":"remove"}`); w.Code != http.StatusOK || mock.animeSettings[7].ShokoPackEpisodes != files.ShokoPackRemove {
		t.Fatalf("remove = %d %+v", w.Code, mock.animeSettings[7])
	}
	if w := put(`{"progress":2}`); w.Code != http.StatusOK || mock.animeSettings[7].ShokoPackEpisodes != files.ShokoPackRemove {
		t.Fatal("a PUT without shoko_pack_episodes dropped it")
	}
	if w := put(`{"shoko_pack_episodes":"download"}`); w.Code != http.StatusOK || mock.animeSettings[7].ShokoPackEpisodes != files.ShokoPackDownload {
		t.Fatalf("download = %+v", mock.animeSettings[7])
	}
	if w := put(`{"shoko_pack_episodes":"default"}`); w.Code != http.StatusOK || mock.animeSettings[7].ShokoPackEpisodes != "" {
		t.Fatalf("default = %+v", mock.animeSettings[7])
	}
	if w := put(`{"shoko_pack_episodes":"track"}`); w.Code != http.StatusBadRequest {
		t.Errorf("an unknown value = %d, want 400", w.Code)
	}
}
