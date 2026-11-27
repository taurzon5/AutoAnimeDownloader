package daemon

import (
	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/logger"
	"AutoAnimeDownloader/src/internal/nyaa"
	"AutoAnimeDownloader/src/internal/torrents"
	"errors"
	"fmt"
	"time"
)

// addAndPrioritize adds the magnet and puts it at the front of the download queue.
//
// A download asked for by hand has to start now, not when the queue gets to it — but it still
// respects max_concurrent_downloads: instead of becoming one more active torrent, it demotes the
// least advanced automatic one. A limit the UI shows being broken is a limit nobody trusts.
//
// A Prioritize failure is only logged: the torrent is already in the session and will download
// anyway, just in its turn — aborting the whole download because of that would be worse. sizeBytes
// is the size Nyaa announced; 0 when the magnet was pasted by the user and there's no way to know
// the size before the metadata.
// animeRoot is the anime's download root override (AnimeSettings.Root), "" for none.
func animeRoot(fm FileManagerInterface, mediaID int) string {
	if fm == nil {
		return ""
	}
	if s, err := fm.LoadAnimeSettings(mediaID); err == nil && s != nil && s.Root != nil {
		return *s.Root
	}
	return ""
}

func addAndPrioritize(backend torrents.TorrentBackend, magnet string, sizeBytes int64, configs *files.Config, target torrentTarget) (string, error) {
	// The disk space guard lives here (and not in torrents.Session.Add) because the torrents package
	// doesn't know files.Config — passing the config there just to read a percentage would invert the
	// dependency. This is the only Add of the manual paths.
	root, _, err := (*diskBudget)(nil).reserve(configs, nyaa.TorrentResult{MagnetLink: magnet, Size: sizeBytes}, placedOrder(configs, backend, magnet, rootOrder(configs, backend, target, target.root)))
	if err != nil {
		return "", err
	}
	if err := registerPlacement(configs, backend, magnet, target, root); err != nil {
		return "", err
	}
	hash, err := backend.Add(magnet)
	if err != nil || hash == "" {
		return hash, err
	}
	if err := backend.Prioritize(hash); err != nil {
		logger.Logger.Warn().Err(err).Str("hash", hash).
			Msg("Manual download added but could not be prioritized; it will start when the queue reaches it")
	}
	return hash, nil
}

type animeDetails struct {
	mediaList  anilist.MediaList
	title      string
	isFinished bool
}

// resolveAnimeDetails fetches an anime by MEDIA id, collapsing the configured accounts (AniList and
// MyAnimeList), and falls back to GetMediaByID when no account tracks it.
//
// The fallback is what makes the per-episode buttons (download, redownload, replace) work on a
// standalone anime: its detail screen opens — resolveMediaList already covered that —, but every
// action died here with "not in any account". With no accounts configured, which became a valid
// state, that was the path of EVERY manual download.
//
// There's no check against the standalone file here, unlike api.resolveMediaList: these functions
// already require a media id and an episode id only the detail screen provides, and the result is
// always saved with ManuallyManaged=true — the loop never deletes it for not tracking the anime. An
// id that doesn't exist on AniList still returns an error.
//
// The fallback (ml == nil) is the same synthetic path as appendStandaloneAnimes: it goes through
// withStandaloneProgress to inject the saved progress. Without that RunAnimeDebug reported a
// standalone anime with saved progress as if it started at episode 1 — the debug pipeline diverging
// from the real pipeline it exists to mirror. For the ManualDownload* callers the effect is
// harmless: they select the episode by explicit number (findEpisodeNode), not by Progress.
func resolveAnimeDetails(fm FileManagerInterface, animeId int, configs *files.Config) (*animeDetails, error) {
	ml, err := anilist.GetAnimeInfo(animeId, configs.AnilistUsernames, configs.MALUsernames, anilist.PriorityCritical)
	// The local cache serves: what's fetched here is title, relations and status to build the Nyaa
	// query, and Nyaa didn't go down with AniList.
	if err != nil && !errors.Is(err, anilist.ErrFromCache) {
		return nil, fmt.Errorf("failed to get anime info: %w", err)
	}
	if ml == nil {
		if ml, err = anilist.GetMediaByID(animeId, anilist.PriorityCritical); err != nil && !errors.Is(err, anilist.ErrFromCache) {
			return nil, fmt.Errorf("failed to get anime info: %w", err)
		}
		ml = withStandaloneProgress(fm, ml)
	}
	if ml == nil {
		return nil, fmt.Errorf("anime %d does not exist on AniList", animeId)
	}
	if ml.Status == anilist.MediaListStatusCompleted && completedCustomListMember(configs, animeId) {
		// The pass downloads it whole (decisions/162); the debug run must select the same.
		ml.Progress = 0
	}

	title := ""
	if ml.Media.Title.English != nil && *ml.Media.Title.English != "" {
		title = *ml.Media.Title.English
	} else if ml.Media.Title.Romaji != nil {
		title = *ml.Media.Title.Romaji
	}

	return &animeDetails{
		mediaList:  *ml,
		title:      title,
		isFinished: ml.Media.Status == anilist.MediaStatusFinished,
	}, nil
}

