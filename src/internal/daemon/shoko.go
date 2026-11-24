package daemon

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
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
	// The last pass's inputs, so a change Shoko reports can be matched again without a pass.
	lastAnimes   []anilist.MediaList
	lastFacts    map[int]anilist.MALFacts
	lastTracked  map[int]bool
	lastBySeries map[int]bool
	lastSettings map[int]files.AnimeSettings
	// The event watcher (Watch): whether it is connected, its last event and its last error.
	watching   bool
	lastEvent  time.Time
	watchError string
	// settle is how long the watcher waits after an event for more before refreshing: a season
	// imported at once is one refresh, not one per file. configCheck is how often it reads the
	// config, while connected and while waiting to.
	settle      time.Duration
	configCheck time.Duration
}

// WatchStatus is the event watcher's state, for GET /shoko/status.
type WatchStatus struct {
	Connected bool
	LastEvent time.Time
	Error     string
}

// ShokoAnimeStatus is how one anime lines up with Shoko: the match, and the AniList episode numbers
// Shoko has a file for.
type ShokoAnimeStatus struct {
	Match shoko.Match
	Owned map[int]shoko.File
}

// NewShokoService keeps its store (shoko.json) in dataDir.
func NewShokoService(dataDir string) *ShokoService {
	return &ShokoService{store: shoko.NewStore(dataDir), settle: 10 * time.Second, configCheck: 30 * time.Second}
}

