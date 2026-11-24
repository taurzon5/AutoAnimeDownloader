package shoko

// MatchStatus says how an AniList entry lines up with a Shoko series.
type MatchStatus string

const (
	// Matched: the entry's episode N is the series' episode N+Offset.
	Matched MatchStatus = "matched"
	// Partial: a series shares the entry's MAL id, but the episodes don't line up on their own.
	// The anime page asks to confirm the series and the offset; until then the anime is treated as
	// not in Shoko (the Shoko spec's answer 2).
	Partial MatchStatus = "partial"
	// NoMatch: no series shares the entry's MAL id.
	NoMatch MatchStatus = "none"
)

// Entry is what matching needs of an AniList entry.
type Entry struct {
	MalID int
	// Episodes is the entry's episode count; 0 when AniList doesn't know it yet (airing).
	Episodes int
}

// Override is a match confirmed by hand on the anime page.
type Override struct {
	SeriesID int `json:"series_id"`
	Offset   int `json:"offset"`
}

// Match is the outcome for one entry.
type Match struct {
	Status     MatchStatus `json:"status"`
	SeriesID   int         `json:"series_id,omitempty"`
	SeriesName string      `json:"series_name,omitempty"`
	Offset     int         `json:"offset,omitempty"`
	// ByHand: confirmed on the anime page rather than worked out.
	ByHand bool `json:"by_hand,omitempty"`
}

// MatchEntry finds the Shoko series of an AniList entry through the MAL id (decisions/146):
//   - one series lists the MAL id alone: matched at offset 0;
//   - it lists several MAL ids and the entry has the series' episode count: matched at offset 0. A
//     series' MAL ids are not only its cours (Frieren lists its shorts too, measured);
//   - otherwise the entry's episodes sit after those of the MAL ids listed before it: the offset is
//     their episode count, and it holds only when every listed MAL id's count is known and they add
//     up to the series' count. Anything else is Partial, to check by hand;
//   - two series listing the same MAL id are Partial too.
//
// malEpisodes gives the episode count of another MAL id (from AniList), false when unknown.
func MatchEntry(e Entry, series []Series, malEpisodes func(malID int) (int, bool)) Match {
	if e.MalID <= 0 {
		return Match{Status: NoMatch}
	}
	var found []Series
	for _, s := range series {
		for _, m := range s.MAL {
			if m == e.MalID {
				found = append(found, s)
				break
			}
		}
	}
	switch {
	case len(found) == 0:
		return Match{Status: NoMatch}
	case len(found) > 1:
		return Match{Status: Partial, SeriesID: found[0].ID, SeriesName: found[0].Name}
	}
	s := found[0]
	matched := Match{Status: Matched, SeriesID: s.ID, SeriesName: s.Name}
	if len(s.MAL) == 1 || (e.Episodes > 0 && e.Episodes == s.Episodes) {
		return matched
	}
	if e.Episodes == 0 || malEpisodes == nil {
		return Match{Status: Partial, SeriesID: s.ID, SeriesName: s.Name}
	}
	offset, total, before := 0, 0, true
	for _, m := range s.MAL {
		count := e.Episodes
		if m == e.MalID {
			before = false
		} else {
			n, ok := malEpisodes(m)
			if !ok || n <= 0 {
				return Match{Status: Partial, SeriesID: s.ID, SeriesName: s.Name}
			}
			count = n
			if before {
				offset += n
			}
		}
		total += count
	}
	if total != s.Episodes {
		return Match{Status: Partial, SeriesID: s.ID, SeriesName: s.Name}
	}
	matched.Offset = offset
	return matched
}

// OwnedOf maps a matched entry's episode numbers to the Shoko file that holds each, from the
// series' owned episodes. Only a Matched outcome owns anything: a Partial match counts as not in
// Shoko until it is confirmed.
func OwnedOf(m Match, e Entry, owned []Episode) map[int]File {
	if m.Status != Matched {
		return nil
	}
	out := map[int]File{}
	for _, ep := range owned {
		n := ep.Number - m.Offset
		if n < 1 || (e.Episodes > 0 && n > e.Episodes) || len(ep.Files) == 0 {
			continue
		}
		out[n] = ep.Files[0]
	}
	return out
}
