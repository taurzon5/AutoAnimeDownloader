package unit

import (
	"slices"
	"testing"

	"AutoAnimeDownloader/src/internal/nyaa"
)

func TestMegapackSignals(t *testing.T) {
	cases := []struct {
		name string
		want []string
	}{
		{"[Trix] Kimetsu no Yaiba S01-05 [Dual Audio] (BD 1080p AV1) (Batch)", []string{nyaa.MegapackSignalSeasons}},
		{"[Anime Time] One Piece (0001-1071+Movies+Specials) [BD+CR] [1080p]", []string{nyaa.MegapackSignalExtras}},
		{"[Judas] JoJo no Kimyou na Bouken (Seasons 1-4 + OVAs) [BD 1080p]", []string{nyaa.MegapackSignalSeasons, nyaa.MegapackSignalExtras}},
		{"Haikyuu!! Complete Batch [1080p]", []string{nyaa.MegapackSignalComplete}},
		{"[Group] Show Complete Series [BD]", []string{nyaa.MegapackSignalComplete}},
		{"[Group] Show Season 2 Complete Batch [1080p]", nil},
		{"[SubsPlease] Frieren (01-28) (1080p) [Batch]", nil},
		{"[Erai-raws] Bleach - 001 ~ 366 [1080p][BATCH]", nil},
	}
	for _, tc := range cases {
		if got := nyaa.MegapackSignals(tc.name); !slices.Equal(got, tc.want) {
			t.Errorf("%q: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A file list is read from Nyaa once: the detail page writes it, and the pack range reads it from
// the cache folder after that (decisions/163).
func TestFileListCache_PackRangeReadsWhatTheDetailRead(t *testing.T) {
	nyaa.SetFileListCacheDir(t.TempDir())
	t.Cleanup(func() { nyaa.SetFileListCacheDir("") })
	var urls []string
	page := fileListHtml("Show S01-02", []string{"Show - S01E01.mkv", "Show - S02E01.mkv"})
	defer mockHttpGetPerURL(func(string) string { return page }, &urls)()

	detail, err := nyaa.FetchTorrentDetail(4242)
	if err != nil || len(detail.Files) != 2 {
		t.Fatalf("detail = %+v, %v", detail, err)
	}
	for range 2 {
		if _, ok := nyaa.PackFileRange(nyaa.ViewURL(4242), 0); !ok {
			t.Fatal("pack range from the cached list")
		}
	}
	if len(urls) != 1 {
		t.Errorf("view page fetched %d times, want once: %v", len(urls), urls)
	}
}

// An empty list is not kept: the next read asks Nyaa again.
func TestFileListCache_DoesNotKeepAnEmptyList(t *testing.T) {
	nyaa.SetFileListCacheDir(t.TempDir())
	t.Cleanup(func() { nyaa.SetFileListCacheDir("") })
	var urls []string
	page := `<!doctype html><html><body><div class="torrent-file-list panel-body"><ul></ul></div></body></html>`
	defer mockHttpGetPerURL(func(string) string { return page }, &urls)()
	for range 2 {
		nyaa.PackFileRange(nyaa.ViewURL(7), 0)
	}
	if len(urls) != 2 {
		t.Errorf("fetched %d times, want 2", len(urls))
	}
}