// WatchState is the event watcher's state.
func (s *ShokoService) WatchState() WatchStatus {
	if s == nil {
		return WatchStatus{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return WatchStatus{Connected: s.watching, LastEvent: s.lastEvent, Error: s.watchError}
}

// Watch follows Shoko's changes as they happen, until ctx ends (decisions/148). While Shoko and
// shoko_watch are on, it holds a connection to Shoko's event hub:
//   - a file linked to an episode or deleted refreshes the series it names, once the events settle,
//     and the anime page's matches are worked out again from the last pass's anime;
//   - after every (re)connection it catches up on what happened while it was away (catchUp);
//   - it doesn't start a pass: an episode deleted in Shoko is downloaded by the next one.
//
// The config is read before every connection and every s.configCheck during one: turning it off,
// or signing in to another Shoko, closes the connection within that time.
func (s *ShokoService) Watch(ctx context.Context, loadConfig func() (*files.Config, error)) {
	if s == nil {
		return
	}
	usable := func(cfg *files.Config, err error) bool {
		return err == nil && cfg != nil && cfg.ShokoEnabled && cfg.ShokoWatch && cfg.ShokoURL != "" && cfg.ShokoAPIKey != ""
	}
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		cfg, err := loadConfig()
		if !usable(cfg, err) {
			s.setWatching(false, "")
			if !sleepCtx(ctx, s.configCheck) {
				return
			}
			continue
		}
		client, err := shoko.NewClient(cfg.ShokoURL, cfg.ShokoAPIKey)
		if err != nil {
			s.setWatching(false, err.Error())
			if !sleepCtx(ctx, s.configCheck) {
				return
			}
			continue
		}
		q := &watchQueue{pending: map[int]bool{}, kick: make(chan struct{}, 1)}
		connCtx, stop := context.WithCancel(ctx)
		var reconfigured atomic.Bool
		done := make(chan struct{})
		go s.settleAndRefresh(connCtx, client, cfg.ShokoURL, q, done)
		go func() {
			for sleepCtx(connCtx, s.configCheck) {
				now, err := loadConfig()
				if err == nil && (!usable(now, nil) || now.ShokoURL != cfg.ShokoURL || now.ShokoAPIKey != cfg.ShokoAPIKey) {
					reconfigured.Store(true)
					stop()
					return
				}
			}
		}()
		started := time.Now()
		err = client.Watch(connCtx,
			func() {
				s.setWatching(true, "")
				logger.Logger.Info().Msg("Following Shoko's changes")
				q.add(nil, true)
			},
			func(ch shoko.Change) {
				s.mu.Lock()
				s.lastEvent = time.Now()
				s.mu.Unlock()
				q.add(ch.Series, false)
			})
		stop()
		<-done
		if ctx.Err() != nil {
			return
		}
		if reconfigured.Load() {
			s.setWatching(false, "")
			continue
		}
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		s.setWatching(false, msg)
		if time.Since(started) > 5*time.Minute {
			backoff = 5 * time.Second
		}
		logger.Logger.Warn().Err(err).Dur("retry_in", backoff).Msg("Lost Shoko's event hub; reconnecting")
		if !sleepCtx(ctx, backoff) {
			return
		}
		backoff = min(backoff*2, 5*time.Minute)
	}
}

// watchQueue holds what the watcher still has to refresh: the series events named, and whether a
// catch-up is due. A set, not a channel: an event is never dropped, however big the burst.
type watchQueue struct {
	mu      sync.Mutex
	pending map[int]bool
	catchUp bool
	kick    chan struct{}
	takes   int // how many times the refresher took the queue (tests)
}

func (q *watchQueue) add(series []int, catchUp bool) {
	q.mu.Lock()
	for _, id := range series {
		q.pending[id] = true
	}
	q.catchUp = q.catchUp || catchUp
	q.mu.Unlock()
	select {
	case q.kick <- struct{}{}:
	default: // already kicked
	}
}

// keep puts series and the catch-up flag back without a kick (settleAndRefresh retries on its own
// timer).
func (q *watchQueue) keep(series []int, catchUp bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, id := range series {
		q.pending[id] = true
	}
	q.catchUp = q.catchUp || catchUp
}

func (q *watchQueue) take() ([]int, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.takes++
	ids := make([]int, 0, len(q.pending))
	for id := range q.pending {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	catchUp := q.catchUp
	q.pending, q.catchUp = map[int]bool{}, false
	return ids, catchUp
}

func (s *ShokoService) setWatching(connected bool, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.watching = connected
	s.watchError = errMsg
}

// settleAndRefresh refreshes what the queue holds: a catch-up at once, the series of events once no
// event came for s.settle. It closes done when ctx ends.
func (s *ShokoService) settleAndRefresh(ctx context.Context, client *shoko.Client, url string, q *watchQueue, done chan<- struct{}) {
	defer close(done)
	var timer <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-q.kick:
			q.mu.Lock()
			now := q.catchUp
			q.mu.Unlock()
			s.mu.Lock()
			settle := s.settle
			s.mu.Unlock()
			if now {
				settle = 0
			}
			timer = time.After(settle)
		case <-timer:
			timer = nil
			ids, catchUp := q.take()
			s.mu.Lock()
			tracked, bySeries := s.lastTracked, s.lastBySeries
			s.mu.Unlock()
			if tracked == nil {
				// No pass yet: nothing says which series are followed. Keep the events and try again
				// after the settle time, rather than drop them. Quietly, without a kick: a kick with
				// the catch-up flag fires at once, and the loop would spin until the first pass.
				if len(ids) > 0 || catchUp {
					q.keep(ids, catchUp)
					s.mu.Lock()
					settle := s.settle
					s.mu.Unlock()
					timer = time.After(settle)
				}
				continue
			}
			if catchUp {
				s.catchUp(ctx, client, url, ids, tracked, bySeries)
				continue
			}
			if err := s.store.RefreshSeries(ctx, client, url, ids, tracked, bySeries); err != nil {
				logger.Logger.Warn().Err(err).Msg("Shoko: failed to read the series of a change; the next sync reads it")
				continue
			}
			logger.Logger.Info().Int("count", len(ids)).Msg("Shoko changed; refreshed its series")
			s.rematch()
		}
	}
}

