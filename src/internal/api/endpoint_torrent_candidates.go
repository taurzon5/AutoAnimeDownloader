package api

import (
	"errors"
	"net/http"
	"path"
	"strconv"
	"strings"

	"AutoAnimeDownloader/src/internal/daemon"
	"AutoAnimeDownloader/src/internal/logger"
	"AutoAnimeDownloader/src/internal/nyaa"
)

type TorrentCandidate struct {
	Name       string `json:"name"`
	Magnet     string `json:"magnet"`
	SizeBytes  int64  `json:"size_bytes"`
	Seeders    int    `json:"seeders"`
	Leechers   int    `json:"leechers"`
	Fansub     string `json:"fansub"`
	Resolution string `json:"resolution"`
	IsBatch    bool   `json:"is_batch"`
	RangeStart int    `json:"range_start"`
	RangeEnd   int    `json:"range_end"`
	NyaaID     int    `json:"nyaa_id"`
	// MegapackSignals is what the name says about holding several AniList entries (season range,
	// "Complete Series", "+ Movies"); the detail's mapping confirms it. Empty for most packs.
	MegapackSignals []string `json:"megapack_signals,omitempty"`
}

type TorrentCandidateMedia struct {
	Title      string `json:"title"`
	CoverImage string `json:"cover_image,omitempty"`
	Format     string `json:"format"`
	Episodes   *int   `json:"episodes,omitempty"`
}

type TorrentCandidatesResponse struct {
	Query string                `json:"query"`
	Media TorrentCandidateMedia `json:"media"`
	Items []TorrentCandidate    `json:"items"`
}

type TorrentFile struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
}

type TorrentCandidateDetail struct {
	DescriptionHTML string        `json:"description_html"`
	Files           []TorrentFile `json:"files"`
	NyaaURL         string        `json:"nyaa_url"`
	// Mapping places the torrent's videos on the anime's franchise entries (megapack spec §3).
	// Absent for a torrent with fewer than two videos, or when AniList couldn't give the group.
	Mapping *MegapackMapping `json:"mapping,omitempty"`
}

// MegapackMapping is daemon.MegapackMapping on the wire.
type MegapackMapping struct {
	Megapack    bool            `json:"megapack"`
	Entries     []MegapackEntry `json:"entries"`
	Unmapped    []MegapackFile  `json:"unmapped"`
	TotalBytes  int64           `json:"total_bytes"`
	MappedBytes int64           `json:"mapped_bytes"`
}

type MegapackEntry struct {
	ID            int            `json:"id"`
	Title         string         `json:"title"`
	Format        string         `json:"format"`
	Year          int            `json:"year,omitempty"`
	EntryEpisodes int            `json:"entry_episodes,omitempty"`
	Episodes      []int          `json:"episodes"`
	Files         []MegapackFile `json:"files"`
	Bytes         int64          `json:"bytes"`
	Seed          bool           `json:"seed,omitempty"`
}

