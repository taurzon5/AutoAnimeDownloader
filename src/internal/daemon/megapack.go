package daemon

import (
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/logger"
	"AutoAnimeDownloader/src/internal/nyaa"
)

// MegapackMapping is where every video file of a torrent goes among an anime's franchise group:
// the mapping table of the megapack spec §3. It decides nothing yet; the picker shows it.
type MegapackMapping struct {
	// Megapack is true when files land in two entries or more.
	Megapack    bool
	Entries     []MegapackEntry
	Unmapped    []MegapackFile
	TotalBytes  int64 // every file of the torrent, videos or not
	MappedBytes int64
}

// MegapackEntry is one franchise entry with the files mapped to it.
type MegapackEntry struct {
	ID            int
	Title         string
	Format        string
	Year          int
	EntryEpisodes int // AniList's count; 0 when unknown
	Episodes      []int
	Files         []MegapackFile
	Bytes         int64
	// Seed marks the anime the mapping was asked for.
	Seed bool
}

// MegapackFile is one video of the torrent. Episode is the entry's local number (1 for an
// unnumbered movie); Reason says why an unmapped file has no entry.
type MegapackFile struct {
	Name      string
	SizeBytes int64
	Episode   int
	Reason    string
}

// Why a video file has no entry: codes the CLI and the web UI word themselves.
const (
	UnmappedCreditless = "creditless"  // a creditless opening or ending, a menu, a promo
	UnmappedNoEntry    = "no_entry"    // a movie, OVA or special no entry of the group matches
	UnmappedPastSeries = "past_series" // a number past the last episode AniList lists
	UnmappedNoNumber   = "no_number"   // neither a number nor a name that places it
)

