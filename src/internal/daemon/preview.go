package daemon

import (
	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/logger"
	"AutoAnimeDownloader/src/internal/nyaa"

	"errors"
	"fmt"
	"strings"
)

// TorrentCandidate is one ranked whole-title torrent the picker can show. Range comes from the
// name only — preview never fetches /view.
type TorrentCandidate struct {
	Name       string
	Magnet     string
	SizeBytes  int64
	Seeders    int
	Leechers   int
	Fansub     string
	Resolution string
	IsBatch    bool
	RangeStart int // ExtractBatchInfo from the NAME only. 0 = unknown. Do not fetch /view.
	RangeEnd   int
	NyaaID     int
}

const maxTorrentCandidates = 8

// PickerMedia is the AniList identity the picker header needs. Built from the GetMediaByID
// already done for the search — no second round-trip, no seasonYear.
type PickerMedia struct {
	Title      string
	CoverImage string
	Format     string
	Episodes   *int
}

type WholeTitlePreview struct {
	Query string
	Media PickerMedia
	Items []TorrentCandidate
}

func pickerMediaFrom(anime *anilist.MediaList) PickerMedia {
	return PickerMedia{
		Title:      getAnimeTitleSafe(*anime),
		CoverImage: anime.Media.CoverImage.Medium,
		Format:     string(anime.Media.Format),
		Episodes:   anime.Media.Episodes,
	}
}

func PreviewWholeTitleTorrents(configs *files.Config, mediaID int, query string) (WholeTitlePreview, error) {
	return previewWholeTitle(configs, mediaID, query, queueRanking(mediaID))
}

// PreviewWholeTitleTorrentsInQueue is PreviewWholeTitleTorrents ranked by queue's priorities instead
// of the anime's own queue's: Add anime searches before the anime is in the queue it chose
// (decisions/164). An unknown queue ranks as the default one.
func PreviewWholeTitleTorrentsInQueue(configs *files.Config, mediaID int, query, queue string) (WholeTitlePreview, error) {
	q, _ := files.FindQueue(loadQueues(), queue)
	return previewWholeTitle(configs, mediaID, query, q.Priorities)
}

func previewWholeTitle(configs *files.Config, mediaID int, query string, ranking *nyaa.Priorities) (WholeTitlePreview, error) {
	empty := WholeTitlePreview{}
	anime, err := anilist.GetMediaByID(mediaID, anilist.PriorityCritical)
	if err != nil && !errors.Is(err, anilist.ErrFromCache) {
		return empty, fmt.Errorf("failed to fetch anime %d from AniList: %w", mediaID, err)
	}
	if anime == nil {
		return empty, fmt.Errorf("anime %d does not exist on AniList", mediaID)
	}
	media := pickerMediaFrom(anime)

	used := strings.TrimSpace(query)
	var results []nyaa.TorrentResult
	if isAnimeMovie(*anime) {
		if used == "" {
			used, results, err = previewSearchWithVariants(anime.Media.Title, func(title string) ([]nyaa.TorrentResult, error) {
				return nyaa.ScrapNyaaForMovie(title, true)
			})
		} else {
			results, err = nyaa.ScrapNyaaForMovie(used, true)
		}
		if err != nil {
			return WholeTitlePreview{Query: used, Media: media}, wrapNyaaSearchError(err)
		}
		results, _ = filterSearchResults(rankMovieResults(results, ranking), configs.MaxEpisodeTorrentSizeGB, configs.MinSeeders)
	} else {
		season, part := seasonPartOf(anime.Media)
		if used == "" {
			used, results, err = previewSearchWithVariants(anime.Media.Title, func(title string) ([]nyaa.TorrentResult, error) {
				return nyaa.ScrapNyaaForAnime(title, nil, season, part)
			})
		} else {
			results, err = nyaa.ScrapNyaaForAnime(used, nil, nil, nil)
		}
		if err != nil {
			return WholeTitlePreview{Query: used, Media: media}, wrapNyaaSearchError(err)
		}
		// Ranked as the pass ranks this anime: the list is the pass's own choice offered to confirm
		// (decisions/160), so a FINISHED series counts its source here too (decisions/107).
		finished := anime.Media.Status == anilist.MediaStatusFinished
		packs, _, _ := partitionSearchResults(configs, *anime, season, rankSeriesResults(results, finished, ranking))
		results = packs
	}

	if len(results) > maxTorrentCandidates {
		results = results[:maxTorrentCandidates]
	}
	return WholeTitlePreview{Query: used, Media: media, Items: toCandidates(results)}, nil
}