// findEpisodeNode finds the episode by NUMBER in the anime's full list — not in the raw
// airingSchedule, which has no node for an episode outside the window AniList keeps. Without this
// the per-episode buttons died with "episode not found" precisely on the animes where the screen
// shows synthetic episodes (see anilist.EpisodeList and decisions/052).
func findEpisodeNode(ml anilist.MediaList, episodeNumber int) *anilist.AiringNode {
	for _, node := range anilist.EpisodeList(ml, 1) {
		if node.Episode == episodeNumber {
			n := node
			return &n
		}
	}
	return nil
}

// ManualDownloadEpisodeWithMagnet downloads a specific episode using a user-supplied magnet link.
// Skips Nyaa search entirely. Returns the saved EpisodeStruct with ManuallyManaged=true on success.
// picked is the Nyaa row the episode picker chose it from; empty for a pasted magnet.
func ManualDownloadEpisodeWithMagnet(fm FileManagerInterface, backend torrents.TorrentBackend, animeId int, episodeNumber int, magnet string, configs *files.Config, picked PickedRelease) (files.EpisodeStruct, error) {
	if _, err := backend.Ensure(configs.DownloadPath()); err != nil {
		return files.EpisodeStruct{}, err
	}

	details, err := resolveAnimeDetails(fm, animeId, configs)
	if err != nil {
		return files.EpisodeStruct{}, err
	}

	targetNode := findEpisodeNode(details.mediaList, episodeNumber)
	if targetNode == nil {
		return files.EpisodeStruct{}, fmt.Errorf("episode %d not found for anime %d", episodeNumber, animeId)
	}

	epName := fmt.Sprintf("%s - Episode %d", details.title, targetNode.Episode)
	target := manualTarget(configs, details.mediaList, targetNode.Episode, false)
	target.root = animeRoot(fm, details.mediaList.Media.Id)
	hash, err := addAndPrioritize(backend, magnet, 0, configs, target)
	if err != nil || hash == "" {
		return files.EpisodeStruct{}, fmt.Errorf("failed to add torrent to embedded client: %w", err)
	}
	picked.record(hash, magnet, animeId, targetNode.Episode)

	return files.EpisodeStruct{
		AnimeID:         animeId,
		AnimeName:       details.title,
		EpisodeHash:     hash,
		EpisodeName:     epName,
		EpisodeNumber:   targetNode.Episode,
		DownloadDate:    time.Now(),
		ManuallyManaged: true,
	}, nil
}

