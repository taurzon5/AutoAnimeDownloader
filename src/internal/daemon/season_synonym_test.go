package daemon

import (
	"strings"
	"testing"

	"AutoAnimeDownloader/src/internal/anilist"
)

// BAKI-DOU Part 2 (AniList 210032): the titles say Part 2, and one synonym says "Season 2" for the
// same second cour. Read as season 2, it dropped every pack: Erai's "14 ~ 25" has no season and
// the S01 packs are season 1. A synonym's season equal to the titles' part, in a synonym with no
// part of its own, names the cour.
func TestExtractAnimeSeasonPart_ASynonymSeasonThatIsTheTitlesPart(t *testing.T) {
	titles := anilist.Title{Romaji: strPtr("BAKI-DOU Part 2"), English: strPtr("BAKI-DOU: The Invincible Samurai Part 2")}
	synonyms := []string{"BAKI-DOU: The Invincible Samurai Season 2", "BAKI-DOU: O Samurai Invencível Parte 2"}
	season, part := ExtractAnimeSeasonPart(titles, synonyms)
	if season != nil {
		t.Errorf("season = %d, want none: the synonym's Season 2 is the titles' Part 2", *season)
	}
	if part == nil || *part != 2 {
		t.Errorf("part = %v, want 2", part)
	}
}

// BEASTARS FINAL SEASON Part 2 (187267): the synonym "BEASTARS Season 3 Part 2" carries both, so
// its season 3 is a season and stays (the EMBER "(Season 3)" and VARYG "S03" packs need it).
func TestExtractAnimeSeasonPart_ASynonymWithSeasonAndPartKeepsTheSeason(t *testing.T) {
	titles := anilist.Title{Romaji: strPtr("BEASTARS FINAL SEASON Part 2"), English: strPtr("BEASTARS FINAL SEASON Part 2")}
	season, part := ExtractAnimeSeasonPart(titles, []string{"BEASTARS Season 3 Part 2"})
	if season == nil || *season != 3 || part == nil || *part != 2 {
		t.Errorf("season, part = %v, %v, want 3, 2", season, part)
	}
}

func bakiDouRows() string {
	return previewListingHTML(
		previewListingRow(1, "[Erai-raws] Baki-dou - 14 ~ 25 [1080p NF WEB-DL AVC EAC3][MultiSub] [BATCH]", "14.2 GiB", "80", "3"),
		previewListingRow(2, "[ToonsHub] BAKI-DOU The Invincible Samurai S01E14-E25 1080p NF WEB-DL DDP5.1 H.264 (BAKI-DOU Part 2, Multi-Subs) [BATCH]", "15.1 GiB", "60", "2"),
		previewListingRow(3, "[Erai-raws] Baki-dou - 01 ~ 13 [1080p NF WEB-DL AVC EAC3][MultiSub] [BATCH]", "15.0 GiB", "90", "1"),
	)
}

// The pack search keeps the season-less "14 ~ 25" once the season no longer comes from the synonym;
// coverage on the axis then decides between the packs (decisions/079).
func TestSearchNyaaForAnime_BakiDouPart2KeepsItsPacks(t *testing.T) {
	defer mockNyaaListing(t, bakiDouRows(), nil)()
	titles := anilist.Title{Romaji: strPtr("BAKI-DOU Part 2"), English: strPtr("BAKI-DOU: The Invincible Samurai Part 2")}
	results := searchNyaaForAnime(titles, []string{"BAKI-DOU: The Invincible Samurai Season 2"}, []int{1, 2, 3})
	found := false
	for _, r := range results {
		if strings.Contains(r.Name, "14 ~ 25") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the 14 ~ 25 pack was dropped: %v", torrentNames(results))
	}
}
