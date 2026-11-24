package api

import (
	"AutoAnimeDownloader/src/internal/daemon"
	"AutoAnimeDownloader/src/internal/files"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func qbitConfig(t *testing.T) *files.Config {
	c := files.DefaultConfig()
	c.CompletedAnimePath = t.TempDir()
	c.LibraryMode = files.LibraryModeDirect
	c.TorrentClient = files.TorrentClientQBittorrent
	c.QBittorrentURL = "http://127.0.0.1:8080"
	c.QBittorrentUsername = "admin"
	c.QBittorrentPassword = "hunter2"
	c.QBittorrentAPIKey = "key-123"
	return c
}

func putConfig(t *testing.T, server *Server, c *files.Config) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(c)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	handleUpdateConfig(server)(w, req)
	return w
}

func TestGetConfig_MasksQBittorrentSecrets(t *testing.T) {
	saved := qbitConfig(t)
	server := &Server{State: daemon.NewState(), FileManager: &mockFileManager{configs: saved}}
	w := httptest.NewRecorder()
	handleGetConfig(server)(w, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))
	body := w.Body.String()
	if strings.Contains(body, "hunter2") || strings.Contains(body, "key-123") {
		t.Fatalf("GET /config leaked a qBittorrent secret: %s", body)
	}
	if !strings.Contains(body, files.SecretPlaceholder) || !strings.Contains(body, `"qbittorrent_username":"admin"`) {
		t.Fatalf("GET /config = %s, want the placeholder and the username", body)
	}
	if saved.QBittorrentPassword != "hunter2" {
		t.Fatal("masking changed the saved config")
	}
}

func TestUpdateConfig_PlaceholderKeepsTheSavedSecret(t *testing.T) {
	mock := &mockFileManager{configs: qbitConfig(t)}
	server := &Server{State: daemon.NewState(), FileManager: mock}

	edit := *mock.configs
	edit.QBittorrentPassword = files.SecretPlaceholder
	edit.QBittorrentAPIKey = files.SecretPlaceholder
	edit.CheckInterval = 15
	if w := putConfig(t, server, &edit); w.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	if mock.configs.QBittorrentPassword != "hunter2" || mock.configs.QBittorrentAPIKey != "key-123" {
		t.Fatalf("the placeholder overwrote the secrets: %q / %q", mock.configs.QBittorrentPassword, mock.configs.QBittorrentAPIKey)
	}

	edit = *mock.configs
	edit.QBittorrentPassword = "new-pass"
	edit.QBittorrentAPIKey = ""
	if w := putConfig(t, server, &edit); w.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	if mock.configs.QBittorrentPassword != "new-pass" || mock.configs.QBittorrentAPIKey != "" {
		t.Fatalf("a new password or a cleared key was not saved: %q / %q", mock.configs.QBittorrentPassword, mock.configs.QBittorrentAPIKey)
	}
}