// catchUp runs after a (re)connection, for what changed while the watcher was away: the newer-only
// sync (files imported since), then the episodes of every series a followed anime matches read
// again, plus the series of events that came meanwhile. The second part is what sees a deletion or
// an unlink made while away, which the newer-only sync can't; it is one request per matched series,
// and reconnections are rare. Before the first pass it does nothing: the pass's first sync reads
// everything.
func (s *ShokoService) catchUp(ctx context.Context, client *shoko.Client, url string, extra []int, tracked, bySeries map[int]bool) {
	if tracked == nil || s.store.Due(url, 1<<62, time.Now()) {
		return
	}
	if err := s.store.Sync(ctx, client, url, tracked, bySeries, false, time.Now()); err != nil {
		logger.Logger.Warn().Err(err).Msg("Shoko: the catch-up sync failed; the next pass syncs")
		return
	}
	ids := append([]int(nil), extra...)
	for _, sr := range s.store.SeriesList() {
		if shokoTracks(sr, tracked) || bySeries[sr.ID] {
			ids = append(ids, sr.ID)
		}
	}
	if err := s.store.RefreshSeries(ctx, client, url, ids, tracked, bySeries); err != nil {
		logger.Logger.Warn().Err(err).Msg("Shoko: the catch-up could not read every followed series; the next full resync does")
	}
	s.rematch()
}

// rematch works out the matches again from the last pass's anime and the store, so the anime page
// shows a change Shoko reported without waiting for the next pass.
func (s *ShokoService) rematch() {
	s.mu.Lock()
	animes, facts, tracked, settings := s.lastAnimes, s.lastFacts, s.lastTracked, s.lastSettings
	s.mu.Unlock()
	if animes == nil {
		return
	}
	matches := s.matchAll(animes, facts, tracked, settings, anilist.PriorityDisposable)
	s.mu.Lock()
	s.matches = matches
	s.mu.Unlock()
}