// PreviewEpisodeTorrents lists the torrents for one episode, the single-episode picker (v0.2.0):
// with no query, the pass's own single-episode search (searchNyaaForSingleEpisode, fallbacks
// included) ranked as the pass ranks; with a query, Nyaa searched for it and filtered to the
// episode. Every result is listed, the size and seeder limits included: a hand pick ignores them
// (decisions/160).
func PreviewEpisodeTorrents(configs *files.Config, mediaID, episode int, query string) (WholeTitlePreview, error) {
	empty := WholeTitlePreview{}
	anime, err := anilist.GetMediaByID(mediaID, anilist.PriorityCritical)
	if err != nil && !errors.Is(err, anilist.ErrFromCache) {
		return empty, fmt.Errorf("failed to fetch anime %d from AniList: %w", mediaID, err)
	}
	if anime == nil {
		return empty, fmt.Errorf("anime %d does not exist on AniList", mediaID)
	}
	media := pickerMediaFrom(anime)
	total := anilist.LastAiredEpisode(*anime)
	finished := anime.Media.Status == anilist.MediaStatusFinished
	used := strings.TrimSpace(query)
	season, part := seasonPartOf(anime.Media)
	var results []nyaa.TorrentResult
	if used == "" {
		results = searchNyaaForSingleEpisode(anilist.AiringNode{Episode: episode}, anime.Media, total)
		if len(results) == 0 {
			// The pass's search swallows Nyaa's errors: an empty list must still tell an outage from
			// "no release" (previewSearchWithVariants exists for the same reason).
			if _, _, err := previewSearchWithVariants(anime.Media.Title, func(title string) ([]nyaa.TorrentResult, error) {
				return nyaa.ScrapNyaa(title, episode, season, part, total)
			}); err != nil {
				return WholeTitlePreview{Media: media}, wrapNyaaSearchError(err)
			}
		}
	} else {
		// The typed query as is, but with the anime's season and part: the single-episode scrape
		// reads a missing season as season 1 and would drop a sequel's rows (decisions/108).
		results, err = nyaa.ScrapNyaa(used, episode, season, part, total)
		if err != nil {
			return WholeTitlePreview{Query: used, Media: media}, wrapNyaaSearchError(err)
		}
	}
	results = rankSeriesResults(results, finished, queueRanking(mediaID))
	if len(results) > maxTorrentCandidates {
		results = results[:maxTorrentCandidates]
	}
	return WholeTitlePreview{Query: used, Media: media, Items: toCandidates(results)}, nil
}

func wrapNyaaSearchError(err error) error {
	if errors.Is(err, nyaa.ErrNyaaUnavailable) {
		return fmt.Errorf("failed to search nyaa: %w", err)
	}
	return fmt.Errorf("failed to search nyaa: %w: %v", nyaa.ErrNyaaUnavailable, err)
}

// previewSearchWithVariants is the title-variant loop for the picker. Unlike
// searchNyaaWithVariants it surfaces a total Nyaa failure (every variant errored) so the
// picker can show "Nyaa down" instead of an empty list. An empty result still continues to
// the next variant, same as the pass.
func previewSearchWithVariants(titles anilist.Title, searchFn nyaaSearchFunc) (string, []nyaa.TorrentResult, error) {
	variants := buildTitleVariants(titles)
	if len(variants) == 0 {
		return "", nil, nil
	}

	var lastErr error
	errored := 0
	for i, variant := range variants {
		logger.Logger.Debug().
			Str("title", variant).
			Int("attempt", i+1).
			Int("total_attempts", len(variants)).
			Msg("Attempting preview search with title variant")

		result, err := searchFn(variant)
		if err != nil {
			logger.Logger.Debug().
				Err(err).
				Str("title", variant).
				Int("attempt", i+1).
				Msg("Error searching Nyaa for preview")
			lastErr = err
			errored++
			continue
		}
		if result != nil {
			logger.Logger.Info().
				Str("title", variant).
				Int("torrents_found", len(result)).
				Int("attempt", i+1).
				Msg("Found preview torrents on Nyaa")
			return variant, result, nil
		}
	}
	if errored == len(variants) {
		return variants[0], nil, lastErr
	}
	return variants[0], nil, nil
}

func toCandidates(results []nyaa.TorrentResult) []TorrentCandidate {
	if len(results) == 0 {
		return nil
	}
	out := make([]TorrentCandidate, len(results))
	for i, tr := range results {
		info := nyaa.ExtractBatchInfo(tr.Name)
		resolution := ""
		if tr.Resolution != nil {
			resolution = *tr.Resolution
		}
		out[i] = TorrentCandidate{
			Name:       tr.Name,
			Magnet:     tr.MagnetLink,
			SizeBytes:  tr.Size,
			Seeders:    nyaa.ParseSeeders(tr.Seeders),
			Leechers:   tr.Leechers,
			Fansub:     tr.Fansub,
			Resolution: resolution,
			IsBatch:    tr.IsBatch,
			RangeStart: info.StartEpisode,
			RangeEnd:   info.EndEpisode,
			NyaaID:     nyaa.ParseNyaaID(tr.DetailURL),
		}
	}
	return out
}
