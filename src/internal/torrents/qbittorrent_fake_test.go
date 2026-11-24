package torrents

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeQBit is an in-memory qBittorrent Web API 2.11+ for the backend's tests: login with a session
// cookie or an API key, the torrent table, files, renames (applied on disk too, as qBittorrent
// moves the file), and a log of every mutating call in order.
type fakeQBit struct {
	t      *testing.T
	server *httptest.Server

	mu         sync.Mutex
	user, pass string
	apiKey     string
	webAPI     string
	queueing   bool
	// modern answers like Web API 2.14+ (qBittorrent 5.1+): 204 for calls with nothing to say, a
	// refused login 401, torrents/add a JSON object (202 while a magnet is pending). Off, it answers
	// like 5.0: 200 with "Ok." / "Fails.".
	modern     bool
	sessions   map[string]bool
	categories map[string]bool
	torrents   map[string]*fakeTorrent
	// meta is the file list each hash gets when its metadata "arrives" (deliverMetadata).
	meta  map[string][]qbitFile
	calls []string
	rid   int64
}

type fakeTorrent struct {
	qbitTorrent
	stopAtMeta bool
	files      []qbitFile
}

func newFakeQBit(t *testing.T) *fakeQBit {
	f := &fakeQBit{
		t:          t,
		user:       "admin",
		pass:       "secret",
		webAPI:     "2.11.3",
		queueing:   true,
		sessions:   map[string]bool{},
		categories: map[string]bool{},
		torrents:   map[string]*fakeTorrent{},
		meta:       map[string][]qbitFile{},
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeQBit) record(format string, args ...any) {
	f.calls = append(f.calls, fmt.Sprintf(format, args...))
}

func (f *fakeQBit) callLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeQBit) authorized(r *http.Request) bool {
	if f.apiKey != "" && r.Header.Get("Authorization") == "Bearer "+f.apiKey {
		return true
	}
	if c, err := r.Cookie("QBT_SID_test"); err == nil && f.sessions[c.Value] {
		return true
	}
	return false
}

// expireSessions forgets every cookie session, as a qBittorrent restart does.
func (f *fakeQBit) expireSessions() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions = map[string]bool{}
}