// MapMegapack places a torrent's files on franchise entries (megapack spec §3, decisions/163):
//
//   - a creditless opening or ending, a menu, a promotional video: unmapped, never guessed;
//   - a file that names itself or its folder a movie, OVA, ONA or special: the non-TV entry whose
//     title its names carry once the franchise's shared words are taken out ("One Piece Film Red"
//     → Film: Red, not Film: Gold), with the year as evidence ("JoJo OVAs [1993]");
//   - any other numbered file: its number on the series axis (decisions/077), with seasons that
//     restart their numbering laid end to end the way PackFileRange flattens them
//     (decisions/116), then the TV entry whose span holds it.
//
// series is GetSeriesIndex over the group's TV entries; an entry missing from it is left off the
// axis, so its files read as unmapped rather than as another season's.
func MapMegapack(files []nyaa.TorrentFile, group anilist.Franchise, series map[int]anilist.Series) MegapackMapping {
	out := MegapackMapping{}
	entries := make(map[int]*MegapackEntry, len(group.Entries))
	order := make([]int, 0, len(group.Entries))
	for _, e := range group.Entries {
		entries[e.ID] = &MegapackEntry{
			ID: e.ID, Title: e.Title(), Format: string(e.Format), Year: e.Year,
			EntryEpisodes: e.Episodes, Seed: e.ID == group.SeedID,
		}
		order = append(order, e.ID)
	}
	chain := seriesSpans(group, series)
	specials := newSpecialMatcher(group)

	type tvFile struct {
		file    nyaa.TorrentFile
		number  int
		season  int
		hasSeas bool
		key     string
	}
	var tv []tvFile
	place := func(id int, f nyaa.TorrentFile, episode int) {
		e := entries[id]
		e.Files = append(e.Files, MegapackFile{Name: f.Name, SizeBytes: f.SizeBytes, Episode: episode})
		e.Bytes += f.SizeBytes
		if !slices.Contains(e.Episodes, episode) {
			e.Episodes = append(e.Episodes, episode)
		}
		out.MappedBytes += f.SizeBytes
	}
	unmapped := func(f nyaa.TorrentFile, reason string) {
		out.Unmapped = append(out.Unmapped, MegapackFile{Name: f.Name, SizeBytes: f.SizeBytes, Reason: reason})
	}

	for _, f := range files {
		out.TotalBytes += f.SizeBytes
		base := path.Base(f.Name)
		if !nyaa.IsVideoFile(base) {
			continue
		}
		parent := path.Base(path.Dir(f.Name))
		if parent == "." {
			parent = ""
		}
		if nyaa.IsCreditlessOrMenu(parent, base) {
			unmapped(f, UnmappedCreditless)
			continue
		}
		inner := innerFolder(f.Name)
		number := nyaa.ExtractEpisodeNumber(base)
		marked := nyaa.IsSpecialMarked(inner, base, number != nil)
		if marked || number == nil {
			if id, ok := specials.match(inner, base); ok {
				episode := 1
				if number != nil && *number > 0 {
					episode = *number
				}
				place(id, f, episode)
				continue
			}
			if marked {
				unmapped(f, UnmappedNoEntry)
			} else {
				unmapped(f, UnmappedNoNumber)
			}
			continue
		}
		season, hasSeason := nyaa.FileSeason(parent, base)
		key := path.Dir(f.Name)
		if hasSeason {
			key = "s:" + strconv.Itoa(season)
		}
		tv = append(tv, tvFile{file: f, number: *number, season: season, hasSeas: hasSeason, key: key})
	}

	// Seasons that restart their numbering go end to end, in season order (folders without a season
	// number after, in the torrent's order); continuous numbering is the axis already.
	type span struct {
		lo, hi, season int
		hasSeason      bool
		first          int
	}
	spans := map[string]*span{}
	var keys []string
	for i, f := range tv {
		s, ok := spans[f.key]
		if !ok {
			spans[f.key] = &span{lo: f.number, hi: f.number, season: f.season, hasSeason: f.hasSeas, first: i}
			keys = append(keys, f.key)
			continue
		}
		s.lo, s.hi = min(s.lo, f.number), max(s.hi, f.number)
	}
	restart := false
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			a, b := spans[keys[i]], spans[keys[j]]
			if a.lo <= b.hi && b.lo <= a.hi {
				restart = true
			}
		}
	}
	shift := map[string]int{}
	if restart {
		slices.SortStableFunc(keys, func(x, y string) int {
			a, b := spans[x], spans[y]
			switch {
			case a.hasSeason && b.hasSeason:
				return a.season - b.season
			case a.hasSeason:
				return -1
			case b.hasSeason:
				return 1
			}
			return a.first - b.first
		})
		total := 0
		for _, k := range keys {
			s := spans[k]
			shift[k] = total - (s.lo - 1)
			total += s.hi - s.lo + 1
		}
	}

	for _, f := range tv {
		absolute := f.number + shift[f.key]
		if id, local, ok := chain.at(absolute); ok {
			place(id, f.file, local)
			continue
		}
		// A spin-off series or a TV special outside the main chain can still be named.
		if id, ok := specials.match(innerFolder(f.file.Name), path.Base(f.file.Name)); ok {
			place(id, f.file, f.number)
			continue
		}
		unmapped(f.file, UnmappedPastSeries)
	}

	used := 0
	for _, id := range order {
		e := entries[id]
		if len(e.Files) == 0 {
			continue
		}
		slices.Sort(e.Episodes)
		out.Entries = append(out.Entries, *e)
		used++
	}
	out.Megapack = used >= 2
	return out
}

// innerFolder is the folder a file sits in, or "" when that is the torrent's own top folder: a
// multi-file torrent always has one, named after the release ("Anime (S1 + OVA + Specials)"), and
// its words describe the whole torrent, not the file (decisions/113).
func innerFolder(name string) string {
	if strings.Count(name, "/") < 2 {
		return ""
	}
	return path.Base(path.Dir(name))
}

// seriesSpan is a TV entry's place on the main series axis: episodes (offset, offset+episodes].
type seriesSpan struct {
	id, offset, episodes int
}

type seriesAxis []seriesSpan

