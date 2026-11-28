package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"time"

	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/logger"
	"AutoAnimeDownloader/src/internal/nyaa"
	"AutoAnimeDownloader/src/internal/torrents"
)

// ErrFileSelectionNeedsQBittorrent is what choosing a torrent's files returns with the built-in
// client: rain downloads every file of a torrent (decisions/165).
var ErrFileSelectionNeedsQBittorrent = errors.New("choosing which files of a torrent to download needs qBittorrent as the torrent client")

// CheckFileSelection refuses a file choice the client can't honor. No choice is always fine.
func CheckFileSelection(configs *files.Config, backend torrents.TorrentBackend, skip []string) error {
	if len(skip) == 0 {
		return nil
	}
	return selectsFiles(configs, backend)
}

// selectsFiles is nil when the running client can skip files: qBittorrent saved in the config and
// running, which a saved switch only does after a restart (the built-in session is still the
// one running until then).
func selectsFiles(configs *files.Config, backend torrents.TorrentBackend) error {
	if _, rain := backend.(*torrents.SessionManager); rain || !configs.UsesQBittorrent() || !configs.IsDirectMode() {
		return ErrFileSelectionNeedsQBittorrent
	}
	return nil
}

// pickFileList is the Nyaa file list of the pick: the cached one (the picker just read it), else
// Nyaa's page. False when there is none to read (a pasted magnet, Nyaa down).
func pickFileList(nyaaID int) ([]nyaa.TorrentFile, bool) {
	if nyaaID <= 0 {
		return nil, false
	}
	list, err := nyaa.TorrentFileList(nyaaID)
	if err != nil || len(list) == 0 {
		return nil, false
	}
	return list, true
}

// selectedBytes is what the pick downloads: its size minus the unticked files, which the disk
// guard must not count (megapack spec §6). Without the file list it is the whole size.
func (p PickedRelease) selectedBytes() int64 {
	if len(p.Skip) == 0 || p.SizeBytes <= 0 {
		return p.SizeBytes
	}
	list, ok := pickFileList(p.NyaaID)
	if !ok {
		return p.SizeBytes
	}
	skipped := torrents.SkipMatcher(p.Skip)
	var total, kept int64
	for _, f := range list {
		total += f.SizeBytes
		if !skipped(f.Name) {
			kept += f.SizeBytes
		}
	}
	if total <= 0 {
		return p.SizeBytes
	}
	return kept
}

// skippedEpisodes are the anime's episodes the pick downloads no file of: every video file the
// mapping gives that episode is unticked. Their records are not written, so the anime page and
// the pass don't count them as downloaded (decisions/165). An episode no file names keeps its
// record, as without a choice.
func skippedEpisodes(mediaID int, p PickedRelease) map[int]bool {
	if len(p.Skip) == 0 {
		return nil
	}
	list, ok := pickFileList(p.NyaaID)
	if !ok {
		return nil
	}
	return episodesWithNoFile(episodesOfFiles(mediaID, list), torrents.SkipMatcher(p.Skip))
}

// episodesOfFiles maps each video file of a torrent to the anime's own episode number: the
// megapack mapping (MapTorrentFiles, the anime's entry), else, when AniList can't give it or it
// places no file on the anime, the number in the file name. A file of another entry, or with no
// number, is left out.
func episodesOfFiles(mediaID int, list []nyaa.TorrentFile) map[string]int {
	episodeOf := map[string]int{}
	m, err := MapTorrentFiles(mediaID, list)
	if err == nil {
		for _, e := range m.Entries {
			if e.ID != mediaID {
				continue
			}
			for _, f := range e.Files {
				if f.Episode > 0 {
					episodeOf[f.Name] = f.Episode
				}
			}
		}
		if len(episodeOf) > 0 {
			return episodeOf
		}
	}
	logger.Logger.Debug().Err(err).Int("media_id", mediaID).Msg("No franchise mapping for a file choice; reading episode numbers from the names")
	for _, f := range list {
		if !nyaa.IsVideoFile(f.Name) {
			continue
		}
		if n := nyaa.ExtractEpisodeNumber(path.Base(f.Name)); n != nil && *n > 0 {
			episodeOf[f.Name] = *n
		}
	}
	return episodeOf
}