func (f *fakeQBit) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_ = r.ParseForm()
	path := strings.TrimPrefix(r.URL.Path, "/api/v2/")
	if path == "auth/login" {
		if r.Form.Get("username") == f.user && r.Form.Get("password") == f.pass {
			sid := fmt.Sprintf("sid%d", len(f.sessions)+1)
			f.sessions[sid] = true
			http.SetCookie(w, &http.Cookie{Name: "QBT_SID_test", Value: sid, Path: "/"})
			if f.modern {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			fmt.Fprint(w, "Ok.")
			return
		}
		if f.modern {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, "Unauthorized")
			return
		}
		fmt.Fprint(w, "Fails.")
		return
	}
	if !f.authorized(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	hash := strings.ToLower(r.Form.Get("hash"))
	hashes := strings.Split(strings.ToLower(r.Form.Get("hashes")), "|")
	switch path {
	case "auth/logout":
	case "app/webapiVersion":
		fmt.Fprint(w, f.webAPI)
	case "app/version":
		fmt.Fprint(w, "v5.1.0")
	case "app/getDirectoryContent":
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
	case "torrents/createCategory":
		name := r.Form.Get("category")
		if f.categories[name] {
			w.WriteHeader(http.StatusConflict)
			return
		}
		f.categories[name] = true
		f.record("createCategory %s", name)
		f.noContent(w)
	case "torrents/setCategory":
		for _, h := range hashes {
			if t := f.torrents[h]; t != nil {
				t.Category = r.Form.Get("category")
			}
		}
		f.record("setCategory %s", r.Form.Get("category"))
		f.noContent(w)
	case "torrents/add":
		h, err := parseInfoHash(r.Form.Get("urls"))
		if err != nil {
			if f.modern {
				w.WriteHeader(http.StatusConflict)
				fmt.Fprint(w, `{"success_count":0,"pending_count":0,"failure_count":1,"added_torrent_ids":[]}`)
				return
			}
			fmt.Fprint(w, "Fails.")
			return
		}
		meta := false
		f.torrents[h] = &fakeTorrent{
			qbitTorrent: qbitTorrent{Hash: h, Name: h, State: "metaDL", Category: r.Form.Get("category"),
				SavePath: r.Form.Get("savepath"), HasMetadata: &meta, AddedOn: int64(len(f.torrents) + 1), ETA: qbitETAInfinity},
			stopAtMeta: r.Form.Get("stopCondition") == "MetadataReceived",
		}
		f.record("add %s savepath=%s stopAtMeta=%v useDownloadPath=%s", h, r.Form.Get("savepath"), f.torrents[h].stopAtMeta, r.Form.Get("useDownloadPath"))
		if f.modern {
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"success_count":0,"pending_count":1,"failure_count":0,"added_torrent_ids":[]}`)
			return
		}
		fmt.Fprint(w, "Ok.")
	case "torrents/info":
		var out []qbitTorrent
		for _, t := range f.sortedTorrents() {
			if len(hashes) > 0 && hashes[0] != "" && !fakeContains(hashes, t.Hash) {
				continue
			}
			if c := r.Form.Get("category"); c != "" && t.Category != c {
				continue
			}
			out = append(out, t.qbitTorrent)
		}
		_ = json.NewEncoder(w).Encode(out)
	case "sync/maindata":
		f.rid++
		table := map[string]qbitTorrent{}
		for h, t := range f.torrents {
			table[h] = t.qbitTorrent
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"rid": f.rid, "full_update": true, "torrents": table})
	case "torrents/files":
		t := f.torrents[hash]
		if t == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(t.files)
	case "torrents/renameFile":
		t := f.torrents[hash]
		if t == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		oldPath, newPath := r.Form.Get("oldPath"), r.Form.Get("newPath")
		for i := range t.files {
			if t.files[i].Name == oldPath {
				from := filepath.Join(t.SavePath, filepath.FromSlash(oldPath))
				to := filepath.Join(t.SavePath, filepath.FromSlash(newPath))
				if _, err := os.Stat(from); err == nil {
					_ = os.MkdirAll(filepath.Dir(to), 0755)
					_ = os.Rename(from, to)
				}
				t.files[i].Name = newPath
				f.record("rename %s %s -> %s", hash, oldPath, newPath)
				f.noContent(w)
				return
			}
		}
		w.WriteHeader(http.StatusConflict)
	case "torrents/start":
		for _, h := range hashes {
			if t := f.torrents[h]; t != nil {
				t.State = "downloading"
				if t.Progress >= 1 {
					t.State = "stalledUP"
				}
			}
		}
		f.record("start %s", strings.Join(hashes, ","))
		f.noContent(w)
	case "torrents/stop":
		for _, h := range hashes {
			if t := f.torrents[h]; t != nil {
				t.State = "stoppedDL"
			}
		}
		f.record("stop %s", strings.Join(hashes, ","))
		f.noContent(w)
	case "torrents/recheck":
		f.record("recheck %s", strings.Join(hashes, ","))
		f.noContent(w)
	case "torrents/reannounce":
		f.record("reannounce %s", strings.Join(hashes, ","))
		f.noContent(w)
	case "torrents/topPrio":
		if !f.queueing {
			w.WriteHeader(http.StatusConflict)
			return
		}
		f.record("topPrio %s", strings.Join(hashes, ","))
		f.noContent(w)
	case "torrents/delete":
		for _, h := range hashes {
			delete(f.torrents, h)
		}
		f.record("delete %s deleteFiles=%s", strings.Join(hashes, ","), r.Form.Get("deleteFiles"))
		f.noContent(w)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// noContent answers 204, as Web API 2.11.8+ does for a call with nothing to say.
func (f *fakeQBit) noContent(w http.ResponseWriter) {
	if f.modern {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (f *fakeQBit) sortedTorrents() []*fakeTorrent {
	var out []*fakeTorrent
	for _, t := range f.torrents {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Hash < out[j].Hash })
	return out
}

func fakeContains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// deliverMetadata gives the torrent its file list, as the swarm would: a torrent added with
// stopCondition=MetadataReceived stops there.
func (f *fakeQBit) deliverMetadata(hash string, files ...qbitFile) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.torrents[hash]
	meta := true
	t.HasMetadata = &meta
	t.files = files
	for _, file := range files {
		t.TotalSize += file.Size
	}
	t.Size = t.TotalSize
	t.State = "downloading"
	if t.stopAtMeta {
		t.State = "stoppedDL"
	}
}

// finish writes every file of the torrent where qBittorrent has it named and marks it seeding.
func (f *fakeQBit) finish(hash string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.torrents[hash]
	for _, file := range t.files {
		path := filepath.Join(t.SavePath, filepath.FromSlash(file.Name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(path, make([]byte, file.Size), 0644); err != nil {
			f.t.Fatal(err)
		}
	}
	t.Progress = 1
	t.Completed = t.Size
	t.State = "stalledUP"
}

func (f *fakeQBit) setState(hash, state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.torrents[hash].State = state
}

func (f *fakeQBit) torrent(hash string) fakeTorrent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *f.torrents[hash]
}

// put inserts a torrent directly, as if it had been in qBittorrent before the app connected.
func (f *fakeQBit) put(t qbitTorrent, files ...qbitFile) {
	f.mu.Lock()
	defer f.mu.Unlock()
	meta := true
	t.HasMetadata = &meta
	f.torrents[t.Hash] = &fakeTorrent{qbitTorrent: t, files: files}
}
