package daemon

import (
	"path/filepath"
	"testing"

	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/nyaa"
	"AutoAnimeDownloader/src/internal/torrents"
)

// useReleaseStore points the package's release store at a temporary file for one test.
func useReleaseStore(t *testing.T) *files.ReleaseStore {
	t.Helper()
	s := files.NewReleaseStore(filepath.Join(t.TempDir(), ReleasesFileName))
	SetReleaseStore(s)
	t.Cleanup(func() { SetReleaseStore(nil) })
	return s
}

// The pass records the release it added: one record for a pack, with the Nyaa row's details, the
// name parsed, and every episode the pack was added for.
func TestProcessAnimeEpisodesRecordsTheRelease(t *testing.T) {
	store := useReleaseStore(t)
	anime := animeWithEpisodes(11, anilist.MediaStatusFinished, true, "")
	anime.Progress = 5
	pack := nyaa.TorrentResult{Name: "[Erai-raws] Anime 01-11 [1080p CR WEB-DL AVC AAC][MultiSub]", MagnetLink: fakeMagnet(1), Seeders: "42", Leechers: 3, Size: 9 << 30, DetailURL: "https://nyaa.si/view/1", Trusted: true}
	searcher := searcherFor([]nyaa.TorrentResult{pack}, nil, nil, nil)

	result := processAnimeEpisodes(limitsConfig(), torrents.NewFakeBackend(), anime, nil, nil, nil, map[files.EpisodeKey]bool{}, searcher, nil, shokoOwned{})
	if len(result.newEpisodes) != 6 {
		t.Fatalf("want 6 episodes recorded, got %d", len(result.newEpisodes))
	}
	all, err := store.All()
	if err != nil || len(all) != 1 {
		t.Fatalf("want one release record, got %v %v", all, err)
	}
	rec := all[result.newEpisodes[0].EpisodeHash]
	if rec.Name != pack.Name || rec.Origin != files.ReleaseOriginSearch || rec.Seeders != 42 || rec.Leechers != 3 ||
		rec.Size != pack.Size || rec.DetailURL != pack.DetailURL || !rec.Trusted || rec.AddedAt.IsZero() {
		t.Errorf("record %+v", rec)
	}
	if rec.Release.Group != "erai-raws" || rec.Release.VideoCodec != "h.264" || rec.Release.Service != "cr" {
		t.Errorf("parsed release %+v", rec.Release)
	}
	if got := rec.Episodes[anime.Media.Id]; len(got) != 6 || got[0] != 6 || got[5] != 11 {
		t.Errorf("episodes %v, want 6–11", got)
	}
}

// The picker and a pasted magnet share the add paths: a Nyaa id is what makes a pick, with its Nyaa
// page; without one it is a pasted magnet named by its dn.
func TestPickedReleaseRecordsAPickOrAPaste(t *testing.T) {
	store := useReleaseStore(t)
	magnet := "magnet:?xt=urn:btih:abc&dn=%5BX%5D+Show+01-12+%5B1080p%5D"
	PickedRelease{Name: "[X] Show 01-12 [1080p]", NyaaID: 99, SizeBytes: 5 << 30}.record("pick", magnet, 7, 1, 2)
	PickedRelease{}.record("paste", magnet, 7, 3)

	all, err := store.All()
	if err != nil {
		t.Fatal(err)
	}
	if p := all["pick"]; p.Origin != files.ReleaseOriginPicker || p.DetailURL != nyaa.ViewURL(99) || p.Size != 5<<30 || p.Name != "[X] Show 01-12 [1080p]" {
		t.Errorf("pick %+v", p)
	}
	if p := all["paste"]; p.Origin != files.ReleaseOriginMagnet || p.DetailURL != "" || p.Name != "[X] Show 01-12 [1080p]" {
		t.Errorf("paste %+v", p)
	}
}