// episodesWithNoFile are the episodes whose every file is skipped.
func episodesWithNoFile(episodeOf map[string]int, skipped func(string) bool) map[int]bool {
	kept := map[int]bool{}
	gone := map[int]bool{}
	for name, ep := range episodeOf {
		if skipped(name) {
			gone[ep] = true
		} else {
			kept[ep] = true
		}
	}
	for ep := range kept {
		delete(gone, ep)
	}
	return gone
}

// withoutSkipped drops the records of the episodes the pick downloads no file of.
func withoutSkipped(records []files.EpisodeStruct, gone map[int]bool) []files.EpisodeStruct {
	if len(gone) == 0 {
		return records
	}
	out := records[:0:0]
	for _, r := range records {
		if !gone[r.EpisodeNumber] {
			out = append(out, r)
		}
	}
	return out
}

// ErrUnknownTorrentFile is what a file choice naming a path the torrent doesn't have returns.
var ErrUnknownTorrentFile = errors.New("the torrent has no such file")

// ErrEpisodeFileUnticked is what a download of an episode returns when its magnet is a torrent the
// app already has with that episode's file unticked: tick it in Downloads instead (decisions/165).
var ErrEpisodeFileUnticked = errors.New("the app already has this torrent with this episode's file unticked; tick the file in Downloads instead")

// FileChoiceResult is what changing a torrent's file choice did.
type FileChoiceResult struct {
	// Skipped and Wanted are how many files changed to not downloaded and to downloaded.
	Skipped int `json:"skipped" example:"12"`
	Wanted  int `json:"wanted" example:"0"`
	// Deleted is how many of the skipped files were on disk and were deleted. A finished file in
	// one of Shoko's import folders is never deleted (decisions/152): Kept counts those.
	Deleted int `json:"deleted" example:"12"`
	Kept    int `json:"kept" example:"0"`
	// RecordsRemoved are the episodes left with no file, whose records went (blocked too when
	// asked); RecordsAdded the episodes a wanted file brought back.
	RecordsRemoved int `json:"records_removed" example:"12"`
	RecordsAdded   int `json:"records_added" example:"0"`
}

