package shoko

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeShoko is an in-memory Shoko v3 API that answers the way v5.3.3 was measured to, including
// where it is surprising: an episode has no AniDB block (so no number) without
// includeDataFrom=AniDB, type=Normal is ignored (credits come back), an unknown sortOrder is
// ignored (files stay in ID order), and a file names its series only with include=XRefs.
type fakeShoko struct {
	t      *testing.T
	server *httptest.Server

	mu       sync.Mutex
	user     string
	pass     string
	key      string
	series   []fakeSeries
	files    []fakeFile
	requests []string
	// ignoreType answers credits whatever the type filter, as a server that drops it would.
	ignoreType bool
}

type fakeSeries struct {
	Series
	// episodes holds each regular episode's file ids, by number; credits are added as noise.
	episodes map[int][]int
}

type fakeFile struct {
	File
	series       int
	unrecognized bool
}

func newFakeShoko(t *testing.T) *fakeShoko {
	f := &fakeShoko{t: t, user: "admin", pass: "pw", key: "key-1"}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeShoko) addSeries(s Series, episodes map[int][]int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.series = append(f.series, fakeSeries{Series: s, episodes: episodes})
}

// addFile adds an identified file of a series, imported at the given time.
func (f *fakeShoko) addFile(id, series, episode int, imported time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files = append(f.files, fakeFile{File: File{ID: id, Size: 100, Imported: imported, Path: fmt.Sprintf("Show/ep%02d.mkv", episode), Accessible: true}, series: series})
	for i := range f.series {
		if f.series[i].ID == series {
			if f.series[i].episodes == nil {
				f.series[i].episodes = map[int][]int{}
			}
			f.series[i].episodes[episode] = append(f.series[i].episodes[episode], id)
		}
	}
}

// removeFile deletes a file, as deleting it in Shoko does.
func (f *fakeShoko) removeFile(id int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, fl := range f.files {
		if fl.ID == id {
			f.files = append(f.files[:i], f.files[i+1:]...)
			break
		}
	}
	for i := range f.series {
		for n, ids := range f.series[i].episodes {
			var keep []int
			for _, fid := range ids {
				if fid != id {
					keep = append(keep, fid)
				}
			}
			if len(keep) == 0 {
				delete(f.series[i].episodes, n)
			} else {
				f.series[i].episodes[n] = keep
			}
		}
	}
}

func (f *fakeShoko) requestLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func (f *fakeShoko) file(id int) fakeFile {
	for _, fl := range f.files {
		if fl.ID == id {
			return fl
		}
	}
	return fakeFile{}
}

func apiFileJSON(fl fakeFile, xrefs bool) map[string]any {
	out := map[string]any{"ID": fl.ID, "Size": fl.Size, "Locations": []map[string]any{{"RelativePath": fl.Path, "IsAccessible": fl.Accessible}}}
	if !fl.unrecognized {
		out["Imported"] = fl.Imported
	} else {
		out["Imported"] = nil
	}
	if xrefs && !fl.unrecognized {
		out["SeriesIDs"] = []map[string]any{{"SeriesID": map[string]any{"ID": fl.series}}}
	} else {
		out["SeriesIDs"] = []any{}
	}
	return out
}

func seriesJSON(s fakeSeries) map[string]any {
	return map[string]any{"IDs": map[string]any{"ID": s.ID, "AniDB": s.AniDB, "MAL": s.MAL, "TopLevelGroup": s.TopLevelGroup},
		"Name": s.Name, "Sizes": map[string]any{"Total": map[string]any{"Episodes": s.Episodes}}}
}

func page[T any](items []T, q map[string][]string, def int) []T {
	size, _ := strconv.Atoi(first(q["pageSize"], strconv.Itoa(def)))
	pg, _ := strconv.Atoi(first(q["page"], "1"))
	if size == 0 {
		return items
	}
	start := (pg - 1) * size
	if start >= len(items) {
		return nil
	}
	end := min(start+size, len(items))
	return items[start:end]
}

func first(v []string, def string) string {
	if len(v) == 0 {
		return def
	}
	return v[0]
}

func (f *fakeShoko) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/api/auth" && r.Method == http.MethodPost {
		var body struct{ User, Pass, Device string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.User != f.user || body.Pass != f.pass || body.Device == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"apikey": f.key})
		return
	}
	if r.Header.Get("apikey") != f.key {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	q := r.URL.Query()
	path := strings.TrimPrefix(r.URL.Path, "/api/v3/")
	parts := strings.Split(path, "/")
	switch {
	case path == "Series":
		var list []map[string]any
		for _, s := range f.series {
			list = append(list, seriesJSON(s))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Total": len(list), "List": page(list, q, 100)})
	case len(parts) == 2 && parts[0] == "Series":
		id, _ := strconv.Atoi(parts[1])
		for _, s := range f.series {
			if s.ID == id {
				_ = json.NewEncoder(w).Encode(seriesJSON(s))
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	case len(parts) == 3 && parts[0] == "Series" && parts[2] == "Episode":
		id, _ := strconv.Atoi(parts[1])
		var list []map[string]any
		found := false
		for _, s := range f.series {
			if s.ID != id {
				continue
			}
			found = true
			var numbers []int
			for n := range s.episodes {
				numbers = append(numbers, n)
			}
			sort.Ints(numbers)
			add := func(typ string, n int, fileIDs []int) {
				e := map[string]any{"IDs": map[string]any{"ID": id*1000 + n}}
				if first(q["includeDataFrom"], "") == "AniDB" {
					e["AniDB"] = map[string]any{"Type": typ, "EpisodeNumber": n}
				}
				if q.Get("includeFiles") == "true" {
					var files []map[string]any
					for _, fid := range fileIDs {
						files = append(files, apiFileJSON(f.file(fid), false))
					}
					e["Files"] = files
				}
				list = append(list, e)
			}
			for _, n := range numbers {
				add("Episode", n, s.episodes[n])
			}
			// A credit with a file: only an unfiltered (or wrongly filtered) list carries it.
			if t := q.Get("type"); t != "Episode" || f.ignoreType {
				add("Credits", 1, s.episodes[numbers[0]])
			}
		}
		if !found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Total": len(list), "List": page(list, q, 100)})
	case path == "File":
		files := append([]fakeFile(nil), f.files...)
		if q.Get("exclude") != "Unrecognized" {
			files = append(files, fakeFile{File: File{ID: 9999, Size: 1, Path: "junk.mkv"}, unrecognized: true})
		}
		sort.Slice(files, func(i, j int) bool { return files[i].ID < files[j].ID })
		if q.Get("sortOrder") == "-ImportedAt" {
			sort.SliceStable(files, func(i, j int) bool {
				if files[i].unrecognized != files[j].unrecognized {
					return !files[i].unrecognized
				}
				return files[i].Imported.After(files[j].Imported)
			})
		}
		var list []map[string]any
		for _, fl := range files {
			list = append(list, apiFileJSON(fl, q.Get("include") == "XRefs"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Total": len(list), "List": page(list, q, 100)})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}
