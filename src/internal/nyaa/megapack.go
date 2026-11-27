package nyaa

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/PuerkitoBio/goquery"

	"AutoAnimeDownloader/src/internal/logger"
)

// Megapack signals in a release name: a torrent that holds more than one AniList entry (megapack
// spec §1). The name only proposes; the file list mapped onto the franchise confirms (daemon
// MapMegapack, decisions/163).
const (
	MegapackSignalSeasons  = "seasons"  // "S01-05", "Seasons 1-4", "Season 1+2+3"
	MegapackSignalComplete = "complete" // "Complete Series", "Complete Batch" with no season marker
	MegapackSignalExtras   = "extras"   // "+ Movies", "& OVAs", "+Specials"
)

var (
	reMegapackComplete = regexp.MustCompile(`(?i)\bcomplete\s+(?:series|collection|franchise|saga)\b`)
	reCompleteBatch    = regexp.MustCompile(`(?i)\bcomplete\s+batch\b`)
	reMegapackExtras   = regexp.MustCompile(`(?i)[+&]\s*(?:the\s+)?(?:movies?|films?|ovas?|oads?|onas?|specials?)\b`)
)

// MegapackSignals lists what name says about carrying several entries, in a fixed order. Empty for
// a name that says nothing, which is most season packs.
func MegapackSignals(name string) []string {
	var out []string
	if _, _, ok := ExtractSeasonRange(name); ok {
		out = append(out, MegapackSignalSeasons)
	}
	// "Complete Batch" with a season marker is one season's batch ("Season 2 Complete Batch").
	if reMegapackComplete.MatchString(name) || (reCompleteBatch.MatchString(name) && ExtractSeason(name) == nil) {
		out = append(out, MegapackSignalComplete)
	}
	if reMegapackExtras.MatchString(name) {
		out = append(out, MegapackSignalExtras)
	}
	return out
}

// FileListCacheFolder is the cache's folder name inside the data directory.
const FileListCacheFolder = "nyaa-files"

// fileListDir is where file lists are kept, one <nyaa id>.json each; empty means no cache (tests,
// the debug run). Set once at start by the daemon.
var fileListDir atomic.Pointer[string]

// fileListMu serializes writes to the cache folder; reads go straight to the file.
var fileListMu sync.Mutex

// SetFileListCacheDir turns on the file-list cache in dir (created on first write). A torrent's
// file list never changes, so a list is read from Nyaa once and kept forever: a 1,509-file
// megapack page costs one request in the app's lifetime for the pack range (megapack spec §1,
// decisions/163). FetchTorrentDetail writes it; PackFileRange reads it.
func SetFileListCacheDir(dir string) {
	fileListDir.Store(&dir)
}

func fileListPath(nyaaID int) string {
	dir := fileListDir.Load()
	if dir == nil || *dir == "" || nyaaID <= 0 {
		return ""
	}
	return filepath.Join(*dir, strconv.Itoa(nyaaID)+".json")
}

// cachedFileList is the kept list of nyaaID, or false when there is none.
func cachedFileList(nyaaID int) ([]TorrentFile, bool) {
	p := fileListPath(nyaaID)
	if p == "" {
		return nil, false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			logger.Logger.Debug().Err(err).Int("nyaa_id", nyaaID).Msg("Failed to read the cached file list")
		}
		return nil, false
	}
	var files []TorrentFile
	if err := json.Unmarshal(data, &files); err != nil || len(files) == 0 {
		return nil, false
	}
	return files, true
}

// storeFileList keeps a list read from Nyaa. An empty list is never kept: a page that lost its file
// list for a moment would otherwise read as an empty torrent forever.
func storeFileList(nyaaID int, files []TorrentFile) {
	p := fileListPath(nyaaID)
	if p == "" || len(files) == 0 {
		return
	}
	data, err := json.Marshal(files)
	if err != nil {
		return
	}
	fileListMu.Lock()
	defer fileListMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		logger.Logger.Warn().Err(err).Msg("Failed to create the file-list cache folder")
		return
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		logger.Logger.Warn().Err(err).Int("nyaa_id", nyaaID).Msg("Failed to cache a file list")
		return
	}
	if err := os.Rename(tmp, p); err != nil {
		logger.Logger.Warn().Err(err).Int("nyaa_id", nyaaID).Msg("Failed to cache a file list")
	}
}

