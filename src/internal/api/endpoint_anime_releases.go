package api

import (
	"cmp"
	"net/http"
	"slices"
	"strconv"
	"time"

	"AutoAnimeDownloader/src/internal/daemon"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/logger"
	"AutoAnimeDownloader/src/internal/nyaa"
	"AutoAnimeDownloader/src/internal/torrents"
)

// The states of a torrent card (AnimeReleaseCard.State).
const (
	ReleaseStateDownloading = "downloading"
	ReleaseStateQueued      = "queued"
	ReleaseStateSeeding     = "seeding"
	ReleaseStateStopped     = "stopped"
	// ReleaseStateReleased: disable_seeding took the torrent out of the client, its files stay in
	// the library (decisions/119).
	ReleaseStateReleased = "released"
	// ReleaseStateMissing: episodes still point at it, but the client doesn't have it.
	ReleaseStateMissing = "missing"
	// ReleaseStateRemoved: no episode points at it and the client doesn't have it; the card stays
	// while keep_removed_release_cards is on.
	ReleaseStateRemoved = "removed"
)

// AnimeReleaseCard is one torrent that brought this anime's episodes: the release it was, how it
// came, its tags, and its state now (the release-filters spec §9). Recorded false: the torrent was
// added before release details were kept, and the card has only what the client knows.
type AnimeReleaseCard struct {
	Hash       string       `json:"hash"`
	Name       string       `json:"name"`
	DetailURL  string       `json:"detail_url,omitempty"`
	Episodes   []int        `json:"episodes"`
	Origin     string       `json:"origin,omitempty"`
	AddedAt    *time.Time   `json:"added_at,omitempty"`
	UploadedAt *time.Time   `json:"uploaded_at,omitempty"`
	Size       int64        `json:"size,omitempty"`
	Seeders    int          `json:"seeders,omitempty"`
	Leechers   int          `json:"leechers,omitempty"`
	Trusted    bool         `json:"trusted,omitempty"`
	Remake     bool         `json:"remake,omitempty"`
	Release    nyaa.Release `json:"release"`
	State      string       `json:"state"`
	Progress   float64      `json:"progress"`
	Recorded   bool         `json:"recorded"`
}

func handleAnimeReleases(server *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			JSONError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only GET method is allowed")
			return
		}
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			JSONError(w, http.StatusBadRequest, "INVALID_ID", "Invalid anime ID")
			return
		}
		config, err := server.FileManager.LoadConfigs()
		if err != nil {
			JSONInternalError(w, err)
			return
		}
		saved, err := server.FileManager.LoadSavedEpisodes()
		if err != nil {
			JSONInternalError(w, err)
			return
		}
		records, err := daemon.ReleaseStore().All()
		if err != nil {
			// The cards still come, from what the client knows.
			logger.Logger.Warn().Err(err).Msg("Could not read the release store")
			records = map[string]files.ReleaseRecord{}
		}
		var live []torrents.TorrentInfo
		if server.Torrents != nil {
			live = server.Torrents.List()
		}
		JSONSuccess(w, http.StatusOK, animeReleaseCards(id, saved, records, live, config.KeepRemovedReleaseCards))
	}
}

