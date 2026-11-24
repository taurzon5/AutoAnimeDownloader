package daemon

import (
	"context"
	"sync"
	"time"

	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/logger"
	"AutoAnimeDownloader/src/internal/shoko"
)

// ShokoService is the daemon's half of the Shoko integration (the Shoko spec, step 1): the synced
// store, and on each pass, which episodes of each anime the user already has in Shoko.
type ShokoService struct {
	store *shoko.Store

	mu            sync.Mutex
	fullRequested bool
	// matches is the last pass's outcome by AniList media id, for the anime page.
	matches map[int]ShokoAnimeStatus
}

// ShokoAnimeStatus is how one anime lines up with Shoko: the match, and the AniList episode numbers
// Shoko has a file for.
type ShokoAnimeStatus struct {
	Match shoko.Match
	Owned map[int]shoko.File
}

// NewShokoService keeps its store (shoko.json) in dataDir.
func NewShokoService(dataDir string) *ShokoService {
	return &ShokoService{store: shoko.NewStore(dataDir)}
}

// Store is the synced state, for the status endpoint.
func (s *ShokoService) Store() *shoko.Store { return s.store }

// RequestFullSync makes the next pass read all of Shoko again (the Full resync button): a
// newer-only refresh can't see a file deleted or moved in Shoko.
func (s *ShokoService) RequestFullSync() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fullRequested = true
}

// AnimeStatus is one anime's outcome in the last pass; false when the pass didn't see it.
func (s *ShokoService) AnimeStatus(animeID int) (ShokoAnimeStatus, bool) {
	if s == nil {
		return ShokoAnimeStatus{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.matches[animeID]
	return st, ok
}

// ForPass runs once per pass, before the per-anime loop: it syncs with Shoko when the refresh is due
// (or a full resync was asked for), then matches every anime of the pass and returns, by AniList
// media id, the episodes Shoko has. A sync that fails doesn't fail the pass: it goes on with what
// was read last. Off (or not signed in), it returns nil and the pass is as without Shoko.
func (s *ShokoService) ForPass(ctx context.Context, cfg *files.Config, animes []anilist.MediaList, settings map[int]files.AnimeSettings, now time.Time) map[int]map[int]shoko.File {
	if s == nil {
		return nil
	}
	if !cfg.ShokoEnabled || cfg.ShokoURL == "" || cfg.ShokoAPIKey == "" {
		s.mu.Lock()
		s.matches = nil
		s.mu.Unlock()
		return nil
	}

	ids := make([]int, 0, len(animes))
	for _, a := range animes {
		ids = append(ids, a.Media.Id)
	}
	facts, err := anilist.MALFactsOf(ids, anilist.PriorityCritical)
	if err != nil {
		logger.Logger.Warn().Err(err).Msg("Shoko: could not read the MAL ids of every anime; those without one are treated as not in Shoko")
	}
	tracked := map[int]bool{}
	for _, f := range facts {
		if f.MalID > 0 {
			tracked[f.MalID] = true
		}
	}

	s.mu.Lock()
	full := s.fullRequested
	s.mu.Unlock()
	every := time.Duration(max(cfg.ShokoRefreshHours, 1)) * time.Hour
	if full || s.store.Due(cfg.ShokoURL, every, now) {
		client, err := shoko.NewClient(cfg.ShokoURL, cfg.ShokoAPIKey)
		if err == nil {
			err = s.store.Sync(ctx, client, cfg.ShokoURL, tracked, full, now)
		}
		if err != nil {
			logger.Logger.Warn().Err(err).Msg("Shoko sync failed; the pass uses what was read last")
		} else {
			s.mu.Lock()
			s.fullRequested = false
			s.mu.Unlock()
			logger.Logger.Info().Bool("full", full).Int("count", len(s.store.SeriesList())).Msg("Synced with Shoko")
		}
	}

	series := s.store.SeriesList()
	// The other MAL ids of the series that list several, for the offsets (decisions/146).
	var others []int
	for _, sr := range series {
		if len(sr.MAL) > 1 && shokoTracks(sr, tracked) {
			others = append(others, sr.MAL...)
		}
	}
	counts := map[int]int{}
	if len(others) > 0 {
		if counts, err = anilist.EpisodesOfMALIDs(others, anilist.PriorityCritical); err != nil {
			logger.Logger.Warn().Err(err).Msg("Shoko: could not read the episode counts of every MAL id; their series wait to be checked by hand")
		}
	}
	malEpisodes := func(id int) (int, bool) { n, ok := counts[id]; return n, ok }

	out := map[int]map[int]shoko.File{}
	matches := make(map[int]ShokoAnimeStatus, len(animes))
	for _, a := range animes {
		f := facts[a.Media.Id]
		entry := shoko.Entry{MalID: f.MalID, Episodes: f.Episodes}
		if a.Media.Episodes != nil && *a.Media.Episodes > 0 {
			entry.Episodes = *a.Media.Episodes
		}
		m := shokoMatchFor(entry, series, malEpisodes, settings[a.Media.Id].Shoko)
		owned := shoko.OwnedOf(m, entry, s.store.OwnedEpisodes(m.SeriesID))
		matches[a.Media.Id] = ShokoAnimeStatus{Match: m, Owned: owned}
		if len(owned) > 0 {
			out[a.Media.Id] = owned
		}
	}
	s.mu.Lock()
	s.matches = matches
	s.mu.Unlock()
	return out
}

// shokoMatchFor is the match set by hand when there is one, else the one worked out.
func shokoMatchFor(entry shoko.Entry, series []shoko.Series, malEpisodes func(int) (int, bool), byHand *files.ShokoMatch) shoko.Match {
	switch {
	case byHand != nil && byHand.None:
		return shoko.Match{Status: shoko.NoMatch, ByHand: true}
	case byHand != nil && byHand.SeriesID > 0:
		m := shoko.Match{Status: shoko.Matched, SeriesID: byHand.SeriesID, Offset: byHand.Offset, ByHand: true}
		for _, sr := range series {
			if sr.ID == byHand.SeriesID {
				m.SeriesName = sr.Name
			}
		}
		return m
	}
	return shoko.MatchEntry(entry, series, malEpisodes)
}

func shokoTracks(sr shoko.Series, tracked map[int]bool) bool {
	for _, m := range sr.MAL {
		if tracked[m] {
			return true
		}
	}
	return false
}