// sleepCtx waits d, or less when ctx ends; false then.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
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
		// The last pass's inputs too: a late event must not bring the matches back (rematch).
		s.matches, s.lastAnimes, s.lastFacts, s.lastTracked, s.lastBySeries, s.lastSettings = nil, nil, nil, nil, nil, nil
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

	bySeries := map[int]bool{}
	for _, st := range settings {
		if st.Shoko != nil && st.Shoko.SeriesID > 0 {
			bySeries[st.Shoko.SeriesID] = true
		}
	}

	s.mu.Lock()
	full := s.fullRequested
	s.mu.Unlock()
	// Every shoko_full_resync_days, all of Shoko again: what the newer-only refresh and the events
	// never see (a file unlinked from an episode) stops counting (decisions/148).
	full = full || s.store.FullDue(cfg.ShokoFullResyncDays, now)
	hours := cfg.ShokoRefreshHours
	if hours < 1 {
		hours = 24 // a config.json from before the field, or one written by hand
	}
	every := time.Duration(hours) * time.Hour
	if full || s.store.Due(cfg.ShokoURL, every, now) {
		client, err := shoko.NewClient(cfg.ShokoURL, cfg.ShokoAPIKey)
		if err == nil {
			err = s.store.Sync(ctx, client, cfg.ShokoURL, tracked, bySeries, full, now)
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

	out := map[int]map[int]shoko.File{}
	matches := s.matchAll(animes, facts, tracked, settings, anilist.PriorityCritical)
	if s.readUnread(ctx, cfg, matches, tracked, bySeries) {
		matches = s.matchAll(animes, facts, tracked, settings, anilist.PriorityCritical)
	}
	s.mu.Lock()
	s.lastAnimes, s.lastFacts, s.lastTracked, s.lastBySeries, s.lastSettings = animes, facts, tracked, bySeries, settings
	s.mu.Unlock()
	for id, st := range matches {
		if len(st.Owned) > 0 {
			out[id] = st.Owned
		}
	}
	s.mu.Lock()
	s.matches = matches
	s.mu.Unlock()
	return out
}

// OwnedFor matches one anime against what the last sync read, without syncing: the add-anime path
// (DownloadStandaloneAnime), which runs outside the pass. A matched series no sync has read yet is
// read now (readUnread), since a newly added anime is exactly that case. nil with Shoko off.
func (s *ShokoService) OwnedFor(cfg *files.Config, anime anilist.MediaList, settings files.AnimeSettings) map[int]shoko.File {
	return s.ownedFor(cfg, anime, settings, true)
}

// OwnedFromLastSync is OwnedFor without asking Shoko anything and without writing the store: the
// debug run's view (a dry run, possibly in another process on the same data dir, decisions/058).
func (s *ShokoService) OwnedFromLastSync(cfg *files.Config, anime anilist.MediaList, settings files.AnimeSettings) map[int]shoko.File {
	return s.ownedFor(cfg, anime, settings, false)
}

func (s *ShokoService) ownedFor(cfg *files.Config, anime anilist.MediaList, settings files.AnimeSettings, readNew bool) map[int]shoko.File {
	if s == nil || !cfg.ShokoEnabled || cfg.ShokoURL == "" || cfg.ShokoAPIKey == "" {
		return nil
	}
	facts, err := anilist.MALFactsOf([]int{anime.Media.Id}, anilist.PriorityCritical)
	if err != nil {
		logger.Logger.Warn().Err(err).Int("anime_id", anime.Media.Id).Msg("Shoko: could not read the anime's MAL id; it is treated as not in Shoko")
	}
	tracked := map[int]bool{}
	if f := facts[anime.Media.Id]; f.MalID > 0 {
		tracked[f.MalID] = true
	}
	one := map[int]files.AnimeSettings{anime.Media.Id: settings}
	bySeries := map[int]bool{}
	if settings.Shoko != nil && settings.Shoko.SeriesID > 0 {
		bySeries[settings.Shoko.SeriesID] = true
	}
	matches := s.matchAll([]anilist.MediaList{anime}, facts, tracked, one, anilist.PriorityCritical)
	if readNew && s.readUnread(context.Background(), cfg, matches, tracked, bySeries) {
		matches = s.matchAll([]anilist.MediaList{anime}, facts, tracked, one, anilist.PriorityCritical)
	}
	return matches[anime.Media.Id].Owned
}

// readUnread reads the owned episodes of the matched series no sync has read yet, and reports
// whether it read any. A sync reads only the series followed at that time: an anime added to a list
// (or as a standalone) since would otherwise own nothing until the next refresh, up to a day, and
// the app would download what Shoko has meanwhile.
func (s *ShokoService) readUnread(ctx context.Context, cfg *files.Config, matches map[int]ShokoAnimeStatus, tracked, bySeries map[int]bool) bool {
	var ids []int
	for _, st := range matches {
		if st.Match.Status == shoko.Matched && st.Match.SeriesID > 0 && !s.store.HasOwned(st.Match.SeriesID) {
			ids = append(ids, st.Match.SeriesID)
		}
	}
	if len(ids) == 0 {
		return false
	}
	sort.Ints(ids)
	client, err := shoko.NewClient(cfg.ShokoURL, cfg.ShokoAPIKey)
	if err == nil {
		err = s.store.RefreshSeries(ctx, client, cfg.ShokoURL, ids, tracked, bySeries)
	}
	if err != nil {
		logger.Logger.Warn().Err(err).Int("count", len(ids)).Msg("Shoko: could not read the episodes of newly followed series; the next sync does")
	}
	return true
}

// matchAll matches each anime against the store's series and returns, by AniList media id, the match
// and the episodes Shoko has.
func (s *ShokoService) matchAll(animes []anilist.MediaList, facts map[int]anilist.MALFacts, tracked map[int]bool, settings map[int]files.AnimeSettings, priority anilist.Priority) map[int]ShokoAnimeStatus {
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
		var err error
		if counts, err = anilist.EpisodesOfMALIDs(others, priority); err != nil {
			logger.Logger.Warn().Err(err).Msg("Shoko: could not read the episode counts of every MAL id; their series wait to be checked by hand")
		}
	}
	malEpisodes := func(id int) (int, bool) { n, ok := counts[id]; return n, ok }

	matches := make(map[int]ShokoAnimeStatus, len(animes))
	for _, a := range animes {
		f := facts[a.Media.Id]
		entry := shoko.Entry{MalID: f.MalID, Episodes: f.Episodes}
		if a.Media.Episodes != nil && *a.Media.Episodes > 0 {
			entry.Episodes = *a.Media.Episodes
		}
		m := shokoMatchFor(entry, series, malEpisodes, settings[a.Media.Id].Shoko)
		matches[a.Media.Id] = ShokoAnimeStatus{Match: m, Owned: shoko.OwnedOf(m, entry, s.store.OwnedEpisodes(m.SeriesID))}
	}
	return matches
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
