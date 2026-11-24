package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"AutoAnimeDownloader/src/internal/daemon"
	"AutoAnimeDownloader/src/internal/files"
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
