package anilist

import (
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"AutoAnimeDownloader/src/internal/logger"
)

// FranchiseEntry is one anime of a franchise group: a TV season, a movie, an OVA, an ONA or a
// special. Episodes is 0 when AniList doesn't know the count yet (airing or unreleased); Year is 0
// when it has no start date.
type FranchiseEntry struct {
	ID       int         `json:"id"`
	Format   MediaFormat `json:"format"`
	Status   MediaStatus `json:"status"`
	Episodes int         `json:"episodes"`
	Year     int         `json:"year"`
	Romaji   string      `json:"romaji,omitempty"`
	English  string      `json:"english,omitempty"`
	Synonyms []string    `json:"synonyms,omitempty"`
}

// Title is the entry's display title: English first, then romaji (the daemon's order).
func (e FranchiseEntry) Title() string {
	if e.English != "" {
		return e.English
	}
	return e.Romaji
}

// Titles are every name the entry goes by, for matching folder and file names.
func (e FranchiseEntry) Titles() []string {
	out := make([]string, 0, 2+len(e.Synonyms))
	for _, t := range append([]string{e.Romaji, e.English}, e.Synonyms...) {
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

// Franchise is the group of anime one megapack can hold (megapack spec §2). Entries are sorted by
// year, then id, and include the seed.
type Franchise struct {
	SeedID  int              `json:"seed_id"`
	Entries []FranchiseEntry `json:"entries"`
}

// franchiseRelations are the edges a group follows. ADAPTATION, SOURCE, CHARACTER and OTHER lead
// to manga, novels and unrelated works; they are never walked.
var franchiseRelations = map[string]bool{
	"PREQUEL": true, "SEQUEL": true, "SIDE_STORY": true, "SPIN_OFF": true,
	"ALTERNATIVE": true, "PARENT": true, "SUMMARY": true,
}

// franchiseMaxEntries caps a group: a long-runner with its movies and specials is well under it
// (One Piece has about 40 anime entries), and a walk that reaches it has drifted.
const franchiseMaxEntries = 120

// franchiseMaxRounds caps the walk's requests: each round reads two levels of relations.
const franchiseMaxRounds = 8

// franchiseTTL keeps a group for a day: a new movie or season joins on the next day's walk. A group
// with an entry still airing, or without an episode count, keeps for franchiseOpenTTL: its count
// grows, and the series axis never caches such a node (decisions/077). Only the picker's mapping
// reads the group today, so an hour-old count only shifts that table (decisions/163).
const (
	franchiseTTL     = 24 * time.Hour
	franchiseOpenTTL = time.Hour
)

var franchiseCache = newTTLCache[Franchise]()

type franchiseNode struct {
	Id        int         `json:"id"`
	Type      string      `json:"type"`
	Format    MediaFormat `json:"format"`
	Status    MediaStatus `json:"status"`
	Episodes  *int        `json:"episodes"`
	Title     Title       `json:"title"`
	Synonyms  []string    `json:"synonyms"`
	StartDate struct {
		Year *int `json:"year"`
	} `json:"startDate"`
	Relations struct {
		Edges []struct {
			RelationType string        `json:"relationType"`
			Node         franchiseNode `json:"node"`
		} `json:"edges"`
	} `json:"relations"`
}

type franchiseResponse struct {
	Data struct {
		Page struct {
			Media []franchiseNode `json:"media"`
		} `json:"Page"`
	} `json:"data"`
}

const franchiseNodeFields = `id type format status episodes title { english romaji } synonyms startDate { year }`

// franchiseQuery reads two levels of relations, the most AniList expands (decisions/071): the
// third level's edges come back empty, so a level-2 node goes back to the frontier to be read
// again, as walkSeries does.
var franchiseQuery = `
	query GetFranchise($ids: [Int]) {
		Page(perPage: ` + strconv.Itoa(anilistMaxPerPage) + `) {
			media(id_in: $ids, type: ANIME) {
				` + franchiseNodeFields + `
				relations { edges { relationType node {
					` + franchiseNodeFields + `
					relations { edges { relationType node {
						` + franchiseNodeFields + `
					} } }
				} } }
			}
		}
	}
`

// GetFranchise walks seedID's franchise group: every anime reached through franchiseRelations
// whose name is similar to the seed's (decisions/163). Relations alone drift (a SPIN_OFF of a
// SPIN_OFF is another work), and names alone join unrelated works ("Monogatari" and
// "Ore Monogatari!!", decisions/071), so an entry needs both. A node that fails the name check is
// not walked from either.
//
// On an error the entries read so far come back with it, like GetSeriesIndex.
func GetFranchise(seedID int, priority Priority) (Franchise, error) {
	key := strconv.Itoa(seedID)
	if cached, ok := franchiseCache.get(key); ok {
		return cached, nil
	}

	entries := map[int]FranchiseEntry{}
	walked := map[int]bool{}
	frontier := []int{seedID}
	var seed *FranchiseEntry

	admit := func(n franchiseNode) bool {
		if n.Id == 0 || (n.Type != "" && n.Type != "ANIME") {
			return false
		}
		if _, ok := entries[n.Id]; ok {
			return true
		}
		e := franchiseEntryOf(n)
		if seed != nil && !SimilarFranchiseTitles(*seed, e) {
			return false
		}
		if len(entries) >= franchiseMaxEntries {
			return false
		}
		entries[n.Id] = e
		return true
	}

	var walkErr error
	for round := 0; len(frontier) > 0 && round < franchiseMaxRounds; round++ {
		batch := frontier
		frontier = nil
		for start := 0; start < len(batch); start += anilistMaxPerPage {
			chunk := batch[start:min(start+anilistMaxPerPage, len(batch))]
			for _, id := range chunk {
				walked[id] = true
			}
			resp, err := sendAnilistRequest[franchiseResponse](franchiseQuery, RequestVariables{"ids": chunk}, priority)
			if err != nil {
				logger.Logger.Warn().Err(err).Int("media_id", seedID).Msg("Failed to walk an AniList franchise group")
				walkErr = err
				break
			}
			for _, media := range resp.Data.Page.Media {
				if seed == nil && media.Id == seedID {
					e := franchiseEntryOf(media)
					seed = &e
				}
				if !admit(media) {
					continue
				}
				for _, edge := range media.Relations.Edges {
					if !franchiseRelations[edge.RelationType] || !admit(edge.Node) {
						continue
					}
					// Level 1 came with its own relations: read them here.
					for _, deeper := range edge.Node.Relations.Edges {
						if !franchiseRelations[deeper.RelationType] || !admit(deeper.Node) {
							continue
						}
						// Level 2's relations are cut: read it again next round.
						if !walked[deeper.Node.Id] && !slices.Contains(frontier, deeper.Node.Id) {
							frontier = append(frontier, deeper.Node.Id)
						}
					}
					walked[edge.Node.Id] = true
				}
			}
		}
		if walkErr != nil {
			break
		}
	}

	group := Franchise{SeedID: seedID}
	for _, e := range entries {
		group.Entries = append(group.Entries, e)
	}
	slices.SortFunc(group.Entries, func(a, b FranchiseEntry) int {
		if a.Year != b.Year {
			return a.Year - b.Year
		}
		return a.ID - b.ID
	})
	if walkErr == nil && seed != nil {
		ttl := franchiseTTL
		for _, e := range group.Entries {
			if e.Status != MediaStatusFinished || e.Episodes == 0 {
				ttl = franchiseOpenTTL
				break
			}
		}
		franchiseCache.set(key, group, ttl)
	}
	return group, walkErr
}

func franchiseEntryOf(n franchiseNode) FranchiseEntry {
	e := FranchiseEntry{ID: n.Id, Format: n.Format, Status: n.Status, Synonyms: n.Synonyms}
	if n.Episodes != nil {
		e.Episodes = *n.Episodes
	}
	if n.StartDate.Year != nil {
		e.Year = *n.StartDate.Year
	}
	if n.Title.English != nil {
		e.English = *n.Title.English
	}
	if n.Title.Romaji != nil {
		e.Romaji = *n.Title.Romaji
	}
	return e
}

// SimilarFranchiseTitles says whether two entries' names share a long enough run of letters: the
// whole of the shorter name ("Bleach" in "Bleach: Sennen Kessen-hen") or at least
// franchiseMinRun letters ("monogatari" in "Bakemonogatari" and "Nisemonogatari"). Romaji and
// English are compared; synonyms are not, since AniList's synonyms include other languages and
// nicknames that would join anything.
func SimilarFranchiseTitles(a, b FranchiseEntry) bool {
	for _, x := range []string{a.Romaji, a.English} {
		for _, y := range []string{b.Romaji, b.English} {
			if similarCompact(compactTitle(x), compactTitle(y)) {
				return true
			}
		}
	}
	return false
}

// franchiseMinRun is the shared run that makes two long names similar.
const franchiseMinRun = 8

func similarCompact(x, y string) bool {
	if x == "" || y == "" {
		return false
	}
	shorter := min(len(x), len(y))
	run := longestCommonRun(x, y)
	return run == shorter || run >= franchiseMinRun
}

// compactTitle keeps a title's lowercase ASCII letters and digits: "Kimetsu no Yaiba: Yuukaku-hen"
// → "kimetsunoyaibayuukakuhen".
func compactTitle(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// longestCommonRun is the length of the longest common substring of x and y (ASCII).
func longestCommonRun(x, y string) int {
	best := 0
	prev := make([]int, len(y)+1)
	cur := make([]int, len(y)+1)
	for i := 1; i <= len(x); i++ {
		for j := 1; j <= len(y); j++ {
			if x[i-1] == y[j-1] {
				cur[j] = prev[j-1] + 1
				if cur[j] > best {
					best = cur[j]
				}
			} else {
				cur[j] = 0
			}
		}
		prev, cur = cur, prev
	}
	return best
}