// ManualDownloadAnimeWithMagnet downloads an entire anime using a user-supplied batch magnet link.
// Marks all aired episodes as downloaded sharing the same torrent hash. picked is the Nyaa row the
// torrent picker chose it from; empty for a pasted magnet.
func ManualDownloadAnimeWithMagnet(fm FileManagerInterface, backend torrents.TorrentBackend, animeId int, magnet string, configs *files.Config, picked PickedRelease) ([]files.EpisodeStruct, error) {
	if _, err := backend.Ensure(configs.DownloadPath()); err != nil {
		return nil, err
	}

	details, err := resolveAnimeDetails(fm, animeId, configs)
	if err != nil {
		return nil, err
	}

	target := manualTarget(configs, details.mediaList, 0, true)
	target.root = animeRoot(fm, details.mediaList.Media.Id)
	hash, err := addAndPrioritize(backend, magnet, 0, configs, target)
	if err != nil || hash == "" {
		return nil, fmt.Errorf("failed to add torrent to embedded client: %w", err)
	}

	now := time.Now()
	var episodes []files.EpisodeStruct
	for _, node := range anilist.EpisodeList(details.mediaList, 1) {
		if node.TimeUntilAiring > 0 {
			continue
		}
		epName := fmt.Sprintf("%s - Episode %d", details.title, node.Episode)
		episodes = append(episodes, files.EpisodeStruct{
			AnimeID:         animeId,
			AnimeName:       details.title,
			EpisodeHash:     hash,
			EpisodeName:     epName,
			EpisodeNumber:   node.Episode,
			IsBatch:         true,
			DownloadDate:    now,
			ManuallyManaged: true,
		})
	}

	if len(episodes) == 0 {
		return nil, fmt.Errorf("no aired episodes found for anime %d", animeId)
	}
	numbers := make([]int, len(episodes))
	for i, ep := range episodes {
		numbers[i] = ep.EpisodeNumber
	}
	picked.record(hash, magnet, animeId, numbers...)

	return episodes, nil
}

// ManualDownloadEpisode downloads a specific episode manually (called from API).
// Returns the saved EpisodeStruct with ManuallyManaged=true on success.
func ManualDownloadEpisode(fm FileManagerInterface, backend torrents.TorrentBackend, animeId int, episodeNumber int, configs *files.Config) (files.EpisodeStruct, error) {
	if _, err := backend.Ensure(configs.DownloadPath()); err != nil {
		return files.EpisodeStruct{}, err
	}

	details, err := resolveAnimeDetails(fm, animeId, configs)
	if err != nil {
		return files.EpisodeStruct{}, err
	}

	targetNode := findEpisodeNode(details.mediaList, episodeNumber)
	if targetNode == nil {
		return files.EpisodeStruct{}, fmt.Errorf("episode %d not found for anime %d", episodeNumber, animeId)
	}

	epName := fmt.Sprintf("%s - Episode %d", details.title, targetNode.Episode)

	// Checked before the Nyaa search so the handler gets ErrInsufficientDiskSpace instead of the
	// generic "failed after N attempts" (which would become a 500 instead of a 409).
	// Any download root with space will do (decisions/154).
	if err := (*diskBudget)(nil).check(configs, 0); err != nil {
		return files.EpisodeStruct{}, err
	}

	// The same search as the scheduled pass (episodes.go), offset and split-entry fallbacks included,
	// but not its size and seeder filters: a click is the user's pick, and only the disk guard in
	// addAndPrioritize applies (decisions/160).
	media := details.mediaList.Media
	results := rankSeriesResults(searchNyaaForSingleEpisode(*targetNode, media, anilist.LastAiredEpisode(details.mediaList)), details.isFinished)
	if len(results) == 0 {
		return files.EpisodeStruct{}, fmt.Errorf("no torrents found for episode %d", targetNode.Episode)
	}

	maxAttempts := min(configs.EpisodeRetryLimit, len(results))
	target := manualTarget(configs, details.mediaList, targetNode.Episode, false)
	target.root = animeRoot(fm, details.mediaList.Media.Id)
	var hash string
	for i := range maxAttempts {
		h, err := addAndPrioritize(backend, results[i].MagnetLink, results[i].Size, configs, target)
		if err == nil && h != "" {
			hash = h
			recordRelease(hash, results[i], files.ReleaseOriginManual, animeId, targetNode.Episode)
			break
		}
	}

	if hash == "" {
		return files.EpisodeStruct{}, fmt.Errorf("failed to download episode after %d attempts", maxAttempts)
	}

	return files.EpisodeStruct{
		AnimeID:         animeId,
		AnimeName:       details.title,
		EpisodeHash:     hash,
		EpisodeName:     epName,
		EpisodeNumber:   targetNode.Episode,
		DownloadDate:    time.Now(),
		ManuallyManaged: true,
	}, nil
}
