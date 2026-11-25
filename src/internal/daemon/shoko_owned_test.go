package daemon

import (
	"sort"
	"testing"

	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/nyaa"
	"AutoAnimeDownloader/src/internal/shoko"
	"AutoAnimeDownloader/src/internal/torrents"
)

func downloadedNumbers(result animeProcessResult) []int {
	var out []int
	for _, ep := range result.newEpisodes {
		out = append(out, ep.EpisodeNumber)
	}
	sort.Ints(out)
	return out
}

// Episodes the user already has in Shoko are not downloaded, and they take no slot of
// max_episodes_per_anime: with 1–6 in Shoko and a limit of 12, the app downloads 7–18. An episode
// the app downloaded itself stays the app's (the Shoko spec §3).
func TestProcessAnimeEpisodes_EpisodesInShokoAreSkippedAndTakeNoSlot(t *testing.T) {
	anime := animeWithEpisodes(20, anilist.MediaStatusFinished, true, "")
	searcher := searcherFor(nil, multipleFor(20, 1<<30), nil, nil)
	owned := map[int]shoko.File{}
	for ep := 1; ep <= 6; ep++ {
		owned[ep] = shoko.File{ID: ep, Path: "Show/ep.mkv"}
	}

	result := processAnimeEpisodes(limitsConfig(), torrents.NewFakeBackend(), anime, nil, nil, nil, map[files.EpisodeKey]bool{}, searcher, nil, shokoOwned{episodes: owned})
	got := downloadedNumbers(result)
	if len(got) != 12 || got[0] != 7 || got[11] != 18 {
		t.Fatalf("downloaded %v, want 7–18: owned episodes skipped and taking no slot", got)
	}

	// Without Shoko, the same anime downloads 1–12.
	plain := downloadedNumbers(processAnimeEpisodes(limitsConfig(), torrents.NewFakeBackend(), anime, nil, nil, nil, map[files.EpisodeKey]bool{}, searcher, nil, shokoOwned{}))
	if len(plain) != 12 || plain[0] != 1 {
		t.Fatalf("without Shoko: %v, want 1–12", plain)
	}
}

func TestWithoutOwnedElsewhere_KeepsTheAppsOwnEpisodes(t *testing.T) {
	episodes := []anilist.AiringNode{{Episode: 1}, {Episode: 2}, {Episode: 3}}
	owned := map[int]shoko.File{1: {}, 2: {}}
	saved := map[files.EpisodeKey]bool{{AnimeID: 7, Episode: 2}: true}
	got := withoutOwnedElsewhere(episodes, owned, 7, saved)
	if len(got) != 2 || got[0].Episode != 2 || got[1].Episode != 3 {
		t.Fatalf("got %+v, want 2 (the app's own record) and 3", got)
	}
	if got := withoutOwnedElsewhere(episodes, nil, 7, saved); len(got) != 3 {
		t.Fatalf("with nothing in Shoko: %+v", got)
	}
}

// A pack chosen for the missing episodes downloads whole, so it brings the episodes in Shoko too.
// With tracking on they get records like the rest of the pack, carrying its hash and range; a
// watched one doesn't, as for any pack. Off, only the missing ones do, as before (decisions/149).
func TestProcessAnimeEpisodes_TrackedPackRecordsTheShokoEpisodesItBrings(t *testing.T) {
	anime := animeWithEpisodes(11, anilist.MediaStatusFinished, true, "")
	anime.Progress = 2
	owned := map[int]shoko.File{}
	for ep := 1; ep <= 5; ep++ {
		owned[ep] = shoko.File{ID: ep}
	}
	searcher := searcherFor([]nyaa.TorrentResult{{Name: "[X] Anime 01-11 [1080p]", MagnetLink: fakeMagnet(1)}}, nil, nil, nil)

	tracked := processAnimeEpisodes(limitsConfig(), torrents.NewFakeBackend(), anime, nil, nil, nil, map[files.EpisodeKey]bool{}, searcher, nil, shokoOwned{episodes: owned, trackInPacks: true})
	got := downloadedNumbers(tracked)
	if len(got) != 9 || got[0] != 3 || got[8] != 11 {
		t.Fatalf("tracked: recorded %v, want 3–11 (the unwatched episodes in Shoko the pack brings, then 6–11)", got)
	}
	hash := tracked.newEpisodes[0].EpisodeHash
	for _, ep := range tracked.newEpisodes {
		if ep.EpisodeHash != hash || !ep.IsBatch || ep.BatchStart != 1 || ep.BatchEnd != 11 {
			t.Errorf("episode %d: want the pack's record (hash %s, range 1-11), got %+v", ep.EpisodeNumber, hash, ep)
		}
	}

	left := downloadedNumbers(processAnimeEpisodes(limitsConfig(), torrents.NewFakeBackend(), anime, nil, nil, nil, map[files.EpisodeKey]bool{}, searcher, nil, shokoOwned{episodes: owned}))
	if len(left) != 6 || left[0] != 6 {
		t.Fatalf("left: recorded %v, want 6–11 only", left)
	}
}

// Only what a chosen pack covers joins, and only from a pack whose range is known. A pack of 06–11
// doesn't hold 1–5: adding them would also stop assignBatches at episode 1, and nothing would be
// downloaded at all. A pack of unknown range may not hold them either (decisions/149).
func TestProcessAnimeEpisodes_TrackedPackAddsOnlyWhatItCovers(t *testing.T) {
	anime := animeWithEpisodes(11, anilist.MediaStatusFinished, true, "")
	owned := map[int]shoko.File{}
	for ep := 1; ep <= 5; ep++ {
		owned[ep] = shoko.File{ID: ep}
	}
	track := shokoOwned{episodes: owned, trackInPacks: true}

	for _, tc := range []struct{ name, pack string }{
		{"a pack of the missing episodes only", "[X] Anime 06-11 [1080p]"},
		{"a pack of unknown range", "[X] Anime (Season 1+OVA) [Batch]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			searcher := searcherFor([]nyaa.TorrentResult{{Name: tc.pack, MagnetLink: fakeMagnet(3)}}, nil, nil, nil)
			got := downloadedNumbers(processAnimeEpisodes(limitsConfig(), torrents.NewFakeBackend(), anime, nil, nil, nil, map[files.EpisodeKey]bool{}, searcher, nil, track))
			if len(got) != 6 || got[0] != 6 || got[5] != 11 {
				t.Fatalf("recorded %v, want 6–11", got)
			}
		})
	}
}
