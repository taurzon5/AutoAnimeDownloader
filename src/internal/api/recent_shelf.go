package api

import (
	"slices"
	"time"

	"AutoAnimeDownloader/src/internal/files"
)

// AnimeRecent is an anime's card on the Recents shelf: the episodes downloaded inside the window
// and still unwatched, collapsed into runs, and when the last of them was downloaded.
type AnimeRecent struct {
	Ranges []EpisodeRange `json:"ranges"`
	// IsBatch: every qualifying episode came from one torrent, and there are two or more of them.
	// A single leftover of a pack is a plain episode.
	IsBatch      bool      `json:"is_batch"`
	DownloadedAt time.Time `json:"downloaded_at"`
}

// EpisodeRange is an inclusive run of episode numbers.
type EpisodeRange struct {
	From int `json:"from"`
	To   int `json:"to"`
}

// recentShelf builds the anime's Recents card from its saved episodes: those numbered above the
// watch progress and downloaded in the last days days. Watching is the only dismissal: there is no
// "seen on the shelf" state. nil when none qualifies, or when days is 0 (the shelf is off).
func recentShelf(saved []files.EpisodeStruct, watched, days int, now time.Time) *AnimeRecent {
	if days <= 0 {
		return nil
	}
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
	var nums []int
	hashes := map[string]bool{}
	var latest time.Time
	for _, ep := range saved {
		if ep.EpisodeNumber <= 0 || ep.EpisodeNumber <= watched || ep.DownloadDate.Before(cutoff) {
			continue
		}
		if !slices.Contains(nums, ep.EpisodeNumber) {
			nums = append(nums, ep.EpisodeNumber)
		}
		hashes[ep.EpisodeHash] = true
		if ep.DownloadDate.After(latest) {
			latest = ep.DownloadDate
		}
	}
	if len(nums) == 0 {
		return nil
	}
	slices.Sort(nums)
	var ranges []EpisodeRange
	for _, n := range nums {
		if last := len(ranges) - 1; last >= 0 && ranges[last].To == n-1 {
			ranges[last].To = n
			continue
		}
		ranges = append(ranges, EpisodeRange{From: n, To: n})
	}
	return &AnimeRecent{
		Ranges:       ranges,
		IsBatch:      len(nums) >= 2 && len(hashes) == 1 && !hashes[""],
		DownloadedAt: latest.UTC(),
	}
}