// at is the entry holding absolute episode n, and n's local number in it. The last entry is open
// ended when AniList doesn't know its count yet (an airing long-runner like One Piece).
func (a seriesAxis) at(n int) (int, int, bool) {
	for i, s := range a {
		local := n - s.offset
		if local < 1 {
			continue
		}
		if local <= s.episodes || (s.episodes == 0 && i == len(a)-1) {
			return s.id, local, true
		}
	}
	return 0, 0, false
}

// seriesSpans is the main TV chain of the group: the series the seed belongs to, else the one
// with the most episodes.
func seriesSpans(group anilist.Franchise, series map[int]anilist.Series) seriesAxis {
	byKey := map[int]seriesAxis{}
	for _, e := range group.Entries {
		if e.Format != anilist.MediaFormatTV && e.Format != anilist.MediaFormatTVShort {
			continue
		}
		s, ok := series[e.ID]
		if !ok {
			continue
		}
		byKey[s.Key] = append(byKey[s.Key], seriesSpan{id: e.ID, offset: s.Offset, episodes: e.Episodes})
	}
	mainKey, best := 0, -1
	if s, ok := series[group.SeedID]; ok {
		if _, has := byKey[s.Key]; has {
			mainKey, best = s.Key, 1<<30
		}
	}
	for key, axis := range byKey {
		total := 0
		for _, s := range axis {
			total += s.episodes
		}
		if total > best || (total == best && key < mainKey) {
			mainKey, best = key, total
		}
	}
	axis := byKey[mainKey]
	slices.SortFunc(axis, func(a, b seriesSpan) int { return a.offset - b.offset })
	return axis
}

// specialMatcher names the non-TV entry a movie, OVA or special file belongs to.
type specialMatcher struct {
	entries []specialEntry
	formats map[string]int // how many entries of each format word
}

type specialEntry struct {
	id     int
	year   int
	format string // "movie", "ova", "ona", "special", "tv"
	tokens map[string]bool
}

var reYear = regexp.MustCompile(`\b(19[5-9]\d|20[0-4]\d)\b`)

// genericTokens say nothing about which entry a file is: format words, release tags and the
// particles romaji titles share.
var genericTokens = map[string]bool{
	"the": true, "movie": true, "movies": true, "film": true, "films": true, "gekijouban": true,
	"ova": true, "ovas": true, "oad": true, "oads": true, "ona": true, "onas": true,
	"special": true, "specials": true, "sp": true, "season": true, "part": true, "ep": true,
	"episode": true, "no": true, "wa": true, "ga": true, "to": true, "de": true, "ni": true,
	"of": true, "and": true, "a": true, "bd": true, "bdrip": true, "web": true, "dl": true,
	"1080p": true, "720p": true, "480p": true, "2160p": true, "x264": true, "x265": true,
	"hevc": true, "avc": true, "aac": true, "flac": true, "dual": true, "audio": true,
	"10bit": true, "8bit": true, "mkv": true, "mp4": true, "new": true, "folder": true,
	"hen": true, "version": true, "tv": true,
}

var reWord = regexp.MustCompile(`[a-z0-9]+`)

func titleTokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range reWord.FindAllString(strings.ToLower(s), -1) {
		if genericTokens[w] || reYear.MatchString(w) {
			continue
		}
		// "Movie 01" names the same entry as "Movie 1".
		if n, err := strconv.Atoi(w); err == nil {
			w = strconv.Itoa(n)
		}
		out[w] = true
	}
	return out
}

func formatWord(f anilist.MediaFormat) string {
	switch f {
	case anilist.MediaFormatMovie:
		return "movie"
	case anilist.MediaFormatOVA:
		return "ova"
	case anilist.MediaFormatONA:
		return "ona"
	case anilist.MediaFormatSpecial:
		return "special"
	}
	return "tv"
}

