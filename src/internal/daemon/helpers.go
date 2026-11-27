package daemon

import (
	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/logger"
	"AutoAnimeDownloader/src/internal/notifications"
	"AutoAnimeDownloader/src/internal/nyaa"
	"AutoAnimeDownloader/src/internal/torrents"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

type FileManagerInterface interface {
	LoadConfigs() (*files.Config, error)
	SaveConfigs(config *files.Config) error
	LoadSavedEpisodes() ([]files.EpisodeStruct, error)
	SaveEpisodesToFile(episodes []files.EpisodeStruct) error
	UpsertEpisodes(episodes []files.EpisodeStruct) error
	DeleteEpisodesFromFile(keys []files.EpisodeKey) error
	DeleteEmptyFolders(completedAnimeSaveFolder string) error
	LoadBlockedEpisodes() ([]files.EpisodeKey, error)
	BlockEpisode(key files.EpisodeKey) error
	UnblockEpisode(key files.EpisodeKey) error
	UnmanageEpisode(key files.EpisodeKey) error
	LoadAllAnimeSettings() (map[int]files.AnimeSettings, error)
	LoadAnimeSettings(animeID int) (*files.AnimeSettings, error)
	SaveAnimeSettings(animeID int, settings files.AnimeSettings) error
	LoadStandaloneAnimes() ([]int, error)
	AddStandaloneAnime(mediaID int) error
	RemoveStandaloneAnime(mediaID int) error
}

// ErrInsufficientDiskSpace is returned by checkRootBudgetSpace when leftover free space would fall
// below Config.MinFreeDiskGB, or when the torrent to be added doesn't fit in what's left.
var ErrInsufficientDiskSpace = errors.New("insufficient free disk space")

// giB is notifications.GiB: one unit for the disk checks and the sizes their alerts print.
const giB = int64(notifications.GiB)

// DiskLow is true exactly when the daemon would refuse a new torrent of size 0: the add-gate,
// including what the session still has to write, against the GiB floor. GET /status's disk_low
// is this flag and nothing else (decisions/093).
func DiskLow(configs *files.Config, backend torrents.TorrentBackend, session []torrents.TorrentInfo, saved []files.EpisodeStruct) bool {
	if configs == nil {
		return false
	}
	// Per download root, as the add-gate counts: low only when no root takes a torrent
	// (decisions/154).
	return newDiskBudget(session, saved).withRoots(configs, backend, nil).check(configs, 0) != nil
}

// RootDisk is one download root's volume for GET /status: Low when it can't take a new torrent of
// size 0 now, counting what the session still writes there (decisions/154).
type RootDisk struct {
	Name, Path  string
	Total, Free uint64
	Low         bool
	// Drive is the index of the first root on the same filesystem: roots on one drive share one
	// free space (decisions/154), and the Status page shows them as one line.
	Drive int
}

// RootDisks reports every download root's volume. A root whose volume doesn't answer reports zeros.
func RootDisks(configs *files.Config, backend torrents.TorrentBackend, session []torrents.TorrentInfo, saved []files.EpisodeStruct) []RootDisk {
	if configs == nil {
		return nil
	}
	budget := newDiskBudget(session, saved).withRoots(configs, backend, nil)
	var out []RootDisk
	roots := configs.Roots()
	for i, r := range roots {
		d := RootDisk{Name: r.Name, Path: r.Path, Drive: i}
		d.Total, d.Free, _ = files.DiskSpace(r.Path)
		d.Low = budget.checkOn(configs, r.Path, 0) != nil
		for j := range i {
			if sameFilesystem(roots[j].Path, r.Path) {
				d.Drive = j
				break
			}
		}
		out = append(out, d)
	}
	return out
}

// checkRootBudgetSpace blocks ADDING a new torrent to a download root when its volume is full OR
// when the torrent doesn't fit in it (decisions/093, decisions/154).
//
// torrentSizeBytes is the size Nyaa announced, 0 when unknown (hand-pasted magnet) — and then the
// guard falls back to the GiB-floor check it always was. Without it a 61 GiB pack got into a
// volume with 40 GiB free and only stopped when the disk filled up, mid-download: free space
// alone answers "is there space NOW", never "is there space at the end".
//
// reservedBytes is what the torrents already added still have to write (see diskBudget): free
// space alone let a pass queue 130 GiB of loose episodes, each one fitting on its own, into 6 GiB
// of headroom (decisions/101). It counts the same as the torrent's own size.
//
// The two halves have different triggers on purpose. The GiB floor is the user's policy and turns
// off with min_free_disk_gb = 0; "the torrent is bigger than the free space" is arithmetic, and
// holds even with the policy off.
//
// It doesn't block the verification pass: pruning watched episodes, deleteEpisodesByStatus and
// organize are exactly what FREES space, and an "if disk full { return }" at the start of the pass
// would leave the app stuck in the one state it can't unclog itself from.
//
// A statfs error does NOT block: a volume that doesn't answer (network, permission) is no proof of
// a full disk, and turning it into "stop downloading everything" is worse than the risk the guard
// covers.
//
// Per root: the root's volume takes the torrent on top of what is reserved there and keeps the
// root's own min_free_gb (min_free_disk_gb when unset); a root over its limit passes the torrent to
// the next one. With the incomplete folder on another drive (decisions/141), the root also counts
// what staging torrents hold and have not moved yet (stagedPending), and the incomplete folder's
// volume counts what staging torrents still download (staged), with incomplete_min_free_disk_gb.
// With it on this root's drive, finishing is a rename, and stagedOtherBytes, what staging torrents
// bound for other volumes still download, lands here first, so it counts.
func checkRootBudgetSpace(configs *files.Config, root string, torrentSizeBytes, reservedBytes, stagedBytes, stagedPendingBytes, stagedOtherBytes int64) error {
	minFree := configs.RootMinFreeGB(root)
	staging := configs.StagingPath()
	if staging == "" {
		return checkVolumeSpace(root, minFree, torrentSizeBytes, reservedBytes)
	}
	if sameFilesystem(staging, root) {
		return checkVolumeSpace(root, minFree, torrentSizeBytes, reservedBytes+stagedOtherBytes)
	}
	if err := checkVolumeSpace(root, minFree, torrentSizeBytes, reservedBytes+stagedPendingBytes); err != nil {
		return err
	}
	return checkVolumeSpace(staging, configs.IncompleteMinFreeDiskGB, torrentSizeBytes, stagedBytes)
}

// sameFilesystem is files.SameFilesystem, swappable in tests (their temp folders share one disk).
var sameFilesystem = files.SameFilesystem

// checkVolumeSpace is the guard on one volume: path's filesystem must take torrentSizeBytes on
// top of reservedBytes, and keep minFreeGB GiB after that.
func checkVolumeSpace(path string, minFreeGB int, torrentSizeBytes, reservedBytes int64) error {
	needed := torrentSizeBytes + reservedBytes
	if path == "" || (minFreeGB <= 0 && needed <= 0) {
		return nil
	}
	total, free, err := files.DiskSpace(path)
	if err != nil {
		logger.Logger.Debug().Err(err).Msg("Disk space guard: statfs failed, not blocking downloads")
		return nil
	}
	if total == 0 {
		return nil
	}
	if needed > 0 && float64(needed) > float64(free) {
		return fmt.Errorf("%w: torrent needs %d bytes on top of %d reserved by torrents still downloading, only %d free on %s", ErrInsufficientDiskSpace, torrentSizeBytes, reservedBytes, free, path)
	}
	if minFreeGB <= 0 {
		return nil
	}
	leftover := int64(free) - needed
	if leftover < int64(minFreeGB)*giB {
		return fmt.Errorf("%w: %d GiB free required on %s (torrent: %d bytes, reserved by torrents still downloading: %d bytes)", ErrInsufficientDiskSpace, minFreeGB, path, torrentSizeBytes, reservedBytes)
	}
	return nil
}

// HandleTorrentFailure reacts to a torrent the embedded client stopped with an error.
//
// rain leaves a failed torrent Stopped *inside* the session and never restarts it, and the
// per-torrent listener goroutine exits after NotifyStop. Left alone, episodeInTorrents would
// keep seeing the hash and the daemon would believe the episode was downloaded forever. So:
// fire the download_failed webhook (the anime name/episode come from the saved record joined
// by infohash) and drop the torrent from the session, which makes episodeInTorrents false on
// the next pass so the verification loop re-adds the magnet — retry with the machinery that
// already exists. The re-add-loop risk on a genuinely dead torrent is accepted (decisions/024).
func HandleTorrentFailure(hash string, cause error, backend torrents.TorrentBackend, fileManager FileManagerInterface) {
	logger.Logger.Warn().Err(cause).Str("hash", hash).Msg("Torrent stopped with error")

	// A finished direct-mode torrent whose library file was deleted outside the app stops with an
	// error, because the placement storage refuses to recreate the file. That is the warning and
	// nothing else (decisions/139): no drop, no re-add, no download_failed.
	// The incomplete folder is gone (its drive unmounted): dropping and re-adding would only fail
	// the same way. The download queue starts the torrent again on every pass, which works as soon
	// as the folder is back; until then the pass reports the folder (decisions/141).
	if errors.Is(cause, torrents.ErrIncompleteFolderUnavailable) {
		logger.Logger.Warn().Str("hash", hash).Msg("The incomplete downloads folder is unavailable; the torrent waits for it")
		return
	}
	// The same for a download root whose drive is gone (decisions/154).
	if errors.Is(cause, torrents.ErrDownloadRootUnavailable) {
		logger.Logger.Warn().Str("hash", hash).Msg("The torrent's download folder is unavailable; the torrent waits for it")
		return
	}
	if backend != nil {
		if p, ok := backend.Placement(hash); ok && PlacedFilesMissing(p) {
			logger.Logger.Warn().Str("hash", hash).Str("series_dir", p.SeriesDir).
				Msg("A library file of this torrent was deleted outside the app; it stays stopped until the user picks Redownload missing files")
			return
		}
	}

	failed := notifications.Episode{TorrentHash: hash}
	if saved, err := fileManager.LoadSavedEpisodes(); err != nil {
		logger.Logger.Warn().Err(err).Str("hash", hash).Msg("Torrent failure: failed to load saved episodes for the webhook")
	} else {
		var records []files.EpisodeStruct
		for _, ep := range saved {
			if ep.EpisodeHash == hash {
				records = append(records, ep)
			}
		}
		if len(records) > 0 {
			failed.AnimeID = records[0].AnimeID
			failed.AnimeName = records[0].AnimeName
			failed.Number = records[0].EpisodeNumber
		}
		// The add → stall → drop loop never shows up in a pass's problems (every add succeeds): the
		// drop itself is what keeps episode_stuck's clock running.
		recordTorrentDrop(records, time.Now())
	}

	failed.Reason = notifications.ReasonDownloadRejected
	if cause != nil {
		failed.Reason = cause.Error()
	}

	// Once per (episode, torrent): a dead torrent is re-picked and drops again about every hour, and
	// the key is the hash, never the reason — "stalled: no peers for 1h0m" changes text every drop.
	// A different torrent failing for the same episode is news and goes out (decisions/128).
	if !firstFailure(failed.AnimeID, failed.Number, hash) {
		logger.Logger.Debug().Str("hash", hash).Msg("Torrent failure: already notified for this episode and torrent")
	} else if configs, err := fileManager.LoadConfigs(); err != nil {
		logger.Logger.Warn().Err(err).Str("hash", hash).Msg("Torrent failure: failed to load configs, skipping webhook")
	} else {
		notifications.Notify(configs, notifications.DownloadFailed, failed)
	}

	if backend == nil {
		return
	}
	// The bytes already downloaded STAY when there are any: the next pass's re-add uses the same id
	// (Session.Add passes AddTorrentOptions{ID: hash}) and rain lands in the same <DataDir>/<id>,
	// sees the existing files (allocator.HasExisting) and runs the verifier, which rebuilds the
	// bitfield. The download resumes where it stopped. Without this, a transient error on a 90 GiB
	// pack at 90% cost the whole download again.
	//
	// PiecesHave is the progress source that survives the Stop — BytesCompleted zeroes when rain
	// frees the pieces, and the failed torrent is already stopped here.
	//
	// Zero verified pieces -> keepData=false: there's nothing to recover, and keeping the folder
	// would only leave permanent garbage on disk when the retry lands on another infohash (the
	// directory is per torrent id, and nothing sweeps the download path).
	keepData := false
	if info, ok := backend.Get(hash); ok && info.PiecesHave > 0 {
		keepData = true
		logger.Logger.Info().Str("hash", hash).Uint32("pieces_have", info.PiecesHave).Uint32("pieces_total", info.PiecesTotal).
			Msg("Torrent failure: keeping partial data for the re-add to resume from")
	}
	if err := backend.Remove(hash, keepData); err != nil {
		logger.Logger.Warn().Err(err).Str("hash", hash).Msg("Torrent failure: failed to remove the failed torrent from the session")
	}
}

func getWebUiURL() string {
	return WebUIURL(os.Getenv("PORT"), "/#/config?missingConfig=true")
}

// isConfigComplete: the LIBRARY is the only requirement. An AniList account doesn't count — since
// standalone animes (decisions/049) the app works entirely without any list, and requiring an
// account just for the pass to run would leave that installation stuck on the config screen.
func isConfigComplete(config *files.Config) bool {
	// The download directory is derived from the library (Config.DownloadPath), so checking the
	// derived one covers the only field the user configures.
	return config.DownloadPath() != ""
}

// OpenBrowser opens the URL in the user's browser. Used by the verification pass (incomplete
// config), the boot (first run, see main.go), the tray and the CLI's `open` command.
//
// A no-op inside a test binary (decisions/120): tests drive the incomplete-config pass, and
// without the guard every `go test` popped the config page in the developer's real browser.
func OpenBrowser(webUIURL string) error {
	if testing.Testing() {
		return nil
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", webUIURL)
	case "darwin":
		cmd = exec.Command("open", webUIURL)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", webUIURL)
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to open browser: %w", err)
	}

	logger.Logger.Info().
		Str("url", webUIURL).
		Msg("Opened the Web UI in the browser")

	return nil
}

func buildTorrentsHashSet(t []torrents.TorrentInfo) map[string]bool {
	m := make(map[string]bool, len(t))
	for _, torrent := range t {
		if torrent.Hash != "" {
			m[torrent.Hash] = true
		}
	}
	return m
}

// episodeInTorrents reports whether a saved episode's torrent is still present in the
// embedded torrent client. The join is always by infohash (never by name), which is
// definitive even for batch torrents whose display name doesn't match the anime title.
func episodeInTorrents(savedHash string, torrentsHashSet map[string]bool) bool {
	return savedHash != "" && torrentsHashSet[savedHash]
}

func buildSavedEpisodesMap(episodes []files.EpisodeStruct) map[files.EpisodeKey]bool {
	m := make(map[files.EpisodeKey]bool, len(episodes))
	for _, episode := range episodes {
		m[episode.Key()] = true
	}
	return m
}

func buildSavedEpisodesFullMap(episodes []files.EpisodeStruct) map[files.EpisodeKey]files.EpisodeStruct {
	m := make(map[files.EpisodeKey]files.EpisodeStruct, len(episodes))
	for _, ep := range episodes {
		m[ep.Key()] = ep
	}
	return m
}

func animeIsInExcludedList(anime anilist.MediaList, excludedLists []string) bool {
	if len(excludedLists) == 0 {
		return false
	}
	excludedSet := make(map[string]bool, len(excludedLists))
	for _, name := range excludedLists {
		excludedSet[name] = true
	}
	for listName, isInList := range anime.CustomLists {
		if excludedSet[listName] && isInList {
			return true
		}
	}
	return false
}

func isAnimeMovie(anime anilist.MediaList) bool {
	return anime.Media.Format == anilist.MediaFormatMovie
}

func getAnimeTitleSafe(anime anilist.MediaList) string {
	if anime.Media.Title.English != nil && *anime.Media.Title.English != "" {
		return *anime.Media.Title.English
	}
	if anime.Media.Title.Romaji != nil {
		return *anime.Media.Title.Romaji
	}
	return "Unknown"
}

// ExtractAnimeSeasonPart extracts the season and part numbers from the AniList data.
// Priority: english → romaji → synonyms. Each field is read independently — english can give the
// season and a synonym the part.
func ExtractAnimeSeasonPart(title anilist.Title, synonyms []string) (season, part *int) {
	titles := make([]string, 0, 2)
	if title.English != nil {
		titles = append(titles, *title.English)
	}
	if title.Romaji != nil {
		titles = append(titles, *title.Romaji)
	}
	var titlePart *int
	for _, t := range titles {
		if titlePart = nyaa.ExtractPart(t); titlePart != nil {
			break
		}
	}

	for i, s := range append(titles, synonyms...) {
		if season == nil {
			season = nyaa.ExtractSeason(s)
			// A synonym's "Season N" equal to the titles' "Part N", in a synonym that names no part
			// itself, is the same cour said another way: BAKI-DOU Part 2's "… Season 2" dropped every
			// pack as season 2. A synonym that says both ("BEASTARS Season 3 Part 2") keeps its season.
			if season != nil && i >= len(titles) && titlePart != nil && *season == *titlePart && nyaa.ExtractPart(s) == nil {
				season = nil
			}
		}
		if part == nil {
			part = nyaa.ExtractPart(s)
		}
		if season != nil && part != nil {
			break
		}
	}
	return
}

// seasonPartOf is ExtractAnimeSeasonPart over the synonyms that name the anime, never its source's
// title (decisions/132).
func seasonPartOf(media anilist.Media) (season, part *int) {
	return ExtractAnimeSeasonPart(media.Title, media.OwnSynonyms())
}

// ComputeEpisodeOffset returns the PREQUEL's episode total when part >= 2.
// Used to convert AniList's relative progress into the absolute number used by fansubs with
// continuous numbering (e.g. SubsPlease).
//
// Only a TV/TV_SHORT PREQUEL counts: recap movies, OVAs and ONAs also arrive as PREQUEL with
// Episodes filled in, but they're not part of the count fansubs follow. The exception is an ONA
// entry, whose ONA prequel counts too: a streaming series split into ONA parts (Stone Ocean
// Part 2) is one show, while an ONA side story of a TV series still stays out (decisions/133).
// It counts only when the entry has no TV/TV_SHORT prequel: with both, the TV one is the count.
// When in doubt it returns 0 — a wrong offset sends the search to a nonexistent episode, while a
// missing offset falls back to relative numbering, which is what most groups use.
//
// The same "first TV/TV_SHORT PREQUEL" selection lives in anilist.prequelOf (anilist/series.go),
// minus the ONA exception, and the two stay separate on purpose: that one walks the WHOLE chain
// (the series' absolute axis), this one takes ONE hop and is gated by part >= 2. They're the two DIFFERENT pack
// numbering hypotheses in daemon.packAxis — "continuous by season" and "absolute by series" —, so
// unifying them would erase the distinction the heuristic uses (decisions/079). A third,
// looser selection — ONA allowed, gated by a shared base title — is splitEntryContinuation
// (search.go, decisions/131).
func ComputeEpisodeOffset(format anilist.MediaFormat, relations anilist.MediaRelations, part *int) int {
	if part == nil || *part < 2 {
		return 0
	}
	// A TV prequel wins over an ONA one whatever the edge order: under an ONA entry with both, the
	// ONA is a side story and the TV entry is the season the count follows.
	ona := 0
	for _, edge := range relations.Edges {
		if edge.RelationType != "PREQUEL" || edge.Node.Episodes == nil {
			continue
		}
		switch edge.Node.Format {
		case anilist.MediaFormatTV, anilist.MediaFormatTVShort:
			return *edge.Node.Episodes
		case anilist.MediaFormatONA:
			if format == anilist.MediaFormatONA && ona == 0 {
				ona = *edge.Node.Episodes
			}
		}
	}
	return ona
}

func isInDeleteStatuses(deleteStatuses []string, status anilist.MediaListStatus) bool {
	for _, s := range deleteStatuses {
		if s == string(status) {
			return true
		}
	}
	return false
}

// episodeItems turns saved records into notification items: one per anime and torrent, in the
// order the records came. A pack's N records become one pack item (PackSize N) instead of N items —
// decisions/124.
func episodeItems(records []files.EpisodeStruct) []notifications.Episode {
	type key struct {
		animeID int
		hash    string
	}
	var order []key
	byKey := make(map[key]*notifications.Episode)
	for _, ep := range records {
		k := key{ep.AnimeID, ep.EpisodeHash}
		it, seen := byKey[k]
		if !seen {
			order = append(order, k)
			byKey[k] = &notifications.Episode{AnimeID: ep.AnimeID, AnimeName: ep.AnimeName, Number: ep.EpisodeNumber, TorrentHash: ep.EpisodeHash, PackSize: 1}
			continue
		}
		it.PackSize++
		it.Number = min(it.Number, ep.EpisodeNumber)
	}
	items := make([]notifications.Episode, 0, len(order))
	for _, k := range order {
		items = append(items, *byKey[k])
	}
	return items
}