func TestUpdateConfig_ValidatesTheTorrentClient(t *testing.T) {
	cases := []struct {
		name string
		edit func(c *files.Config)
		want string
	}{
		{"unknown client", func(c *files.Config) { c.TorrentClient = "deluge" }, `"builtin" or "qbittorrent"`},
		{"hardlink mode", func(c *files.Config) { c.LibraryMode = files.LibraryModeHardlink }, `library_mode to "direct"`},
		{"bad URL", func(c *files.Config) { c.QBittorrentURL = "localhost:8080" }, "http(s) address"},
		{"incomplete folder outside the map", func(c *files.Config) {
			c.IncompletePath = t.TempDir()
			c.QBittorrentPathMap = []files.PathMapping{{App: c.CompletedAnimePath, QBittorrent: "/data"}}
		}, "incomplete download folder is not under"},
		{"upload floor of 0", func(c *files.Config) { c.DisableSeeding, c.QBittorrentDisableSeedingUploadKiB = true, 0 }, "at least 1 KiB/s"},
		{"relative map", func(c *files.Config) {
			c.QBittorrentPathMap = []files.PathMapping{{App: "anime", QBittorrent: "/data"}}
		}, "two absolute folders"},
		{"nested map", func(c *files.Config) {
			c.QBittorrentPathMap = []files.PathMapping{{App: "/a", QBittorrent: "/x"}, {App: "/a/b", QBittorrent: "/y"}}
		}, "one inside another"},
		{"library outside the map", func(c *files.Config) {
			c.QBittorrentPathMap = []files.PathMapping{{App: "/elsewhere", QBittorrent: "/data"}}
		}, "not under any app folder"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := qbitConfig(t)
			tc.edit(c)
			server := &Server{State: daemon.NewState(), FileManager: &mockFileManager{configs: qbitConfig(t)}}
			w := putConfig(t, server, c)
			var resp struct {
				Error struct{ Message string } `json:"error"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &resp)
			if w.Code != http.StatusBadRequest || !strings.Contains(resp.Error.Message, tc.want) {
				t.Fatalf("PUT = %d %s, want 400 mentioning %q", w.Code, w.Body.String(), tc.want)
			}
		})
	}
}

// The incomplete folder works with qBittorrent through its per-torrent download path (decisions/145).
func TestUpdateConfig_IncompleteFolderIsAllowedWithQBittorrent(t *testing.T) {
	mock := &mockFileManager{configs: qbitConfig(t)}
	server := &Server{State: daemon.NewState(), FileManager: mock}
	edit := *mock.configs
	edit.IncompletePath = t.TempDir()
	if w := putConfig(t, server, &edit); w.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	if mock.configs.IncompletePath != edit.IncompletePath {
		t.Fatalf("incomplete_path saved as %q", mock.configs.IncompletePath)
	}
}

// Step 1 refused disable_seeding with qBittorrent; step 2 made it "almost no upload" (decisions/144).
func TestUpdateConfig_DisableSeedingIsAllowedWithQBittorrent(t *testing.T) {
	mock := &mockFileManager{configs: qbitConfig(t)}
	server := &Server{State: daemon.NewState(), FileManager: mock}
	edit := *mock.configs
	edit.DisableSeeding = true
	edit.QBittorrentManageSettings = true
	if w := putConfig(t, server, &edit); w.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	if !mock.configs.DisableSeeding || !mock.configs.QBittorrentManageSettings {
		t.Fatalf("saved disable_seeding=%v manage_settings=%v, want both on", mock.configs.DisableSeeding, mock.configs.QBittorrentManageSettings)
	}

	// The floor is only checked while it is used: a 0 left from config set doesn't block other saves.
	edit = *mock.configs
	edit.DisableSeeding = false
	edit.QBittorrentDisableSeedingUploadKiB = 0
	if w := putConfig(t, server, &edit); w.Code != http.StatusOK {
		t.Fatalf("PUT with disable_seeding off and a floor of 0 = %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateConfig_SwitchingClientsAsksForARestart(t *testing.T) {
	saved := qbitConfig(t)
	saved.TorrentClient = files.TorrentClientBuiltin
	server := &Server{State: daemon.NewState(), FileManager: &mockFileManager{configs: saved}}

	edit := *saved
	edit.TorrentClient = files.TorrentClientQBittorrent
	edit.QBittorrentPathMap = []files.PathMapping{{App: saved.CompletedAnimePath, QBittorrent: "/data"}}
	w := putConfig(t, server, &edit)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"restart_required":true`) {
		t.Fatalf("switching to qBittorrent = %d %s, want 200 with restart_required", w.Code, w.Body.String())
	}
	if w := putConfig(t, server, &edit); strings.Contains(w.Body.String(), "restart_required") {
		t.Fatalf("saving again without a client change asked for a restart: %s", w.Body.String())
	}
}

// fakeQBitWebAPI answers the calls the connection test makes.
func fakeQBitWebAPI(t *testing.T) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/api/v2/auth/login":
			if r.Form.Get("password") != "hunter2" {
				fmt.Fprint(w, "Fails.")
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "s", Path: "/"})
			fmt.Fprint(w, "Ok.")
		case "/api/v2/auth/logout":
		case "/api/v2/app/webapiVersion":
			fmt.Fprint(w, "2.11.3")
		case "/api/v2/app/version":
			fmt.Fprint(w, "v5.1.0")
		case "/api/v2/app/getDirectoryContent":
			entries, err := os.ReadDir(r.Form.Get("dirPath"))
			if err != nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			var out []string
			for _, e := range entries {
				// Like qBittorrent: its listing hides dotfiles.
				if strings.HasPrefix(e.Name(), ".") {
					continue
				}
				out = append(out, filepath.Join(r.Form.Get("dirPath"), e.Name()))
			}
			_ = json.NewEncoder(w).Encode(out)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func postTorrentClientTest(t *testing.T, server *Server, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrent-client/test", &buf)
	w := httptest.NewRecorder()
	handleTorrentClientTest(server)(w, req)
	return w
}

func TestTorrentClientTest(t *testing.T) {
	qb := fakeQBitWebAPI(t)
	saved := qbitConfig(t)
	saved.QBittorrentURL = qb.URL
	saved.QBittorrentAPIKey = ""
	server := &Server{State: daemon.NewState(), FileManager: &mockFileManager{configs: saved}}

	t.Run("saved settings", func(t *testing.T) {
		w := postTorrentClientTest(t, server, nil)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"library_visible":true`) {
			t.Fatalf("test with the saved settings = %d %s", w.Code, w.Body.String())
		}
	})
	t.Run("edited settings with the saved password", func(t *testing.T) {
		w := postTorrentClientTest(t, server, torrentClientTestRequest{
			CompletedAnimePath: saved.CompletedAnimePath, QBittorrentURL: qb.URL,
			QBittorrentUsername: "admin", QBittorrentPassword: files.SecretPlaceholder,
		})
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"webapi_version":"2.11.3"`) {
			t.Fatalf("test with the placeholder password = %d %s", w.Code, w.Body.String())
		}
	})
	t.Run("wrong password", func(t *testing.T) {
		w := postTorrentClientTest(t, server, torrentClientTestRequest{
			CompletedAnimePath: saved.CompletedAnimePath, QBittorrentURL: qb.URL, QBittorrentPassword: "nope",
		})
		if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "TORRENT_CLIENT_UNREACHABLE") {
			t.Fatalf("test with a wrong password = %d %s, want 502", w.Code, w.Body.String())
		}
	})
	t.Run("no URL", func(t *testing.T) {
		empty := qbitConfig(t)
		empty.QBittorrentURL = ""
		s := &Server{State: daemon.NewState(), FileManager: &mockFileManager{configs: empty}}
		if w := postTorrentClientTest(t, s, nil); w.Code != http.StatusBadRequest {
			t.Fatalf("test without a URL = %d, want 400", w.Code)
		}
	})
}