// stemTokens are the words most of the group's titles share ("one", "piece"): they tell the
// franchise, not the entry.
func stemTokens(group anilist.Franchise) map[string]bool {
	counts := map[string]int{}
	for _, e := range group.Entries {
		seen := map[string]bool{}
		for _, t := range []string{e.Romaji, e.English} {
			for w := range titleTokens(t) {
				seen[w] = true
			}
		}
		for w := range seen {
			counts[w]++
		}
	}
	stem := map[string]bool{}
	for w, n := range counts {
		if len(group.Entries) > 1 && n*2 > len(group.Entries) {
			stem[w] = true
		}
	}
	return stem
}

func newSpecialMatcher(group anilist.Franchise) specialMatcher {
	stem := stemTokens(group)
	m := specialMatcher{formats: map[string]int{}}
	for _, e := range group.Entries {
		tokens := map[string]bool{}
		for _, t := range e.Titles() {
			for w := range titleTokens(t) {
				if !stem[w] {
					tokens[w] = true
				}
			}
		}
		f := formatWord(e.Format)
		m.formats[f]++
		m.entries = append(m.entries, specialEntry{id: e.ID, year: e.Year, format: f, tokens: tokens})
	}
	return m
}

var formatWords = map[string]*regexp.Regexp{
	"movie":   regexp.MustCompile(`(?i)\b(?:movies?|films?|gekijouban)\b`),
	"ova":     regexp.MustCompile(`(?i)\b(?:ovas?|oads?)\b`),
	"ona":     regexp.MustCompile(`(?i)\bonas?\b`),
	"special": regexp.MustCompile(`(?i)\b(?:specials?|sp\d{0,3})\b`),
}

// match scores every non-TV entry against the folder and the file name and keeps the best one
// above half, when no other entry ties it. Evidence: the entry's own title words in the names
// (the larger share wins), the entry's year in a name, and the format word when the group has a
// single entry of that format.
func (m specialMatcher) match(parent, base string) (int, bool) {
	text := strings.ReplaceAll(parent+" "+strings.TrimSuffix(base, path.Ext(base)), "_", " ")
	have := titleTokens(text)
	years := map[int]bool{}
	for _, y := range reYear.FindAllString(text, -1) {
		n, _ := strconv.Atoi(y)
		years[n] = true
	}
	bestID, best, tie := 0, 0.0, false
	for _, e := range m.entries {
		if e.format == "tv" {
			continue
		}
		score := 0.0
		if len(e.tokens) > 0 {
			hit := 0
			for w := range e.tokens {
				if have[w] {
					hit++
				}
			}
			score = float64(hit) / float64(len(e.tokens))
		}
		if e.year > 0 && years[e.year] {
			score += 0.5
		}
		if re := formatWords[e.format]; re != nil && re.MatchString(text) {
			if m.formats[e.format] == 1 {
				score += 0.6
			} else {
				score += 0.1
			}
		}
		switch {
		case score > best:
			bestID, best, tie = e.id, score, false
		case score == best && score > 0:
			tie = true
		}
	}
	if best < 0.5 || tie {
		return 0, false
	}
	return bestID, true
}

// MapTorrentFiles maps a torrent's files onto mediaID's franchise group: the picker's mapping
// table. AniList is asked for the group and the series axis of its TV entries, both cached; a
// failed series read leaves the TV entries off the axis instead of failing the mapping.
func MapTorrentFiles(mediaID int, files []nyaa.TorrentFile) (MegapackMapping, error) {
	group, err := anilist.GetFranchise(mediaID, anilist.PriorityCritical)
	if err != nil && len(group.Entries) == 0 {
		return MegapackMapping{}, err
	}
	var tvIDs []int
	for _, e := range group.Entries {
		if e.Format == anilist.MediaFormatTV || e.Format == anilist.MediaFormatTVShort {
			tvIDs = append(tvIDs, e.ID)
		}
	}
	series, serr := anilist.GetSeriesIndex(tvIDs, anilist.PriorityCritical)
	if serr != nil {
		logger.Logger.Warn().Err(serr).Int("media_id", mediaID).Msg("Series axis incomplete for a megapack mapping")
	}
	return MapMegapack(files, group, series), nil
}
