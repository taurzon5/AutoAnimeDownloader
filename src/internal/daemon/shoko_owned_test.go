package daemon

import (
	"sort"
	"testing"

	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/files"
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

	result := processAnimeEpisodes(limitsConfig(), torrents.NewFakeBackend(), anime, nil, nil, nil, map[files.EpisodeKey]bool{}, searcher, nil, owned)
	got := downloadedNumbers(result)
	if len(got) != 12 || got[0] != 7 || got[11] != 18 {
		t.Fatalf("downloaded %v, want 7–18: owned episodes skipped and taking no slot", got)
	}

	// Without Shoko, the same anime downloads 1–12.
	plain := downloadedNumbers(processAnimeEpisodes(limitsConfig(), torrents.NewFakeBackend(), anime, nil, nil, nil, map[files.EpisodeKey]bool{}, searcher, nil, nil))
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