// animeReleaseCards builds the cards: one per torrent the anime's episode records point at, then,
// when keepRemoved, one per recorded torrent of this anime that nothing points at any more.
func animeReleaseCards(animeID int, saved []files.EpisodeStruct, records map[string]files.ReleaseRecord, live []torrents.TorrentInfo, keepRemoved bool) []AnimeReleaseCard {
	byHash := map[string]torrents.TorrentInfo{}
	for _, t := range live {
		byHash[t.Hash] = t
	}
	type pointed struct {
		episodes []int
		released bool
		name     string
	}
	pointers := map[string]*pointed{}
	var order []string
	for _, ep := range saved {
		if ep.AnimeID != animeID || ep.EpisodeHash == "" {
			continue
		}
		p := pointers[ep.EpisodeHash]
		if p == nil {
			p = &pointed{}
			pointers[ep.EpisodeHash] = p
			order = append(order, ep.EpisodeHash)
		}
		p.episodes = append(p.episodes, ep.EpisodeNumber)
		p.released = p.released || ep.TorrentReleased
		if p.name == "" {
			p.name = ep.EpisodeName
		}
	}

	cards := []AnimeReleaseCard{}
	for _, hash := range order {
		p := pointers[hash]
		slices.Sort(p.episodes)
		t, inClient := byHash[hash]
		card := releaseCard(hash, records[hash], t, p.name)
		card.Episodes = slices.Compact(p.episodes)
		switch {
		case p.released:
			card.State = ReleaseStateReleased
		case inClient:
			card.State, card.Progress = clientState(t)
		default:
			card.State = ReleaseStateMissing
		}
		cards = append(cards, card)
	}
	if keepRemoved {
		for hash, rec := range records {
			eps := rec.Episodes[animeID]
			if _, pointedAt := pointers[hash]; pointedAt || len(eps) == 0 {
				continue
			}
			if _, inClient := byHash[hash]; inClient {
				continue
			}
			card := releaseCard(hash, rec, torrents.TorrentInfo{}, "")
			card.Episodes = eps
			card.State = ReleaseStateRemoved
			cards = append(cards, card)
		}
	}
	slices.SortStableFunc(cards, func(a, b AnimeReleaseCard) int {
		if c := cmp.Compare(firstOr(a.Episodes), firstOr(b.Episodes)); c != 0 {
			return c
		}
		return cmp.Compare(a.Hash, b.Hash)
	})
	return cards
}

// releaseCard fills a card from the release record, or, for a torrent added before records were
// kept, from the client's name and size, its tags parsed from that name. Gone from the client too,
// it has only the episode record's name (episodeName), which is the app's, not the release's: no
// tags are read from it.
func releaseCard(hash string, rec files.ReleaseRecord, t torrents.TorrentInfo, episodeName string) AnimeReleaseCard {
	if rec.Name == "" && rec.AddedAt.IsZero() {
		if t.Name == "" {
			return AnimeReleaseCard{Hash: hash, Name: episodeName}
		}
		return AnimeReleaseCard{Hash: hash, Name: t.Name, Size: t.BytesTotal, Release: nyaa.ParseRelease(t.Name)}
	}
	card := AnimeReleaseCard{
		Hash: hash, Name: rec.Name, DetailURL: rec.DetailURL, Origin: rec.Origin, Size: rec.Size,
		Seeders: rec.Seeders, Leechers: rec.Leechers, Trusted: rec.Trusted, Remake: rec.Remake,
		Release: rec.Release, Recorded: true,
	}
	if card.Name == "" {
		// A magnet without dn: the client knows the name once the metadata came.
		card.Name = t.Name
		card.Release = nyaa.ParseRelease(t.Name)
	}
	if card.Size == 0 {
		card.Size = t.BytesTotal
	}
	if !rec.AddedAt.IsZero() {
		added := rec.AddedAt
		card.AddedAt = &added
	}
	if !rec.UploadedAt.IsZero() {
		uploaded := rec.UploadedAt
		card.UploadedAt = &uploaded
	}
	return card
}

// clientState is the card's state and progress (0..1) from the torrent client.
func clientState(t torrents.TorrentInfo) (string, float64) {
	progress := 0.0
	switch {
	case t.PiecesTotal > 0:
		progress = float64(t.PiecesHave) / float64(t.PiecesTotal)
	case t.BytesTotal > 0:
		progress = float64(t.BytesCompleted) / float64(t.BytesTotal)
	}
	switch {
	case t.Completed:
		if t.Status == "stopped" {
			return ReleaseStateStopped, 1
		}
		return ReleaseStateSeeding, 1
	case t.Status == "queued":
		return ReleaseStateQueued, progress
	case t.Status == "stopped":
		return ReleaseStateStopped, progress
	}
	return ReleaseStateDownloading, progress
}

func firstOr(eps []int) int {
	if len(eps) == 0 {
		return 0
	}
	return eps[0]
}