// ChooseTorrentFiles changes which files of a placed torrent download (megapack spec §4,
// decisions/165), qBittorrent only:
//
//   - skip: priority 0. A file already written is deleted, the rest of the torrent keeps seeding
//     (measured on qBittorrent 5.x, decisions/153), unless it is finished in one of Shoko's import
//     folders or the torrent was handed to Shoko: what was written there is Shoko's
//     (decisions/152); a partial file there is still deleted, Shoko never imports it. An
//     episode of an anime the torrent was downloaded for that is left with no file loses its
//     record, so the anime page and the pass stop counting it; block also blocks it from
//     automatic download, like deleting a torrent with block.
//   - want: priority 1, qBittorrent downloads them, and the episodes they bring get their records
//     back (copied from a record of the same anime on the torrent) and are unblocked.
//
// Paths are the placement's torrent paths (GET /torrents/{hash}/files). A path the torrent
// doesn't have, or one in both lists, is ErrUnknownTorrentFile; a torrent with no file map yet is
// torrents.ErrFilesNotPlanned.
func ChooseTorrentFiles(fm FileManagerInterface, backend torrents.TorrentBackend, configs *files.Config, folders ShokoImporter, hash string, skip, want []string, block bool) (FileChoiceResult, error) {
	var result FileChoiceResult
	if len(skip) == 0 && len(want) == 0 {
		return result, nil
	}
	if err := selectsFiles(configs, backend); err != nil {
		return result, err
	}
	p, placed := backend.Placement(hash)
	if !placed || len(p.Files) == 0 {
		return result, fmt.Errorf("%w: %s", torrents.ErrFilesNotPlanned, hash)
	}
	byPath := make(map[string]torrents.PlacedFile, len(p.Files))
	for _, f := range p.Files {
		byPath[f.Torrent] = f
	}
	inSkip := map[string]bool{}
	for _, t := range skip {
		if _, ok := byPath[t]; !ok {
			return result, fmt.Errorf("%w: %s", ErrUnknownTorrentFile, t)
		}
		inSkip[t] = true
	}
	for _, t := range want {
		if _, ok := byPath[t]; !ok || inSkip[t] {
			return result, fmt.Errorf("%w: %s", ErrUnknownTorrentFile, t)
		}
	}
	var newlySkipped, newlyWanted []string
	for _, t := range skip {
		if !byPath[t].Skipped {
			newlySkipped = append(newlySkipped, t)
		}
	}
	for _, t := range want {
		if byPath[t].Skipped {
			newlyWanted = append(newlyWanted, t)
		}
	}
	if len(newlySkipped) == 0 && len(newlyWanted) == 0 {
		return result, nil
	}

	saved, err := fm.LoadSavedEpisodes()
	if err != nil {
		return result, err
	}
	// The skipped files that stay on disk, Shoko's, from this choice or an earlier one: their
	// episodes still have a file, so they keep their records (decisions/152).
	keptFiles := map[string]bool{}
	for _, f := range p.Files {
		if f.Skipped && fileExists(f.Path) {
			keptFiles[f.Torrent] = true
		}
	}
	var records []files.EpisodeStruct
	handed := false
	for _, ep := range saved {
		if ep.EpisodeHash == hash {
			records = append(records, ep)
			handed = handed || ep.HandedToShoko
		}
	}

	if len(newlySkipped) > 0 {
		if err := backend.SkipPlacedFiles(hash, newlySkipped); err != nil {
			return result, err
		}
		result.Skipped = len(newlySkipped)
		shokos := handed || (folders != nil && folders.InShoko(configs, p.RootOr(configs.CompletedAnimePath)))
		for _, t := range newlySkipped {
			for _, disk := range p.DiskNames(byPath[t]) {
				if _, err := os.Stat(disk); err != nil {
					continue
				}
				// Only a finished file is Shoko's: a ".part" or ".!qB" one is never imported, and
				// left there it would stay half-written forever.
				if shokos && disk == byPath[t].Path {
					result.Kept++
					keptFiles[t] = true
					continue
				}
				if err := os.Remove(disk); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return result, fmt.Errorf("failed to delete %s: %w", disk, err)
				}
				result.Deleted++
				logger.Logger.Info().Str("hash", hash).Str("path", disk).Msg("Deleted a file the user unticked")
			}
		}
	}
	if len(newlyWanted) > 0 {
		if err := backend.WantPlacedFiles(hash, newlyWanted); err != nil {
			return result, err
		}
		result.Wanted = len(newlyWanted)
	}

	removed, added, err := followFileChoice(fm, backend, hash, records, newlyWanted, keptFiles, block)
	result.RecordsRemoved, result.RecordsAdded = removed, added
	return result, err
}