// fetchViewPage loads /view/<id>, mapping a 404 to ErrTorrentNotFound and anything else to
// ErrNyaaUnavailable.
func fetchViewPage(nyaaID int) (*goquery.Document, error) {
	doc, err := fetchNyaaPage(ViewURL(nyaaID))
	if err != nil {
		var httpErr *nyaaHTTPError
		if errors.As(err, &httpErr) && httpErr.Status == http.StatusNotFound {
			return nil, fmt.Errorf("torrent %d not found: %w", nyaaID, ErrTorrentNotFound)
		}
		return nil, fmt.Errorf("failed to fetch nyaa view %d: %w", nyaaID, ErrNyaaUnavailable)
	}
	return doc, nil
}

// IsCreditlessOrMenu reports a video that is no episode of anything: a creditless opening or
// ending, a menu, a promotional video. The same test PackFileRange uses to keep them out of a
// pack's range.
func IsCreditlessOrMenu(parent, name string) bool {
	name = strings.ReplaceAll(name, "_", " ")
	parent = strings.ReplaceAll(parent, "_", " ")
	return isOPEDFolder(parent) ||
		reFileNCOPNCED.MatchString(name) || reFileOpenEnding.MatchString(name) ||
		reFileCreditless.MatchString(name) || reFileOPtoken.MatchString(name) ||
		reFilePV.MatchString(name) || reFileMenu.MatchString(name)
}

var (
	// reSpecialFolder marks a folder of movies, OVAs or specials ("Movies", "JoJo OVAs [1993]").
	reSpecialFolder = regexp.MustCompile(`(?i)\b(?:movies?|films?|gekijouban|specials?|ovas?|oads?|onas?)\b`)
	// reSpecialFile marks a file: only tags that don't occur in an episode's title ("EP 03 - Special
	// Training!" is an episode, decisions/113). Movie words count only on an unnumbered file.
	reSpecialFile = regexp.MustCompile(`(?i)\bSP\d{1,3}\b`)
	reMovieFile   = regexp.MustCompile(`(?i)\b(?:movie|film|gekijouban)\b`)
	reSeasonWord  = regexp.MustCompile(`(?i)\b(first|second|third|fourth|fifth|sixth|seventh|eighth|ninth|tenth)\s+season\b`)
)

var seasonWords = map[string]int{
	"first": 1, "second": 2, "third": 3, "fourth": 4, "fifth": 5,
	"sixth": 6, "seventh": 7, "eighth": 8, "ninth": 9, "tenth": 10,
}

// IsSpecialMarked reports a video that names itself, or its folder, a movie, OVA, ONA or special:
// it belongs to a non-TV entry of the franchise, not to the series' episode axis. parent is the
// folder the file sits in, or "" when that folder is the torrent's own: a root named
// "Anime (S1 + OVA + Specials)" says nothing about one file (decisions/113). numbered says the file
// name carries an episode number.
func IsSpecialMarked(parent, name string, numbered bool) bool {
	name = strings.ReplaceAll(name, "_", " ")
	parent = strings.ReplaceAll(parent, "_", " ")
	return isClaimedExtra(name) || reSpecialFile.MatchString(name) ||
		(!numbered && reMovieFile.MatchString(name)) || reSpecialFolder.MatchString(parent)
}

// FileSeason is the season a video sits in: from its own name (S02E05), else its folder, as a
// number ("Season 3", "S03", "2nd Season") or in words ("Third Season").
func FileSeason(parent, name string) (int, bool) {
	for _, s := range []string{name, parent} {
		if n := ExtractSeason(s); n != nil {
			return *n, true
		}
	}
	if m := reSeasonWord.FindStringSubmatch(parent); m != nil {
		return seasonWords[strings.ToLower(m[1])], true
	}
	return 0, false
}