// The saved password belongs to the saved address: sending the placeholder with another address
// must not make the daemon sign in there with it (decisions/143).
func TestUpdateConfig_PlaceholderWithAnotherAddressIsRefused(t *testing.T) {
	mock := &mockFileManager{configs: qbitConfig(t)}
	server := &Server{State: daemon.NewState(), FileManager: mock}

	edit := *mock.configs
	edit.QBittorrentURL = "http://attacker.example:8080"
	edit.QBittorrentPassword = files.SecretPlaceholder
	edit.QBittorrentAPIKey = ""
	w := putConfig(t, server, &edit)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "type the password") {
		t.Fatalf("PUT with a new address and the placeholder = %d %s, want 400", w.Code, w.Body.String())
	}
	if mock.configs.QBittorrentURL != "http://127.0.0.1:8080" {
		t.Fatal("the refused PUT was saved")
	}

	// The same address with a trailing slash is the same address.
	edit = *mock.configs
	edit.QBittorrentURL = "http://127.0.0.1:8080/"
	edit.QBittorrentPassword = files.SecretPlaceholder
	edit.QBittorrentAPIKey = files.SecretPlaceholder
	if w := putConfig(t, server, &edit); w.Code != http.StatusOK {
		t.Fatalf("PUT with the same address = %d %s", w.Code, w.Body.String())
	}

	// A new address with the password typed again is fine.
	edit = *mock.configs
	edit.QBittorrentURL = "http://192.168.1.5:8080"
	edit.QBittorrentPassword = "typed-again"
	edit.QBittorrentAPIKey = ""
	if w := putConfig(t, server, &edit); w.Code != http.StatusOK || mock.configs.QBittorrentPassword != "typed-again" {
		t.Fatalf("PUT with a new address and a typed password = %d %s", w.Code, w.Body.String())
	}
}

func TestTorrentClientTest_NeverSendsTheSavedPasswordToAnotherAddress(t *testing.T) {
	requests := 0
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++ }))
	defer other.Close()
	saved := qbitConfig(t)
	server := &Server{State: daemon.NewState(), FileManager: &mockFileManager{configs: saved}}

	w := postTorrentClientTest(t, server, torrentClientTestRequest{
		CompletedAnimePath: saved.CompletedAnimePath, QBittorrentURL: other.URL,
		QBittorrentUsername: "admin", QBittorrentPassword: files.SecretPlaceholder,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("test against another address with the placeholder = %d %s, want 400", w.Code, w.Body.String())
	}
	if requests != 0 {
		t.Fatalf("the other address received %d requests", requests)
	}
}
