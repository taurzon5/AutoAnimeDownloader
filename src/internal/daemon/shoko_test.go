package daemon

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/shoko"
)

// shokoServer answers like Shoko v5.3.3 for one series, Frieren, with files for episodes 1 and 2.
func shokoServer(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("apikey") != "key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/Series":
			io.WriteString(w, `{"Total":1,"List":[{"IDs":{"ID":1,"AniDB":17617,"MAL":[52991,56885]},"Name":"Sousou no Frieren","Sizes":{"Total":{"Episodes":28}}}]}`)
		case "/api/v3/File":
			io.WriteString(w, `{"Total":0,"List":[]}`)
		case "/api/v3/Series/1/Episode":
			io.WriteString(w, `{"Total":2,"List":[`+
				`{"AniDB":{"Type":"Episode","EpisodeNumber":1},"Files":[{"ID":10,"Size":5,"Imported":"2026-11-01T00:00:00Z","Locations":[{"RelativePath":"Frieren/01.mkv","IsAccessible":true}]}]},`+
				`{"AniDB":{"Type":"Episode","EpisodeNumber":2},"Files":[{"ID":11,"Size":5,"Imported":"2026-11-01T00:00:00Z","Locations":[{"RelativePath":"Frieren/02.mkv","IsAccessible":true}]}]}]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func frierenAnime() anilist.MediaList {
	eps := 28
	return anilist.MediaList{Media: anilist.Media{Id: 154587, Episodes: &eps}}
}

func TestShokoServiceForPass(t *testing.T) {
	t.Cleanup(anilist.MockAniListDo(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"data":{"Page":{"media":[{"id":154587,"idMal":52991,"episodes":28}]}}}`))}, nil
	}))
	srv := shokoServer(t)
	svc := NewShokoService(t.TempDir())
	cfg := &files.Config{ShokoEnabled: true, ShokoURL: srv.URL, ShokoAPIKey: "key", ShokoRefreshHours: 24}
	animes := []anilist.MediaList{frierenAnime()}
	ctx := context.Background()
	now := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)

	owned := svc.ForPass(ctx, cfg, animes, nil, now)
	if f := owned[154587]; len(f) != 2 || f[1].Path != "Frieren/01.mkv" {
		t.Fatalf("owned = %+v, want episodes 1 and 2", owned)
	}
	st, ok := svc.AnimeStatus(154587)
	if !ok || st.Match.Status != shoko.Matched || st.Match.SeriesName != "Sousou no Frieren" || len(st.Owned) != 2 {
		t.Fatalf("status = %+v, %v", st, ok)
	}

	// "Not in Shoko", set by hand: nothing owned.
	none := map[int]files.AnimeSettings{154587: {Shoko: &files.ShokoMatch{None: true}}}
	if got := svc.ForPass(ctx, cfg, animes, none, now); len(got) != 0 {
		t.Fatalf("with the match set to none: %+v", got)
	}

	// Shoko down: the pass goes on with what was read last.
	srv.Close()
	svc.RequestFullSync()
	if got := svc.ForPass(ctx, cfg, animes, nil, now.Add(48*time.Hour)); len(got[154587]) != 2 {
		t.Fatalf("with Shoko unreachable: %+v, want the last sync's episodes", got)
	}

	// Off: nothing, and no status left over.
	cfg.ShokoEnabled = false
	if got := svc.ForPass(ctx, cfg, animes, nil, now); got != nil {
		t.Fatalf("with Shoko off: %+v", got)
	}
	if _, ok := svc.AnimeStatus(154587); ok {
		t.Fatal("a status survived turning Shoko off")
	}
	var nilSvc *ShokoService
	if nilSvc.ForPass(ctx, cfg, animes, nil, now) != nil {
		t.Fatal("a nil service returned episodes")
	}
}

func TestShokoMatchForByHand(t *testing.T) {
	series := []shoko.Series{{ID: 1, MAL: []int{52991}, Name: "Frieren", Episodes: 28}}
	m := shokoMatchFor(shoko.Entry{MalID: 999}, series, nil, &files.ShokoMatch{SeriesID: 1, Offset: 3})
	if m.Status != shoko.Matched || m.SeriesID != 1 || m.Offset != 3 || !m.ByHand || m.SeriesName != "Frieren" {
		t.Fatalf("a match set by hand = %+v", m)
	}
	if m := shokoMatchFor(shoko.Entry{MalID: 52991}, series, nil, nil); m.Status != shoko.Matched || m.ByHand {
		t.Fatalf("automatic = %+v", m)
	}
}