// followFileChoice brings the records of the torrent's animes in line with its files: an episode
// whose every file is now skipped, and not kept on disk for Shoko, loses its record (and is
// blocked with block), an episode a wanted file maps to gets one back. Each anime's files are
// mapped the way the add mapped them (episodesOfFiles).
func followFileChoice(fm FileManagerInterface, backend torrents.TorrentBackend, hash string, records []files.EpisodeStruct, wanted []string, kept map[string]bool, block bool) (int, int, error) {
	if len(records) == 0 {
		return 0, 0, nil
	}
	p, _ := backend.Placement(hash)
	sizes := map[string]int64{}
	if list, err := backend.Files(hash); err == nil {
		for _, f := range list {
			sizes[f.Path] = f.Size
		}
	}
	list := make([]nyaa.TorrentFile, 0, len(p.Files))
	skippedNow := map[string]bool{}
	for _, f := range p.Files {
		list = append(list, nyaa.TorrentFile{Name: f.Torrent, SizeBytes: sizes[f.Torrent]})
		skippedNow[f.Torrent] = f.Skipped && !kept[f.Torrent]
	}
	wantedSet := map[string]bool{}
	for _, t := range wanted {
		wantedSet[t] = true
	}

	byAnime := map[int][]files.EpisodeStruct{}
	var order []int
	for _, r := range records {
		if _, seen := byAnime[r.AnimeID]; !seen {
			order = append(order, r.AnimeID)
		}
		byAnime[r.AnimeID] = append(byAnime[r.AnimeID], r)
	}
	var toDelete []files.EpisodeKey
	var toAdd []files.EpisodeStruct
	// The records that stay drop the skipped files from their library paths, as dropPackFiles
	// does; organize writes them again once wanted files are in.
	keep := p.VideoPaths()
	var refreshed []files.EpisodeStruct
	for _, animeID := range order {
		own := byAnime[animeID]
		episodeOf := episodesOfFiles(animeID, list)
		gone := episodesWithNoFile(episodeOf, func(name string) bool { return skippedNow[name] })
		recorded := map[int]bool{}
		for _, r := range own {
			recorded[r.EpisodeNumber] = true
			switch {
			case gone[r.EpisodeNumber]:
				toDelete = append(toDelete, r.Key())
			case len(r.LibraryPaths) > 0 && !slices.Equal(r.LibraryPaths, keep):
				r.LibraryPaths = keep
				refreshed = append(refreshed, r)
			}
		}
		head := own[0]
		back := map[int]bool{}
		for name, ep := range episodeOf {
			if wantedSet[name] && !recorded[ep] && !gone[ep] && !back[ep] {
				back[ep] = true
				r := head
				r.EpisodeNumber = ep
				r.EpisodeName = fmt.Sprintf("%s - Episode %d", head.AnimeName, ep)
				r.DownloadDate = time.Now()
				// Per-episode state is not the head's: the file isn't in the library nor in Shoko yet.
				r.LibraryPaths = nil
				r.HandedToShoko = false
				toAdd = append(toAdd, r)
			}
		}
	}
	for _, k := range toDelete {
		if block {
			if err := fm.BlockEpisode(k); err != nil {
				return 0, 0, err
			}
		}
	}
	if len(toDelete) > 0 {
		if err := fm.DeleteEpisodesFromFile(toDelete); err != nil {
			return 0, 0, err
		}
	}
	for _, r := range toAdd {
		if err := fm.UnblockEpisode(r.Key()); err != nil {
			logger.Logger.Warn().Err(err).Int("anime_id", r.AnimeID).Int("episode", r.EpisodeNumber).Msg("Failed to unblock an episode a wanted file brought back")
		}
	}
	if len(toAdd)+len(refreshed) > 0 {
		if err := fm.UpsertEpisodes(append(refreshed, toAdd...)); err != nil {
			return len(toDelete), 0, err
		}
	}
	return len(toDelete), len(toAdd), nil
}

// skippedInPack tells whether a pack's file for the anime's local episode is not downloaded:
// every file the pack maps to that episode is skipped.
type skippedInPack func(hash string, seriesOffset, totalEpisodes int, season *int, episode int) bool

// packSkips is the coverage adoption's view of the skipped files (decisions/165): a pack whose
// declared range covers an episode doesn't hold it when the user unticked its file, so the episode
// is not adopted. A file maps to an episode the way organize and the Shoko removal map a pack
// (nyaa.FilesInPackWindow, decisions/116), else by the number in its name. A placement is read
// once per hash.
func packSkips(backend torrents.TorrentBackend) skippedInPack {
	if backend == nil {
		return nil
	}
	type pack struct {
		videos  []string
		skipped map[string]bool
	}
	read := map[string]*pack{}
	return func(hash string, seriesOffset, totalEpisodes int, season *int, episode int) bool {
		pk, seen := read[hash]
		if !seen {
			pk = &pack{skipped: map[string]bool{}}
			if p, placed := backend.Placement(hash); placed {
				for _, f := range p.Files {
					if nyaa.IsVideoFile(f.Torrent) {
						pk.videos = append(pk.videos, f.Torrent)
						if f.Skipped {
							pk.skipped[f.Torrent] = true
						}
					}
				}
			}
			read[hash] = pk
		}
		if len(pk.skipped) == 0 {
			return false
		}
		window := nyaa.FilesInPackWindow(pk.videos, seriesOffset, totalEpisodes, season)
		if window == nil {
			window = map[string]int{}
			for _, v := range pk.videos {
				if n := nyaa.ExtractEpisodeNumber(path.Base(v)); n != nil && *n > seriesOffset {
					window[v] = *n - seriesOffset
				}
			}
		}
		held, gone := false, false
		for file, local := range window {
			if local != episode {
				continue
			}
			if pk.skipped[file] {
				gone = true
			} else {
				held = true
			}
		}
		return gone && !held
	}
}