type MegapackFile struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	Episode   int    `json:"episode,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

func handleTorrentCandidates(server *Server) http.HandlerFunc {
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
			logger.Logger.Error().Err(err).Msg("Failed to load config")
			JSONInternalError(w, err)
			return
		}

		preview, err := daemon.PreviewWholeTitleTorrents(config, id, r.URL.Query().Get("q"))
		if err != nil {
			if errors.Is(err, nyaa.ErrNyaaUnavailable) {
				JSONError(w, http.StatusBadGateway, "NYAA_UNAVAILABLE", "Nyaa is unavailable")
				return
			}
			if strings.Contains(err.Error(), "does not exist on AniList") {
				JSONError(w, http.StatusNotFound, "ANIME_NOT_FOUND", "AniList does not know this media id")
				return
			}
			logger.Logger.Error().Err(err).Int("media_id", id).Msg("Failed to preview torrent candidates")
			JSONInternalError(w, err)
			return
		}

		JSONSuccess(w, http.StatusOK, TorrentCandidatesResponse{
			Query: preview.Query,
			Media: TorrentCandidateMedia{
				Title:      preview.Media.Title,
				CoverImage: preview.Media.CoverImage,
				Format:     preview.Media.Format,
				Episodes:   preview.Media.Episodes,
			},
			Items: mapTorrentCandidates(preview.Items),
		})
	}
}

// handleEpisodeTorrentCandidates is GET /animes/{id}/episodes/{episodeNumber}/torrent-candidates:
// the single-episode picker's list (daemon.PreviewEpisodeTorrents). The pick goes through
// POST /animes/{id}/episodes/{episodeNumber}/replace, as a pasted magnet does.
func handleEpisodeTorrentCandidates(server *Server) http.HandlerFunc {
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
		episode, err := strconv.Atoi(r.PathValue("episodeNumber"))
		if err != nil || episode <= 0 {
			JSONError(w, http.StatusBadRequest, "INVALID_EPISODE_NUMBER", "Invalid episode number")
			return
		}
		config, err := server.FileManager.LoadConfigs()
		if err != nil {
			JSONInternalError(w, err)
			return
		}
		preview, err := daemon.PreviewEpisodeTorrents(config, id, episode, r.URL.Query().Get("q"))
		if err != nil {
			if errors.Is(err, nyaa.ErrNyaaUnavailable) {
				JSONError(w, http.StatusBadGateway, "NYAA_UNAVAILABLE", "Nyaa is unavailable")
				return
			}
			if strings.Contains(err.Error(), "does not exist on AniList") {
				JSONError(w, http.StatusNotFound, "ANIME_NOT_FOUND", "AniList does not know this media id")
				return
			}
			JSONInternalError(w, err)
			return
		}
		JSONSuccess(w, http.StatusOK, TorrentCandidatesResponse{
			Query: preview.Query,
			Media: TorrentCandidateMedia{Title: preview.Media.Title, CoverImage: preview.Media.CoverImage, Format: preview.Media.Format, Episodes: preview.Media.Episodes},
			Items: mapTorrentCandidates(preview.Items),
		})
	}
}

func handleTorrentCandidateDetail(server *Server) http.HandlerFunc {
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

		nyaaID, err := strconv.Atoi(r.PathValue("nyaaId"))
		if err != nil || nyaaID <= 0 {
			JSONError(w, http.StatusBadRequest, "INVALID_NYAA_ID", "Invalid Nyaa torrent id")
			return
		}

		detail, err := nyaa.FetchTorrentDetail(nyaaID)
		if err != nil {
			if errors.Is(err, nyaa.ErrTorrentNotFound) {
				JSONError(w, http.StatusNotFound, "TORRENT_NOT_FOUND", "Torrent not found on Nyaa")
				return
			}
			if errors.Is(err, nyaa.ErrNyaaUnavailable) {
				JSONError(w, http.StatusBadGateway, "NYAA_UNAVAILABLE", "Nyaa is unavailable")
				return
			}
			logger.Logger.Error().Err(err).Msg("Failed to fetch torrent detail")
			JSONInternalError(w, err)
			return
		}

		out := mapTorrentCandidateDetail(detail)
		out.Mapping = megapackMappingOf(id, detail.Files)
		JSONSuccess(w, http.StatusOK, out)
	}
}

// megapackMappingOf maps a torrent's files for the picker. A failure leaves the detail without
// its mapping: the file list and the description are still worth showing.
func megapackMappingOf(mediaID int, files []nyaa.TorrentFile) *MegapackMapping {
	videos := 0
	for _, f := range files {
		if nyaa.IsVideoFile(path.Base(f.Name)) {
			videos++
		}
	}
	if videos < 2 {
		return nil
	}
	m, err := daemon.MapTorrentFiles(mediaID, files)
	if err != nil {
		logger.Logger.Warn().Err(err).Int("media_id", mediaID).Msg("No franchise group for the megapack mapping")
		return nil
	}
	out := &MegapackMapping{
		Megapack:    m.Megapack,
		Entries:     make([]MegapackEntry, 0, len(m.Entries)),
		Unmapped:    mapMegapackFiles(m.Unmapped),
		TotalBytes:  m.TotalBytes,
		MappedBytes: m.MappedBytes,
	}
	for _, e := range m.Entries {
		out.Entries = append(out.Entries, MegapackEntry{
			ID: e.ID, Title: e.Title, Format: e.Format, Year: e.Year, EntryEpisodes: e.EntryEpisodes,
			Episodes: e.Episodes, Files: mapMegapackFiles(e.Files), Bytes: e.Bytes, Seed: e.Seed,
		})
	}
	return out
}

func mapMegapackFiles(in []daemon.MegapackFile) []MegapackFile {
	out := make([]MegapackFile, 0, len(in))
	for _, f := range in {
		out = append(out, MegapackFile{Name: f.Name, SizeBytes: f.SizeBytes, Episode: f.Episode, Reason: f.Reason})
	}
	return out
}

func mapTorrentCandidates(in []daemon.TorrentCandidate) []TorrentCandidate {
	out := make([]TorrentCandidate, 0, len(in))
	for _, c := range in {
		out = append(out, TorrentCandidate{
			Name:       c.Name,
			Magnet:     c.Magnet,
			SizeBytes:  c.SizeBytes,
			Seeders:    c.Seeders,
			Leechers:   c.Leechers,
			Fansub:     c.Fansub,
			Resolution: c.Resolution,
			IsBatch:    c.IsBatch,
			RangeStart: c.RangeStart,
			RangeEnd:   c.RangeEnd,
			NyaaID:     c.NyaaID,

			MegapackSignals: nyaa.MegapackSignals(c.Name),
		})
	}
	return out
}

func mapTorrentCandidateDetail(d nyaa.TorrentDetail) TorrentCandidateDetail {
	files := make([]TorrentFile, 0, len(d.Files))
	for _, f := range d.Files {
		files = append(files, TorrentFile{Name: f.Name, SizeBytes: f.SizeBytes})
	}
	return TorrentCandidateDetail{
		DescriptionHTML: d.DescriptionHTML,
		Files:           files,
		NyaaURL:         d.NyaaURL,
	}
}
