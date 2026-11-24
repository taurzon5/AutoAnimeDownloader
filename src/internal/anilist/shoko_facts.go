package anilist

import (
	"strconv"
	"time"
)

// MALFacts is what the Shoko match needs of an AniList media: its MAL id (0 when it has none) and
// its episode count (0 when AniList doesn't know it yet).
type MALFacts struct {
	MalID    int `json:"mal_id"`
	Episodes int `json:"episodes"`
}

const malFactsTTL = 24 * time.Hour

var (
	// mediaFactsCache maps an AniList media id to its MALFacts; malEpisodesCache a MAL id to AniList's
	// episode count for it (0 = unknown). Both only fill when Shoko is in use.
	mediaFactsCache  = newTTLCache[MALFacts]()
	malEpisodesCache = newTTLCache[int]()
)

// MALFactsOf returns the MAL id and episode count of each AniList media id, 50 per query, cached
// for a day. It is a query of its own, sent only when Shoko is in use, so the queries the pass and
// the golden set replay stay as they are (decisions/146). A media id AniList doesn't answer for is
// absent.
func MALFactsOf(mediaIDs []int, priority Priority) (map[int]MALFacts, error) {
	out := make(map[int]MALFacts, len(mediaIDs))
	var missing []int
	for _, id := range mediaIDs {
		if f, ok := mediaFactsCache.get(strconv.Itoa(id)); ok {
			out[id] = f
			continue
		}
		missing = append(missing, id)
	}
	query := `
		query ShokoMALFacts($ids: [Int]) {
			Page(perPage: ` + strconv.Itoa(anilistMaxPerPage) + `) {
				media(id_in: $ids, type: ANIME) { id idMal episodes }
			}
		}
	`
	for start := 0; start < len(missing); start += anilistMaxPerPage {
		chunk := missing[start:min(start+anilistMaxPerPage, len(missing))]
		resp, err := sendAnilistRequest[malFactsResponse](query, RequestVariables{"ids": chunk}, priority)
		if err != nil {
			return out, err
		}
		for _, m := range resp.Data.Page.Media {
			f := MALFacts{MalID: m.IdMal, Episodes: m.Episodes}
			out[m.Id] = f
			mediaFactsCache.set(strconv.Itoa(m.Id), f, malFactsTTL)
		}
	}
	return out, nil
}

// EpisodesOfMALIDs returns AniList's episode count for each MAL id it knows with a count, cached for
// a day: the Shoko match's offsets for a series that lists several MAL ids.
func EpisodesOfMALIDs(malIDs []int, priority Priority) (map[int]int, error) {
	out := make(map[int]int, len(malIDs))
	var missing []int
	for _, id := range malIDs {
		if n, ok := malEpisodesCache.get(strconv.Itoa(id)); ok {
			if n > 0 {
				out[id] = n
			}
			continue
		}
		missing = append(missing, id)
	}
	query := `
		query ShokoMALEpisodes($ids: [Int]) {
			Page(perPage: ` + strconv.Itoa(anilistMaxPerPage) + `) {
				media(idMal_in: $ids, type: ANIME) { id idMal episodes }
			}
		}
	`
	for start := 0; start < len(missing); start += anilistMaxPerPage {
		chunk := missing[start:min(start+anilistMaxPerPage, len(missing))]
		resp, err := sendAnilistRequest[malFactsResponse](query, RequestVariables{"ids": chunk}, priority)
		if err != nil {
			return out, err
		}
		seen := map[int]bool{}
		for _, m := range resp.Data.Page.Media {
			seen[m.IdMal] = true
			malEpisodesCache.set(strconv.Itoa(m.IdMal), m.Episodes, malFactsTTL)
			if m.Episodes > 0 {
				out[m.IdMal] = m.Episodes
			}
		}
		// idMal_in silently omits the ids AniList doesn't know: unknown, cached as such.
		for _, id := range chunk {
			if !seen[id] {
				malEpisodesCache.set(strconv.Itoa(id), 0, malFactsTTL)
			}
		}
	}
	return out, nil
}

type malFactsResponse struct {
	Data struct {
		Page struct {
			Media []struct {
				Id       int `json:"id"`
				IdMal    int `json:"idMal"`
				Episodes int `json:"episodes"`
			} `json:"media"`
		} `json:"Page"`
	} `json:"data"`
}