// withoutSkipForPlacedTorrent drops a pick's file choice when the app already has that torrent
// (a re-add of a Kept one, a pack another cour holds): its placement keeps its first plan
// (decisions/136), and marking its files from this pick would take them from the anime they
// belong to. Its files are chosen from the Downloads page instead.
func withoutSkipForPlacedTorrent(backend torrents.TorrentBackend, magnet string, picked PickedRelease) PickedRelease {
	if len(picked.Skip) == 0 {
		return picked
	}
	hash, err := torrents.InfoHash(magnet)
	if err != nil {
		return picked
	}
	if _, placed := backend.Placement(hash); placed {
		logger.Logger.Info().Str("hash", hash).Int("files", len(picked.Skip)).
			Msg("The torrent is already in the app: its file choice stays as it is, the pick's is ignored")
		picked.Skip = nil
	}
	return picked
}

// repicksUnticked tells whether a candidate is a torrent the app already has with the target's
// episode unticked in it (decisions/165): adding it again would record the episode against a file
// that never downloads. For a pack target, any of the anime's episodes unticked in it counts.
func repicksUnticked(backend torrents.TorrentBackend, magnet string, target torrentTarget) bool {
	if backend == nil {
		return false
	}
	hash, err := torrents.InfoHash(magnet)
	if err != nil {
		return false
	}
	p, placed := backend.Placement(hash)
	if !placed {
		return false
	}
	unticked := false
	for _, f := range p.Files {
		unticked = unticked || (f.Skipped && nyaa.IsVideoFile(f.Torrent))
	}
	if !unticked {
		return false
	}
	offset := 0
	if target.series != nil {
		offset = target.series.Offset
	}
	total := mediaTotalEpisodes(target.anime)
	season, _ := seasonPartOf(target.anime.Media)
	skipped := packSkips(backend)
	if target.episode > 0 {
		return skipped(hash, offset, total, season, target.episode)
	}
	last := max(total, len(p.Files))
	for ep := 1; ep <= last; ep++ {
		if skipped(hash, offset, total, season, ep) {
			return true
		}
	}
	return false
}

// withoutUntickedRepicks leaves out the candidates repicksUnticked refuses.
func withoutUntickedRepicks(backend torrents.TorrentBackend, candidates []nyaa.TorrentResult, target torrentTarget) []nyaa.TorrentResult {
	out := candidates[:0:0]
	for _, c := range candidates {
		if repicksUnticked(backend, c.MagnetLink, target) {
			logger.Logger.Info().Int("anime_id", target.anime.Media.Id).Int("episode", target.episode).Str("torrent", c.Name).
				Msg("Skipping candidate: the app already has this torrent with the episode's file unticked")
			continue
		}
		out = append(out, c)
	}
	return out
}

// untickedEpisodes are the episodes, of those given, whose files are unticked in the placement of a
// torrent the app already had: a pick of it keeps that choice, so they get no record.
func untickedEpisodes(backend torrents.TorrentBackend, hash string, target torrentTarget, episodes []int) map[int]bool {
	if backend == nil || hash == "" {
		return nil
	}
	offset := 0
	if target.series != nil {
		offset = target.series.Offset
	}
	total := mediaTotalEpisodes(target.anime)
	season, _ := seasonPartOf(target.anime.Media)
	skipped := packSkips(backend)
	gone := map[int]bool{}
	for _, ep := range episodes {
		if skipped(hash, offset, total, season, ep) {
			gone[ep] = true
		}
	}
	return gone
}

func recordNumbers(records []files.EpisodeStruct) []int {
	out := make([]int, len(records))
	for i, r := range records {
		out[i] = r.EpisodeNumber
	}
	return out
}

// CheckEpisodeMagnet refuses with ErrEpisodeFileUnticked a magnet for one episode that is a
// torrent the app already has with that episode's file unticked. The episode replace calls it
// before it deletes the episode's current download. Only a placed torrent costs an AniList read
// (cached); anything else the add itself reports.
func CheckEpisodeMagnet(fm FileManagerInterface, backend torrents.TorrentBackend, configs *files.Config, animeID, episode int, magnet string) error {
	hash, err := torrents.InfoHash(magnet)
	if err != nil || backend == nil {
		return nil
	}
	if _, placed := backend.Placement(hash); !placed {
		return nil
	}
	details, err := resolveAnimeDetails(fm, animeID, configs)
	if err != nil {
		return nil
	}
	if repicksUnticked(backend, magnet, manualTarget(configs, details.mediaList, episode, false)) {
		return ErrEpisodeFileUnticked
	}
	return nil
}
