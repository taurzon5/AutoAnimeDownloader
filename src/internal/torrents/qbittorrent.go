package torrents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"AutoAnimeDownloader/src/internal/logger"
)

// QBitBackend drives an external qBittorrent 5.0+ through its Web API (torrent_client
// "qbittorrent", the qBittorrent spec). It is direct mode only: qBittorrent writes every placed
// torrent straight into the library.
//
// What the app sees comes from one poll loop on sync/maindata: List and Get read its snapshot, and
// a torrent crossing into "complete" or into an error fires the callbacks, as rain's events do.
//
// Placement is the app's (planFiles, placements.json), applied through qBittorrent: a placed
// torrent is added at the library root, stopped as soon as its file list is known
// (stopCondition=MetadataReceived), its files renamed to their planned paths (with ".part" while
// downloading when the placement asks for part files), and only then started — so qBittorrent never
// writes a byte under the torrent's own names (decisions/142). A placement that stages
// (incomplete_path) downloads into its staging folder as qBittorrent's per-torrent download path,
// and qBittorrent moves it into the library when it finishes (decisions/145).
type QBitBackend struct {
	settings     func() (QBitSettings, error)
	idPath       string
	placements   *placementStore
	pollInterval time.Duration
	// moveWait is how long FinalizePlacement waits for qBittorrent to put a finished torrent's files
	// at their final paths (renames, or a staged torrent's move) before organize retries.
	moveWait time.Duration

	mu          sync.Mutex
	client      *qbitClient
	current     QBitSettings
	savePath    string
	libraryRoot string
	pendingSwap bool
	onComplete  func(hash string)
	onFailed    func(hash string, err error)
	// raw is sync/maindata's torrent table, every category, merged update by update; decoded is the
	// same table decoded once per change, so List and Get (asked often by the web UI) decode nothing.
	raw     map[string]map[string]json.RawMessage
	decoded map[string]qbitTorrent
	rid     int64
	// complete and failed remember, per torrent seen in this run, its last completion and failure
	// state: a callback fires on a CHANGE. A torrent first seen already complete fires nothing (the
	// verification pass's reconcile handles it, as with rain).
	complete map[string]bool
	failed   map[string]bool
	// applied marks placed torrents whose files carry their planned names. awaitingStart holds the
	// ones the backend stopped at their metadata and must start once renamed; recheckFirst the ones
	// whose files may already be on disk (a redownload).
	applied       map[string]bool
	awaitingStart map[string]bool
	recheckFirst  map[string]bool
	retryApplyAt  map[string]time.Time
	// reopened holds the torrents a redownload reopened (RedownloadPlacement), until qBittorrent shows
	// it took the recheck: until then its old "complete" state is stale and must neither fire
	// onComplete nor let FinalizePlacement rename anything (decisions/140).
	reopened map[string]time.Time
	// maxActive is max_concurrent_downloads (SetMaxActiveDownloads), applied to qBittorrent's queue
	// only when the app manages its settings; queueDirty asks the next poll to apply a change.
	// diskIOTried: the disk I/O type was sent on this connection, so a qBittorrent that doesn't take
	// it is not sent it again on every pass.
	maxActive      int
	maxActiveKnown bool
	queueDirty     bool
	diskIOTried    bool
	// uploadDisabled is disable_seeding (SetUploadDisabled). floorsUsed holds every upload floor the
	// backend set in this run, so turning disable_seeding off also lifts a floor whose value changed
	// since. shareSent and uploadSent hold the limit last sent to a torrent and when, so a limit
	// qBittorrent doesn't take is not sent again on every poll.
	uploadDisabled bool
	floorsUsed     map[int64]bool
	shareSent      map[string]qbitSent
	uploadSent     map[string]qbitSent
	// stagingGone holds the staged torrents the backend stopped, held back or saw fail while their
	// incomplete folder was missing: the poll restarts them once it is back (guardStaging).
	stagingGone map[string]bool
	pollFailing bool
	stopLoop    chan struct{}
	loopDone    chan struct{}
}

// QBitSettings is what the backend needs from config.json.
type QBitSettings struct {
	URL      string
	Username string
	Password string
	APIKey   string
	Category string
	PathMap  []QBitPathMapping
	// ManageSettings lets the app set qBittorrent's global settings it relies on (for now its
	// download queue, from max_concurrent_downloads). Off, it only reads them (decisions/144).
	ManageSettings bool
	// UploadFloorKiB is disable_seeding's upload limit per torrent, in KiB/s; 0 = the default.
	UploadFloorKiB int
}

// uploadFloor is disable_seeding's upload limit in bytes per second.
func (s QBitSettings) uploadFloor() int64 {
	kib := s.UploadFloorKiB
	if kib <= 0 {
		kib = qbitDefaultUploadFloorKiB
	}
	return int64(kib) * 1024
}

// QBitPathMapping is one folder as each side sees it.
type QBitPathMapping struct {
	App         string
	QBittorrent string
}

var _ TorrentBackend = (*QBitBackend)(nil)

// qbitApplyPerPoll caps how many placed torrents one poll checks: after a restart every placed
// torrent is checked once, and a big library should not turn the first poll into hundreds of calls.
const qbitApplyPerPoll = 10

// qbitETAInfinity is qBittorrent's "no estimate" ETA (100 days).
const qbitETAInfinity = 8640000

// qbitProgressScale is how progress stands in for rain's piece bitfield in TorrentInfo: torrents/info
// has no piece counts, and the daemon reads PiecesHave > 0 as "this torrent has data on disk worth
// keeping" (decisions/024) and PiecesHave/PiecesTotal as its progress in the disk budget.
const qbitProgressScale = 10000

// qbitRecheckGrace is how long a reopened torrent may keep showing its old complete state before
// the backend stops waiting to see qBittorrent's recheck start.
const qbitRecheckGrace = 2 * time.Minute

// qbitLimitResend is how long the backend waits before sending a torrent the same limit again when
// qBittorrent still reports another one (a failed call, or a call it ignored).
const qbitLimitResend = time.Minute

// qbitSent is a limit sent to a torrent, and when.
type qbitSent struct {
	limit int64
	at    time.Time
}

// NewQBitBackend returns a backend whose state (placements.json, the download root id) lives in
// dataDir, next to where rain keeps the same files. settings is read on every Ensure, so a changed
// URL or password applies on the next pass without a restart.
func NewQBitBackend(dataDir string, settings func() (QBitSettings, error)) *QBitBackend {
	return &QBitBackend{
		settings:      settings,
		idPath:        filepath.Join(dataDir, rootIDFileName),
		placements:    newPlacementStore(filepath.Join(dataDir, placementsFileName)),
		pollInterval:  2 * time.Second,
		moveWait:      30 * time.Second,
		raw:           map[string]map[string]json.RawMessage{},
		decoded:       map[string]qbitTorrent{},
		complete:      map[string]bool{},
		failed:        map[string]bool{},
		applied:       map[string]bool{},
		awaitingStart: map[string]bool{},
		recheckFirst:  map[string]bool{},
		retryApplyAt:  map[string]time.Time{},
		reopened:      map[string]time.Time{},
		shareSent:     map[string]qbitSent{},
		uploadSent:    map[string]qbitSent{},
		floorsUsed:    map[int64]bool{},
		stagingGone:   map[string]bool{},
	}
}

func qbitContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 60*time.Second)
}

// Ensure connects to qBittorrent (again, when the settings or the download path changed), checks its
// Web API version and creates the app's category. When already connected it only pings, so a
// qBittorrent that went away fails the pass instead of the pass reading a stale snapshot.
func (b *QBitBackend) Ensure(savePath string) (bool, error) {
	if savePath == "" {
		return false, ErrSessionNotReady
	}
	settings, err := b.settings()
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(settings.URL) == "" {
		return false, errors.New("qBittorrent is the torrent client, but its URL is empty")
	}
	if settings.Category == "" {
		settings.Category = "autoanimedownloader"
	}

	b.mu.Lock()
	client := b.client
	same := client != nil && b.savePath == savePath && reflect.DeepEqual(b.current, settings)
	b.mu.Unlock()

	ctx, cancel := qbitContext()
	defer cancel()
	if same {
		if _, err := client.webAPIVersion(ctx); err != nil {
			return false, err
		}
		// Every pass, as rain's manager does: a library moved or replaced while the app runs is
		// reported on the next pass, not on the next reconnect (decisions/034).
		b.checkSwap(savePath)
		// Every pass too: a queue changed by hand in qBittorrent goes back to the app's.
		b.syncManagedSettings(ctx, client, settings)
		return false, nil
	}

	client, err = newQBitClient(settings.URL, settings.Username, settings.Password, settings.APIKey)
	if err != nil {
		return false, err
	}
	if err := client.login(ctx); err != nil {
		return false, err
	}
	version, err := client.webAPIVersion(ctx)
	if err != nil {
		return false, err
	}
	if !webAPIAtLeast(version, qbitMinWebAPI) {
		return false, fmt.Errorf("qBittorrent 5.0 or later is required (Web API %s+); it answered Web API %s", qbitMinWebAPI, version)
	}
	if err := client.createCategory(ctx, settings.Category); err != nil {
		return false, fmt.Errorf("failed to create the qBittorrent category %q: %w", settings.Category, err)
	}
	data, err := client.mainData(ctx, 0)
	if err != nil {
		return false, err
	}

	swapped, id, rootErr := checkRootMarkers(b.idPath, savePath)
	if rootErr != nil {
		logger.Logger.Warn().Err(rootErr).Str("save_path", savePath).
			Msg("Could not read the download root markers; skipping the swap check for this pass")
	}

	b.mu.Lock()
	b.client = client
	b.current = settings
	b.savePath = savePath
	b.libraryRoot = libraryRootOf(savePath)
	b.raw = map[string]map[string]json.RawMessage{}
	b.decoded = map[string]qbitTorrent{}
	b.rid = 0
	b.diskIOTried = false
	b.mergeLocked(data)
	// Everything already there is known from now on: a change fires, the present state doesn't.
	for hash, t := range b.ownTorrentsLocked() {
		b.complete[hash] = qbitIsComplete(t)
		b.failed[hash] = qbitIsFailed(t)
	}
	if swapped {
		b.pendingSwap = true
	}
	if b.stopLoop == nil {
		b.stopLoop = make(chan struct{})
		b.loopDone = make(chan struct{})
		go b.loop(b.stopLoop, b.loopDone)
	}
	b.mu.Unlock()

	writeRootMarkers(b.idPath, savePath, id)
	logger.Logger.Info().Str("webapi", version).Str("category", settings.Category).
		Msg("Connected to qBittorrent")
	b.syncManagedSettings(ctx, client, settings)
	return true, nil
}

// syncManagedSettings sets the global qBittorrent settings the app relies on, when it manages them
// (decisions/144):
//   - the download queue from max_concurrent_downloads: queueing on, that many downloads at once
//     (0 = no limit), and no cap on seeding or on active torrents, which would hold downloads back;
//   - no ".!qB" extension: the app names incomplete files itself (incomplete_files);
//   - Simple pread/pwrite disk I/O in place of Default or memory-mapped, once per connection
//     (it takes effect when qBittorrent restarts).
//
// It reads the preferences first and writes only the keys that differ. A failure is logged, not
// returned: downloads still run on qBittorrent's own settings.
func (b *QBitBackend) syncManagedSettings(ctx context.Context, client *qbitClient, settings QBitSettings) {
	b.mu.Lock()
	limit, known, diskIOTried := b.maxActive, b.maxActiveKnown, b.diskIOTried
	b.queueDirty = false
	b.mu.Unlock()
	if !settings.ManageSettings {
		return
	}
	have, err := client.managedPrefs(ctx)
	if err != nil {
		logger.Logger.Warn().Err(err).Msg("Failed to read qBittorrent's settings; it keeps its own until the next pass")
		return
	}
	want := have
	if known {
		want.qbitQueuePrefs = qbitWantedQueue(limit)
	}
	want.IncompleteFilesExt = false
	if !diskIOTried && (have.DiskIOType == qbitDiskIODefault || have.DiskIOType == qbitDiskIOMMap) {
		want.DiskIOType = qbitDiskIOSimplePread
		b.mu.Lock()
		b.diskIOTried = true
		b.mu.Unlock()
	}
	changed, err := client.setPrefs(ctx, have, want)
	if err != nil {
		logger.Logger.Warn().Err(err).Msg("Failed to set qBittorrent's settings; it keeps its own until the next pass")
		return
	}
	if changed {
		logger.Logger.Info().Int("max_active_downloads", want.MaxActiveDownloads).
			Msg("Set qBittorrent's download queue, incomplete-file extension and disk I/O type")
	}
}

// qbitWantedQueue is the queue the app gives a qBittorrent it manages.
func qbitWantedQueue(maxActive int) qbitQueuePrefs {
	downloads := maxActive
	if downloads <= 0 {
		downloads = -1
	}
	return qbitQueuePrefs{QueueingEnabled: true, MaxActiveDownloads: downloads, MaxActiveUploads: -1, MaxActiveTorrents: -1}
}

// checkRootMarkers is SessionManager.checkRoot for the qBittorrent backend: the same pair of markers,
// checked on every Ensure, so a download folder the user moved, trashed or replaced is reported the
// same way.
func checkRootMarkers(idPath, savePath string) (swapped bool, id string, err error) {
	expected, err := readRootID(idPath)
	if err != nil {
		return false, "", err
	}
	actual, err := readRootID(filepath.Join(savePath, RootMarkerName))
	if err != nil {
		return false, "", err
	}
	if expected == "" {
		fresh, err := newRootID()
		if err != nil {
			return false, "", err
		}
		return false, fresh, nil
	}
	return actual != expected, expected, nil
}

func writeRootMarkers(idPath, savePath, id string) {
	if id == "" {
		return
	}
	if err := writeRootID(idPath, id); err != nil {
		logger.Logger.Warn().Err(err).Str("path", idPath).Msg("Failed to record the download root id")
	}
	if err := writeRootID(filepath.Join(savePath, RootMarkerName), id); err != nil {
		logger.Logger.Warn().Err(err).Str("save_path", savePath).Msg("Failed to write the download root marker")
	}
}

// checkSwap latches a download-root swap and writes the expected id into the new folder, so the
// swap is reported once.
func (b *QBitBackend) checkSwap(savePath string) {
	swapped, id, err := checkRootMarkers(b.idPath, savePath)
	if err != nil {
		logger.Logger.Warn().Err(err).Str("save_path", savePath).
			Msg("Could not read the download root markers; skipping the swap check for this pass")
		return
	}
	if !swapped {
		return
	}
	b.mu.Lock()
	b.pendingSwap = true
	b.mu.Unlock()
	writeRootMarkers(b.idPath, savePath, id)
}

func (b *QBitBackend) ConsumeRootSwap() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	swapped := b.pendingSwap
	b.pendingSwap = false
	return swapped
}

// loop polls sync/maindata until stop closes.
func (b *QBitBackend) loop(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(b.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			b.poll()
		}
	}
}

type qbitEvent struct {
	hash     string
	complete bool
	err      error
}

// poll merges one sync/maindata update, fires the callbacks for what changed, and applies the
// placements of torrents whose file list just arrived.
func (b *QBitBackend) poll() {
	b.mu.Lock()
	client, rid := b.client, b.rid
	b.mu.Unlock()
	if client == nil {
		return
	}
	ctx, cancel := qbitContext()
	defer cancel()
	data, err := client.mainData(ctx, rid)
	if err != nil {
		b.mu.Lock()
		first := !b.pollFailing
		b.pollFailing = true
		b.mu.Unlock()
		if first {
			logger.Logger.Warn().Err(err).Msg("qBittorrent stopped answering; the torrent list shows the last state it sent")
		}
		return
	}

	b.mu.Lock()
	if b.pollFailing {
		logger.Logger.Info().Msg("qBittorrent answers again")
		b.pollFailing = false
	}
	b.mergeLocked(data)
	events := b.eventsLocked()
	onComplete, onFailed := b.onComplete, b.onFailed
	b.mu.Unlock()

	for _, ev := range events {
		switch {
		case ev.complete && onComplete != nil:
			onComplete(ev.hash)
		case ev.err != nil && onFailed != nil:
			onFailed(ev.hash, ev.err)
		}
	}
	b.enforceLimits(ctx, client)
	b.guardStaging(ctx, client)
	b.mu.Lock()
	dirty, settings := b.queueDirty, b.current
	b.mu.Unlock()
	if dirty {
		b.syncManagedSettings(ctx, client, settings)
	}
	b.applyPlacements(ctx)
}

// guardStaging keeps staged torrents off a missing incomplete folder (an unmounted drive). Measured
// on 5.3.0: qBittorrent creates a missing download folder when its user can write the parent, so a
// running torrent would recreate <incomplete_path> on the system disk; rain refuses to (decisions/141).
// The poll stops a running staged torrent whose folder is gone, and once the folder is back it
// rechecks and starts the ones it stopped, held back (applyPlacement) or saw fail — with rain the
// download queue restarts those on every pass, and nothing does for qBittorrent. A torrent the user
// had stopped stays stopped (decisions/145).
func (b *QBitBackend) guardStaging(ctx context.Context, client *qbitClient) {
	b.mu.Lock()
	var stop, back []string
	for hash, t := range b.ownTorrentsLocked() {
		p, ok := b.placements.get(hash)
		if !ok || p.IncompleteRoot == "" || p.Completed {
			delete(b.stagingGone, hash)
			continue
		}
		if !isDir(p.IncompleteRoot) {
			switch {
			case qbitIsFailed(t):
				b.stagingGone[hash] = true
			case !qbitIsStopped(t.State) && t.metadataKnown() && !b.stagingGone[hash]:
				// Still fetching its metadata it writes nothing, and it stops there by itself
				// (stopCondition): its renames can happen while the folder is away.
				b.stagingGone[hash] = true
				stop = append(stop, hash)
			}
			continue
		}
		if b.stagingGone[hash] {
			delete(b.stagingGone, hash)
			back = append(back, hash)
		}
	}
	b.mu.Unlock()
	sort.Strings(stop)
	if len(stop) > 0 {
		if err := client.stop(ctx, stop...); err != nil {
			logger.Logger.Warn().Err(err).Int("count", len(stop)).Msg("Failed to stop qBittorrent downloads whose incomplete folder is gone")
		} else {
			logger.Logger.Warn().Int("count", len(stop)).Msg("The incomplete downloads folder is gone; stopped the downloads in it until it is back")
		}
	}
	sort.Strings(back)
	for _, hash := range back {
		err := client.recheck(ctx, hash)
		if err == nil {
			err = client.start(ctx, hash)
		}
		if err != nil {
			logger.Logger.Warn().Err(err).Str("hash", hash).Msg("Failed to restart a qBittorrent download whose incomplete folder is back")
			continue
		}
		logger.Logger.Info().Str("hash", hash).Msg("The incomplete downloads folder is back; restarted the torrent")
	}
}

// enforceLimits keeps the app's torrents on the app's limits, whoever changed them: no share limit
// (qBittorrent's global one, or one set by hand, could stop the torrent or remove it with the
// library's files), and with disable_seeding the upload floor. Turning disable_seeding off lifts
// the floor from the torrents that still carry it, and leaves a limit set by hand alone
// (decisions/144).
func (b *QBitBackend) enforceLimits(ctx context.Context, client *qbitClient) {
	b.mu.Lock()
	now := time.Now()
	floorLimit := b.current.uploadFloor()
	if b.uploadDisabled {
		b.floorsUsed[floorLimit] = true
	}
	// due reports whether limit should be sent to the torrent: not when the same limit went out less
	// than qbitLimitResend ago. A different limit goes out at once.
	due := func(sent map[string]qbitSent, hash string, limit int64) bool {
		if last, ok := sent[hash]; ok && last.limit == limit && now.Sub(last.at) < qbitLimitResend {
			return false
		}
		sent[hash] = qbitSent{limit: limit, at: now}
		return true
	}
	var share, floor, lift []string
	for hash, t := range b.ownTorrentsLocked() {
		if t.hasShareLimit() && due(b.shareSent, hash, -1) {
			share = append(share, hash)
		}
		if t.UpLimit == nil {
			continue
		}
		switch limit := *t.UpLimit; {
		case b.uploadDisabled && limit != floorLimit && due(b.uploadSent, hash, floorLimit):
			floor = append(floor, hash)
		case !b.uploadDisabled && (limit == floorLimit || b.floorsUsed[limit]) && limit > 0 && due(b.uploadSent, hash, 0):
			lift = append(lift, hash)
		}
	}
	b.mu.Unlock()

	sort.Strings(share)
	sort.Strings(floor)
	sort.Strings(lift)
	if len(share) > 0 {
		if err := client.setNoShareLimit(ctx, share...); err != nil {
			logger.Logger.Warn().Err(err).Int("count", len(share)).Msg("Failed to remove qBittorrent's share limit from the app's torrents; retrying in a minute")
		}
	}
	if len(floor) > 0 {
		if err := client.setUploadLimit(ctx, floorLimit, floor...); err != nil {
			logger.Logger.Warn().Err(err).Int("count", len(floor)).Msg("Failed to limit the upload of the app's torrents for disable_seeding; retrying in a minute")
		}
	}
	if len(lift) > 0 {
		if err := client.setUploadLimit(ctx, 0, lift...); err != nil {
			logger.Logger.Warn().Err(err).Int("count", len(lift)).Msg("Failed to lift disable_seeding's upload limit; retrying in a minute")
		}
	}
}

// mergeLocked folds a sync/maindata answer into raw.
func (b *QBitBackend) mergeLocked(data qbitMainData) {
	if data.FullUpdate {
		b.raw = map[string]map[string]json.RawMessage{}
		b.decoded = map[string]qbitTorrent{}
	}
	for hash, fields := range data.Torrents {
		hash = strings.ToLower(hash)
		entry := b.raw[hash]
		if entry == nil {
			entry = map[string]json.RawMessage{}
			b.raw[hash] = entry
		}
		for k, v := range fields {
			entry[k] = v
		}
		entry["hash"] = json.RawMessage(fmt.Sprintf("%q", hash))
		b.decodeLocked(hash)
	}
	for _, hash := range data.TorrentsRemoved {
		hash = strings.ToLower(hash)
		b.dropLocked(hash)
	}
	b.rid = data.RID
}

func (b *QBitBackend) forgetLocked(hash string) {
	delete(b.complete, hash)
	delete(b.failed, hash)
	delete(b.applied, hash)
	delete(b.awaitingStart, hash)
	delete(b.recheckFirst, hash)
	delete(b.retryApplyAt, hash)
	delete(b.reopened, hash)
	delete(b.shareSent, hash)
	delete(b.uploadSent, hash)
	delete(b.stagingGone, hash)
}

// decodeLocked refreshes decoded[hash] from raw[hash]. An entry that doesn't decode is left out.
func (b *QBitBackend) decodeLocked(hash string) {
	t, err := decodeQBitTorrent(b.raw[hash])
	if err != nil {
		delete(b.decoded, hash)
		return
	}
	b.decoded[hash] = t
}

// dropLocked forgets a torrent qBittorrent no longer has.
func (b *QBitBackend) dropLocked(hash string) {
	delete(b.raw, hash)
	delete(b.decoded, hash)
	b.forgetLocked(hash)
}

// ownTorrentsLocked returns the torrents in the app's category.
func (b *QBitBackend) ownTorrentsLocked() map[string]qbitTorrent {
	out := map[string]qbitTorrent{}
	for hash, t := range b.decoded {
		if t.Category == b.current.Category {
			out[hash] = t
		}
	}
	return out
}

func decodeQBitTorrent(entry map[string]json.RawMessage) (qbitTorrent, error) {
	data, err := json.Marshal(entry)
	if err != nil {
		return qbitTorrent{}, err
	}
	var t qbitTorrent
	err = json.Unmarshal(data, &t)
	t.Hash = strings.ToLower(t.Hash)
	return t, err
}

// eventsLocked compares every own torrent with what was seen before and records the new state.
func (b *QBitBackend) eventsLocked() []qbitEvent {
	var events []qbitEvent
	for hash, t := range b.ownTorrentsLocked() {
		done := qbitIsComplete(t)
		if since, reopening := b.reopened[hash]; reopening {
			switch {
			case !done || qbitIsChecking(t.State):
				// qBittorrent took the recheck: from here on, complete is real.
				delete(b.reopened, hash)
				b.complete[hash] = false
			case time.Since(since) >= qbitRecheckGrace:
				delete(b.reopened, hash)
				b.complete[hash] = false
			default:
				// Still the state from before the redownload.
				done = false
			}
		}
		if prev, seen := b.complete[hash]; seen && !prev && done && !qbitIsChecking(t.State) {
			events = append(events, qbitEvent{hash: hash, complete: true})
		}
		if !qbitIsChecking(t.State) {
			b.complete[hash] = done
		}
		failing := qbitIsFailed(t)
		if failing && !b.failed[hash] {
			events = append(events, qbitEvent{hash: hash, err: b.stopError(t)})
		}
		b.failed[hash] = failing
	}
	sort.Slice(events, func(i, j int) bool { return events[i].hash < events[j].hash })
	return events
}

func qbitIsComplete(t qbitTorrent) bool {
	return t.metadataKnown() && t.Progress >= 1
}

func qbitIsFailed(t qbitTorrent) bool {
	return t.State == "error" || t.State == "missingFiles"
}

func qbitIsStopped(state string) bool {
	switch state {
	case "stoppedDL", "pausedDL", "stoppedUP", "pausedUP":
		return true
	}
	return false
}

func qbitIsChecking(state string) bool {
	switch state {
	case "checkingDL", "checkingUP", "checkingResumeData", "moving":
		return true
	}
	return false
}

// stopError is the error a failed torrent reports. Missing files of a finished placement are files
// deleted outside the app: the same ErrPlacedFileMissing rain reports, so the daemon flags the row
// instead of downloading them again (decisions/139).
func (b *QBitBackend) stopError(t qbitTorrent) error {
	if qbitIsFailed(t) {
		// The incomplete folder is gone (its drive unmounted): the daemon waits instead of dropping
		// the torrent, and the poll restarts it once the folder is back (decisions/141).
		if p, ok := b.placements.get(t.Hash); ok && p.IncompleteRoot != "" && !p.Completed && !isDir(p.IncompleteRoot) {
			return fmt.Errorf("%w: %s", ErrIncompleteFolderUnavailable, p.IncompleteRoot)
		}
	}
	switch t.State {
	case "missingFiles":
		if p, ok := b.placements.get(t.Hash); ok && p.Completed {
			return ErrPlacedFileMissing
		}
		return errors.New("qBittorrent reports the torrent's files are missing")
	case "error":
		if t.ErrorMessage != "" {
			return fmt.Errorf("qBittorrent stopped the torrent with an error: %s", t.ErrorMessage)
		}
		return errors.New("qBittorrent stopped the torrent with an error")
	}
	return nil
}

// qbitStatusSlug maps qBittorrent's torrent state onto the app's status slugs (the same contract
// with the web UI as statusSlug for rain).
func qbitStatusSlug(state string) string {
	switch state {
	case "metaDL", "forcedMetaDL":
		return StatusDownloadingMetadata
	case "downloading", "forcedDL", "stalledDL":
		return StatusDownloading
	case "queuedDL":
		return StatusQueued
	case "uploading", "stalledUP", "forcedUP", "queuedUP":
		return "seeding"
	case "checkingDL", "checkingUP", "checkingResumeData":
		return "verifying"
	case "moving":
		// The app only gives qBittorrent a download path for staging, so a move is a finished
		// staged torrent going into the library.
		return StatusMoving
	case "allocating":
		return "allocating"
	case "stoppedDL", "pausedDL", "stoppedUP", "pausedUP", "error", "missingFiles":
		return StatusStopped
	}
	return "unknown"
}

func (b *QBitBackend) toInfoLocked(t qbitTorrent) TorrentInfo {
	done := qbitIsComplete(t)
	info := TorrentInfo{
		Hash:             t.Hash,
		Name:             t.Name,
		DataDir:          qbitMapPath(t.SavePath, b.current.PathMap, false),
		Completed:        done,
		Status:           qbitStatusSlug(t.State),
		BytesCompleted:   t.Completed,
		BytesTotal:       t.Size,
		BytesUploaded:    t.Uploaded,
		DownloadSpeed:    t.DlSpeed,
		UploadSpeed:      t.UpSpeed,
		PeersTotal:       t.NumSeeds + t.NumLeechs,
		SeededForSeconds: t.SeedingTime,
		AddedAt:          time.Unix(t.AddedOn, 0),
		StopError:        b.stopError(t),
	}
	if t.metadataKnown() {
		info.PiecesTotal = qbitProgressScale
		info.PiecesHave = uint32(math.Min(math.Ceil(t.Progress*qbitProgressScale), qbitProgressScale))
	}
	if !done && t.ETA >= 0 && t.ETA < qbitETAInfinity {
		eta := t.ETA
		info.ETASeconds = &eta
	}
	if t.State == "queuedDL" && t.Priority > 0 {
		info.QueuePosition = t.Priority
	}
	if p, ok := b.placements.get(t.Hash); ok && p.IncompleteRoot != "" && !p.Completed {
		info.Staged = true
	}
	return info
}

// withMoveProgress fills MoveProgress for the torrents qBittorrent is moving into the library,
// which it doesn't report: the bytes already at the final paths, out of each file's larger copy
// (a file being copied is partly at its final path and still whole in staging). It stats the disk,
// so it runs without b.mu.
func (b *QBitBackend) withMoveProgress(infos []TorrentInfo, root string) {
	for i := range infos {
		if infos[i].Status != StatusMoving {
			continue
		}
		p, ok := b.placements.get(infos[i].Hash)
		if !ok || p.IncompleteRoot == "" || len(p.Files) == 0 {
			continue
		}
		p = p.Resolve(root)
		var moved, total int64
		for _, f := range p.Files {
			final, staging := fileSize(f.Path), fileSize(p.StagingPath(f))
			moved += final
			total += max(final, staging)
		}
		if total > 0 {
			infos[i].MoveProgress = float64(moved) / float64(total)
		}
	}
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return 0
	}
	return info.Size()
}

func (b *QBitBackend) List() []TorrentInfo {
	b.mu.Lock()
	own := b.ownTorrentsLocked()
	out := make([]TorrentInfo, 0, len(own))
	for _, t := range own {
		out = append(out, b.toInfoLocked(t))
	}
	root := b.libraryRoot
	b.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].AddedAt.Equal(out[j].AddedAt) {
			return out[i].AddedAt.Before(out[j].AddedAt)
		}
		return out[i].Hash < out[j].Hash
	})
	b.withMoveProgress(out, root)
	return out
}

func (b *QBitBackend) Get(hash string) (TorrentInfo, bool) {
	hash = strings.ToLower(hash)
	b.mu.Lock()
	t, ok := b.decoded[hash]
	if !ok || t.Category != b.current.Category {
		b.mu.Unlock()
		return TorrentInfo{}, false
	}
	info, root := []TorrentInfo{b.toInfoLocked(t)}, b.libraryRoot
	b.mu.Unlock()
	b.withMoveProgress(info, root)
	return info[0], true
}

// connection returns the client and the paths, or ErrSessionNotReady before the first Ensure.
func (b *QBitBackend) connection() (*qbitClient, QBitSettings, string, string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.client == nil {
		return nil, QBitSettings{}, "", "", ErrSessionNotReady
	}
	return b.client, b.current, b.savePath, b.libraryRoot, nil
}

// Add adds a magnet to qBittorrent in the app's category. A placed torrent goes to the library root
// and stops at its metadata; the poll loop renames its files and starts it. An unplaced one (added
// while direct mode was off) goes to the download path as it is.
func (b *QBitBackend) Add(magnet string) (string, error) {
	hash, err := InfoHash(magnet)
	if err != nil {
		return "", err
	}
	client, settings, savePath, root, err := b.connection()
	if err != nil {
		return "", err
	}

	b.mu.Lock()
	_, present := b.raw[hash]
	b.mu.Unlock()
	ctx, cancel := qbitContext()
	defer cancel()
	if present {
		if existing, ok := b.Get(hash); !ok || existing.Hash == "" {
			// The same torrent in another category: the app takes it over rather than add a duplicate
			// qBittorrent would refuse.
			if _, err := client.call(ctx, "POST", "torrents/setCategory", map[string][]string{
				"hashes": {hash}, "category": {settings.Category},
			}); err != nil {
				return "", fmt.Errorf("the torrent is already in qBittorrent and could not be moved to the %q category: %w", settings.Category, err)
			}
		}
		b.mu.Lock()
		delete(b.applied, hash)
		b.mu.Unlock()
		return hash, nil
	}

	req := qbitAddRequest{Magnet: magnet, Category: settings.Category}
	placement, placed := b.placements.get(hash)
	if placed && placement.IncompleteRoot != "" && !placement.Completed {
		req.DownloadPath = qbitMapPath(placement.StagingDir(), settings.PathMap, true)
	}
	b.mu.Lock()
	if b.uploadDisabled {
		req.UploadLimit = settings.uploadFloor()
	}
	if placed {
		req.SavePath = qbitMapPath(root, settings.PathMap, true)
		req.StopAtMetadata = true
		b.awaitingStart[hash] = true
		delete(b.applied, hash)
	} else {
		req.SavePath = qbitMapPath(savePath, settings.PathMap, true)
	}
	// Seen incomplete from the start, so finishing fires onComplete.
	b.complete[hash] = false
	b.mu.Unlock()

	if err := client.add(ctx, req); err != nil {
		b.mu.Lock()
		b.forgetLocked(hash)
		b.mu.Unlock()
		return "", err
	}
	// torrents/add answers before qBittorrent has the torrent; wait until it lists it, so the caller
	// can Get it at once, as with rain.
	for attempt := 0; attempt < 40; attempt++ {
		listed, err := client.info(ctx, "", hash)
		if err == nil && len(listed) > 0 {
			b.mu.Lock()
			b.storeInfoLocked(listed[0])
			b.mu.Unlock()
			return hash, nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return "", fmt.Errorf("qBittorrent accepted torrent %s but never listed it", hash)
}

// storeInfoLocked puts a torrents/info entry in raw, as if sync/maindata had sent it. The next
// update overwrites it with qBittorrent's own fields.
func (b *QBitBackend) storeInfoLocked(t qbitTorrent) {
	data, err := json.Marshal(t)
	if err != nil {
		return
	}
	var entry map[string]json.RawMessage
	if json.Unmarshal(data, &entry) != nil {
		return
	}
	if t.HasMetadata == nil {
		delete(entry, "has_metadata")
	}
	hash := strings.ToLower(t.Hash)
	b.raw[hash] = entry
	b.decodeLocked(hash)
}

// applyPlacements checks the placed torrents whose files may not carry their planned names yet.
func (b *QBitBackend) applyPlacements(ctx context.Context) {
	b.mu.Lock()
	now := time.Now()
	var todo []string
	for hash, t := range b.ownTorrentsLocked() {
		if b.applied[hash] || !t.metadataKnown() || now.Before(b.retryApplyAt[hash]) {
			continue
		}
		if _, placed := b.placements.get(hash); !placed {
			b.applied[hash] = true
			continue
		}
		todo = append(todo, hash)
	}
	client, settings, root := b.client, b.current, b.libraryRoot
	b.mu.Unlock()

	sort.Strings(todo)
	if len(todo) > qbitApplyPerPoll {
		todo = todo[:qbitApplyPerPoll]
	}
	for _, hash := range todo {
		if err := b.applyPlacement(ctx, client, settings, root, hash); err != nil {
			logger.Logger.Warn().Err(err).Str("hash", hash).Msg("Failed to place a qBittorrent torrent's files; retrying in a minute")
			b.mu.Lock()
			b.retryApplyAt[hash] = time.Now().Add(time.Minute)
			b.mu.Unlock()
		}
	}
}

// applyPlacement freezes the torrent's file map the first time (planFiles over qBittorrent's file
// list), renames every file whose name differs from its planned path, then rechecks and starts the
// torrent when the backend was the one holding it.
func (b *QBitBackend) applyPlacement(ctx context.Context, client *qbitClient, settings QBitSettings, root, hash string) error {
	b.mu.Lock()
	t, ok := b.decoded[hash]
	b.mu.Unlock()
	if !ok {
		return nil
	}
	if got := filepath.Clean(qbitMapPath(t.SavePath, settings.PathMap, false)); got != filepath.Clean(root) {
		// Its file names are relative to another folder: renaming them by library paths would scatter
		// them. Left alone, loudly, once.
		b.mu.Lock()
		b.applied[hash] = true
		b.mu.Unlock()
		return fmt.Errorf("qBittorrent saves it in %s, not in the library %s, so its files cannot be placed", got, root)
	}

	list, err := client.files(ctx, hash)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return nil
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Index < list[j].Index })

	b.placements.planMu.Lock()
	p, ok := b.placements.get(hash)
	if !ok {
		b.placements.planMu.Unlock()
		return nil
	}
	if len(p.Files) == 0 {
		paths := make([]string, len(list))
		for i, f := range list {
			paths[i] = f.Name
		}
		exists := func(rel string) bool { return pathExists(filepath.Join(root, rel)) }
		taken := func(rel string) bool {
			owner := b.placements.ownerOf(rel)
			return owner != "" && owner != hash
		}
		planned, err := planFiles(p.PlacementRequest, paths, exists, taken)
		if err == nil {
			p.Files = planned
			err = b.placements.update(p)
		}
		if err != nil {
			b.placements.planMu.Unlock()
			return err
		}
		logger.Logger.Info().Str("hash", hash).Str("series_dir", p.SeriesDir).Int("files", len(planned)).
			Msg("Placed torrent files in the library")
	}
	b.placements.planMu.Unlock()
	if len(p.Files) != len(list) {
		return fmt.Errorf("torrent %s has %d files but its placement maps %d", hash, len(list), len(p.Files))
	}

	onDisk := false
	resolved := p.Resolve(root)
	staged := p.IncompleteRoot != "" && !p.Completed
	for _, f := range list {
		if f.Index < 0 || f.Index >= len(p.Files) {
			return fmt.Errorf("torrent %s reports a file index %d outside its placement", hash, f.Index)
		}
		want := filepath.ToSlash(p.Files[f.Index].Path)
		switch {
		case hasSize(resolved.Files[f.Index].Path, f.Size) || (staged && hasSize(resolved.StagingPath(resolved.Files[f.Index]), f.Size)):
			onDisk = true
		case !p.Completed && p.PartFiles && !staged:
			// Staged files keep their final names: they are in the incomplete folder, out of
			// Jellyfin's sight (decisions/141).
			want += partSuffix
		}
		if f.Name == want {
			continue
		}
		if err := client.renameFile(ctx, hash, f.Name, want); err != nil {
			return fmt.Errorf("failed to rename %s to %s: %w", f.Name, want, err)
		}
	}

	b.mu.Lock()
	start := b.awaitingStart[hash]
	recheck := b.recheckFirst[hash] || (start && onDisk)
	delete(b.awaitingStart, hash)
	delete(b.recheckFirst, hash)
	delete(b.retryApplyAt, hash)
	b.applied[hash] = true
	if (start || recheck) && staged && !isDir(p.IncompleteRoot) {
		// Started now, qBittorrent would create the missing incomplete folder; guardStaging starts
		// it once the folder is back.
		b.stagingGone[hash] = true
		start, recheck = false, false
	}
	b.mu.Unlock()

	if recheck {
		if err := client.recheck(ctx, hash); err != nil {
			return err
		}
	}
	if start || recheck {
		return client.start(ctx, hash)
	}
	return nil
}

func (b *QBitBackend) Files(hash string) ([]FileInfo, error) {
	client, _, _, _, err := b.connection()
	if err != nil {
		return nil, err
	}
	ctx, cancel := qbitContext()
	defer cancel()
	list, err := client.files(ctx, hash)
	if errors.Is(err, errQBitNotFound) {
		return nil, fmt.Errorf("torrent %s not found", hash)
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Index < list[j].Index })
	p, placed := b.placements.get(hash)
	out := make([]FileInfo, 0, len(list))
	for _, f := range list {
		path := f.Name
		// The raw name from the metadata, not the planned one: it is what tells fansub and codec.
		if placed && f.Index >= 0 && f.Index < len(p.Files) {
			path = p.Files[f.Index].Torrent
		}
		done := int64(f.Progress * float64(f.Size))
		out = append(out, FileInfo{Path: path, Size: f.Size, BytesCompleted: &done})
	}
	return out, nil
}

// Remove deletes the torrent from qBittorrent. A placed torrent's files are deleted by the app, with
// the same rule as rain's placed storage (RemovePlacedFiles: the .part and .nfo names too, and the
// folders left empty); qBittorrent only drops the torrent.
func (b *QBitBackend) Remove(hash string, keepData bool) error {
	client, _, _, root, err := b.connection()
	if err != nil {
		return err
	}
	hash = strings.ToLower(hash)
	p, placed := b.placements.get(hash)
	ctx, cancel := qbitContext()
	defer cancel()
	if err := client.remove(ctx, hash, !keepData && !placed); err != nil && !errors.Is(err, errQBitNotFound) {
		return err
	}
	b.mu.Lock()
	known, listed := b.decoded[hash]
	hadMetadata := listed && known.metadataKnown()
	b.dropLocked(hash)
	b.mu.Unlock()
	if !placed || keepData {
		return nil
	}
	// rain's rule: a torrent that never got its metadata in qBittorrent wrote nothing this time, and
	// a Kept placement's files are the user's (decisions/137) — even when an earlier run froze its
	// file map. Nothing is deleted.
	if !hadMetadata && p.Kept {
		return nil
	}
	resolved := p.Resolve(root)
	// qBittorrent's "Append .!qB extension to incomplete files" puts it after the app's own names
	// (measured: "ep.mkv.part.!qB"). RemovePlacedFiles doesn't know it, and the folders it prunes
	// would not be empty.
	for _, f := range resolved.Files {
		for _, name := range []string{f.Path, f.Path + partSuffix, resolved.StagingPath(f)} {
			_ = os.Remove(name + qbitIncompleteExt)
		}
	}
	if err := RemovePlacedFiles(resolved); err != nil {
		return err
	}
	return b.placements.forget(hash)
}

// qbitIncompleteExt is the extension qBittorrent appends to incomplete files when its
// incomplete_files_ext preference is on.
const qbitIncompleteExt = ".!qB"

// Pause stops the torrent. One still waiting for its files to be renamed stays stopped afterwards.
func (b *QBitBackend) Pause(hash string) error {
	client, _, _, _, err := b.connection()
	if err != nil {
		return err
	}
	b.mu.Lock()
	delete(b.awaitingStart, hash)
	b.mu.Unlock()
	ctx, cancel := qbitContext()
	defer cancel()
	return client.stop(ctx, hash)
}

// Resume starts the torrent; qBittorrent's own queue decides whether it runs now. A placed torrent
// whose files aren't renamed yet starts once they are, never before.
func (b *QBitBackend) Resume(hash string) error {
	client, _, _, _, err := b.connection()
	if err != nil {
		return err
	}
	if b.awaitingPlacement(hash) {
		return nil
	}
	ctx, cancel := qbitContext()
	defer cancel()
	return client.start(ctx, hash)
}

// awaitingPlacement reports whether a placed torrent still waits for its renames, and if so makes
// the rename step start it.
func (b *QBitBackend) awaitingPlacement(hash string) bool {
	if _, placed := b.placements.get(hash); !placed {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.applied[hash] {
		return false
	}
	b.awaitingStart[hash] = true
	return true
}

func (b *QBitBackend) Prioritize(hash string) error {
	info, ok := b.Get(hash)
	if !ok {
		return fmt.Errorf("torrent %s not found", hash)
	}
	if info.Completed {
		return fmt.Errorf("torrent %s has already completed", hash)
	}
	return b.PrioritizeAll([]string{hash})
}

// PrioritizeAll puts the torrents at the front of qBittorrent's queue in the order received:
// topPrio one by one from the last, since each call goes in front of the previous ones.
func (b *QBitBackend) PrioritizeAll(hashes []string) error {
	client, _, _, _, err := b.connection()
	if err != nil {
		return err
	}
	var todo []string
	for _, h := range hashes {
		if info, ok := b.Get(h); ok && !info.Completed {
			todo = append(todo, info.Hash)
		}
	}
	ctx, cancel := qbitContext()
	defer cancel()
	for i := len(todo) - 1; i >= 0; i-- {
		if err := client.topPrio(ctx, todo[i]); err != nil {
			return err
		}
	}
	for _, h := range todo {
		if b.awaitingPlacement(h) {
			continue
		}
		if err := client.start(ctx, h); err != nil {
			return err
		}
	}
	return nil
}

// SetMaxActiveDownloads records max_concurrent_downloads. It reaches qBittorrent's queue only when
// the app manages its settings: on the next Ensure, or on the next poll when it changed (PUT /config
// applies it at once). Otherwise qBittorrent's own queue decides.
func (b *QBitBackend) SetMaxActiveDownloads(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.maxActiveKnown && b.maxActive == n {
		return
	}
	b.maxActive, b.maxActiveKnown = n, true
	b.queueDirty = true
}

// SetUploadDisabled records disable_seeding: new torrents are added with the upload floor, and the
// poll loop puts the floor on (or lifts it from) the torrents already there.
func (b *QBitBackend) SetUploadDisabled(disabled bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.uploadDisabled = disabled
}

// SetDirectMode is a no-op: this backend is always direct mode.
func (b *QBitBackend) SetDirectMode(bool) {}

func (b *QBitBackend) SetPlacement(req PlacementRequest) error {
	_, err := b.placements.register(req)
	return err
}

func (b *QBitBackend) Placement(hash string) (Placement, bool) {
	p, ok := b.placements.get(hash)
	if !ok {
		return Placement{}, false
	}
	b.mu.Lock()
	root := b.libraryRoot
	b.mu.Unlock()
	return p.Resolve(root), true
}

func (b *QBitBackend) Placements() []Placement {
	b.mu.Lock()
	root := b.libraryRoot
	b.mu.Unlock()
	stored := b.placements.all()
	out := make([]Placement, len(stored))
	for i, p := range stored {
		out[i] = p.Resolve(root)
	}
	return out
}

// RedownloadPlacement reopens a finished placement and makes qBittorrent check the files and download
// what is missing: the missing ones become .part again (when the placement uses part files), the
// whole ones keep their names. Always in place, incomplete_path or not: qBittorrent keeps a
// torrent's files in one place, and a download path set while it is incomplete would move every
// file of it, the whole ones too, out of the library and back (measured on 5.3.0; decisions/145).
func (b *QBitBackend) RedownloadPlacement(hash, _ string) error {
	client, err := b.connectionHash(hash)
	if err != nil {
		return err
	}
	// Before the recheck makes it incomplete: a download path left from staging would take effect.
	ctx, cancel := qbitContext()
	defer cancel()
	if err := client.setDownloadPath(ctx, hash, ""); err != nil {
		return err
	}
	if err := b.placements.reopen(hash, ""); err != nil {
		return err
	}
	b.mu.Lock()
	delete(b.applied, hash)
	b.recheckFirst[hash] = true
	b.reopened[hash] = time.Now()
	b.mu.Unlock()
	return nil
}

// connectionHash checks that the backend is connected and qBittorrent has the torrent.
func (b *QBitBackend) connectionHash(hash string) (*qbitClient, error) {
	client, _, _, _, err := b.connection()
	if err != nil {
		return nil, err
	}
	if _, ok := b.Get(hash); !ok {
		return nil, fmt.Errorf("torrent %s is not in qBittorrent", hash)
	}
	return client, nil
}

// MovePlacementStaging follows an incomplete folder the user moved (decisions/141): the placement
// and qBittorrent's download path point at the new folder, and a recheck finds the moved data.
// Measured on 5.3.0: setting the path keeps files already there.
func (b *QBitBackend) MovePlacementStaging(hash, root string) error {
	client, settings, _, _, err := b.connection()
	if err != nil {
		return err
	}
	if err := b.placements.setIncompleteRoot(hash, root); err != nil {
		return err
	}
	p, _ := b.placements.get(hash)
	ctx, cancel := qbitContext()
	defer cancel()
	if err := client.setDownloadPath(ctx, hash, qbitMapPath(p.StagingDir(), settings.PathMap, true)); err != nil {
		return err
	}
	return client.recheck(ctx, hash)
}

// FinalizePlacement gives a finished placed torrent's .part files their final names (through
// qBittorrent, which renames them on disk) and marks the placement completed. Idempotent.
func (b *QBitBackend) FinalizePlacement(hash string) error {
	p, ok := b.placements.get(hash)
	if !ok || p.Completed {
		return nil
	}
	client, err := b.connectionHash(hash)
	if err != nil {
		return err
	}
	// Settled, like rain's FinalizePlacement (decisions/140): complete, not checking or moving, and not
	// a reopened torrent whose "complete" is the state from before its recheck.
	b.mu.Lock()
	t, listed := b.decoded[hash]
	_, reopening := b.reopened[hash]
	root := b.libraryRoot
	b.mu.Unlock()
	if !listed || !qbitIsComplete(t) || qbitIsChecking(t.State) || reopening {
		return fmt.Errorf("torrent %s is not complete yet", hash)
	}
	if p.IncompleteRoot != "" {
		return b.finalizeStaged(client, hash, p, root)
	}

	if p.PartFiles && len(p.Files) > 0 {
		ctx, cancel := qbitContext()
		defer cancel()
		list, err := client.files(ctx, hash)
		if err != nil {
			return err
		}
		for _, f := range list {
			if f.Index < 0 || f.Index >= len(p.Files) {
				continue
			}
			final := filepath.ToSlash(p.Files[f.Index].Path)
			if f.Name == final+partSuffix {
				if err := client.renameFile(ctx, hash, f.Name, final); err != nil {
					return fmt.Errorf("failed to give %s its final name: %w", final, err)
				}
			}
		}
		// qBittorrent renames on disk asynchronously; the library paths must exist before organize
		// records them.
		sizes := map[int]int64{}
		for _, f := range list {
			sizes[f.Index] = f.Size
		}
		if !qbitWaitFor(b.moveWait, func() bool {
			for i, pf := range p.Files {
				if !hasSize(filepath.Join(root, pf.Path), sizes[i]) {
					return false
				}
			}
			return true
		}) {
			return fmt.Errorf("torrent %s: qBittorrent has not renamed the files to their final names yet", hash)
		}
	}
	p.Completed = true
	return b.placements.update(p)
}

// finalizeStaged finishes a staged placement. qBittorrent moves a finished torrent from its download
// path into the library itself, so this waits for every file at its final path, then drops the
// torrent's download path: left set, any recheck that made the torrent incomplete again would move
// all of its files out of the library and back (decisions/145). A move still running is an error,
// and organize retries.
func (b *QBitBackend) finalizeStaged(client *qbitClient, hash string, p Placement, root string) error {
	ctx, cancel := qbitContext()
	defer cancel()
	list, err := client.files(ctx, hash)
	if err != nil {
		return err
	}
	sizes := map[int]int64{}
	for _, f := range list {
		sizes[f.Index] = f.Size
	}
	if !qbitWaitFor(b.moveWait, func() bool {
		for i, pf := range p.Files {
			if !hasSize(filepath.Join(root, pf.Path), sizes[i]) {
				return false
			}
		}
		return true
	}) {
		return fmt.Errorf("qBittorrent is still moving torrent %s into the library", hash)
	}
	if err := client.setDownloadPath(ctx, hash, ""); err != nil {
		return fmt.Errorf("failed to clear the download path of torrent %s: %w", hash, err)
	}
	p.Completed = true
	return b.placements.update(p)
}

func qbitWaitFor(timeout time.Duration, ok func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if ok() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (b *QBitBackend) ForgetPlacement(hash string) error {
	return b.placements.forget(hash)
}

func (b *QBitBackend) SetPlacementKept(hash string, kept bool) error {
	return b.placements.setKept(hash, kept)
}

func (b *QBitBackend) Announce(hash string) error {
	client, _, _, _, err := b.connection()
	if err != nil {
		return err
	}
	ctx, cancel := qbitContext()
	defer cancel()
	return client.reannounce(ctx, hash)
}

func (b *QBitBackend) SetCallbacks(onComplete func(hash string), onFailed func(hash string, err error)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onComplete = onComplete
	b.onFailed = onFailed
}

// Close stops the poll loop and ends the qBittorrent session. qBittorrent keeps every torrent.
func (b *QBitBackend) Close() error {
	b.mu.Lock()
	stop, done, client := b.stopLoop, b.loopDone, b.client
	b.stopLoop, b.loopDone = nil, nil
	b.mu.Unlock()
	if stop != nil {
		close(stop)
		<-done
	}
	if client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		client.logout(ctx)
	}
	return nil
}

// qbitMapPath translates a path between the app's and qBittorrent's view through the longest
// matching prefix of the path map. No match, or no map, leaves it as it is.
func qbitMapPath(path string, maps []QBitPathMapping, toQBit bool) string {
	best, bestLen := -1, -1
	for i, m := range maps {
		from := m.App
		if !toQBit {
			from = m.QBittorrent
		}
		from = strings.TrimRight(from, `/\`)
		if from == "" {
			continue
		}
		if path == from || strings.HasPrefix(path, from+"/") || strings.HasPrefix(path, from+`\`) {
			if len(from) > bestLen {
				best, bestLen = i, len(from)
			}
		}
	}
	if best < 0 {
		return path
	}
	from, to := maps[best].App, maps[best].QBittorrent
	if !toQBit {
		from, to = to, from
	}
	return strings.TrimRight(to, `/\`) + path[len(strings.TrimRight(from, `/\`)):]
}

// QBitTestResult is what the connection test found.
type QBitTestResult struct {
	AppVersion    string `json:"app_version"`
	WebAPIVersion string `json:"webapi_version"`
	// SignIn is how the app signed in: "api_key" (sent as a Bearer token, used when set) or
	// "password" (the WebUI login).
	SignIn string `json:"sign_in"`
	// LibraryChecked and LibraryVisible: whether qBittorrent sees the library the app writes, proved
	// with a probe file the app writes there. Not checked without a library path.
	LibraryChecked bool   `json:"library_checked"`
	LibraryVisible bool   `json:"library_visible"`
	LibraryPath    string `json:"library_path,omitempty"` // the library as qBittorrent sees it
	// IncompleteChecked, IncompleteVisible and IncompletePath: the same for incomplete_path, when
	// set — qBittorrent downloads staged torrents there (decisions/145).
	IncompleteChecked bool   `json:"incomplete_checked"`
	IncompleteVisible bool   `json:"incomplete_visible"`
	IncompletePath    string `json:"incomplete_path,omitempty"`
	Problem           string `json:"problem,omitempty"`
	// QueueingEnabled and MaxActiveDownloads are qBittorrent's own download queue (-1 = no limit):
	// what applies instead of max_concurrent_downloads when the app doesn't manage its settings.
	// Absent when qBittorrent didn't answer its preferences.
	QueueingEnabled    *bool `json:"queueing_enabled,omitempty"`
	MaxActiveDownloads *int  `json:"max_active_downloads,omitempty"`
	// DiskIOType is qBittorrent's disk_io_type: 0 Default and 1 memory-mapped are the ones the app
	// would change when it manages the settings.
	DiskIOType *int `json:"disk_io_type,omitempty"`
}

// TestQBittorrent signs in, reads the versions and checks that qBittorrent sees the library and the
// incomplete folder (when set): the connection test behind POST /api/v1/torrent-client/test. An
// error means it could not connect; a connected qBittorrent with a problem (too old, a folder not
// visible) reports it in Problem.
func TestQBittorrent(settings QBitSettings, libraryRoot, incompleteRoot string) (QBitTestResult, error) {
	var out QBitTestResult
	client, err := newQBitClient(settings.URL, settings.Username, settings.Password, settings.APIKey)
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.login(ctx); err != nil {
		return out, err
	}
	defer client.logout(ctx)
	out.SignIn = "password"
	if client.apiKey != "" {
		out.SignIn = "api_key"
	}
	if out.WebAPIVersion, err = client.webAPIVersion(ctx); err != nil {
		return out, err
	}
	out.AppVersion, _ = client.appVersion(ctx)
	if !webAPIAtLeast(out.WebAPIVersion, qbitMinWebAPI) {
		out.Problem = fmt.Sprintf("qBittorrent 5.0 or later is required (Web API %s+)", qbitMinWebAPI)
		return out, nil
	}
	if prefs, err := client.managedPrefs(ctx); err == nil {
		out.QueueingEnabled, out.MaxActiveDownloads = &prefs.QueueingEnabled, &prefs.MaxActiveDownloads
		out.DiskIOType = &prefs.DiskIOType
	}
	if libraryRoot == "" {
		return out, nil
	}
	out.LibraryChecked = true
	out.LibraryPath = qbitMapPath(libraryRoot, settings.PathMap, true)
	if out.LibraryVisible, out.Problem = probeVisible(ctx, client, libraryRoot, out.LibraryPath, "the library"); out.Problem != "" {
		return out, nil
	}
	if incompleteRoot != "" {
		out.IncompleteChecked = true
		out.IncompletePath = qbitMapPath(incompleteRoot, settings.PathMap, true)
		out.IncompleteVisible, out.Problem = probeVisible(ctx, client, incompleteRoot, out.IncompletePath, "the incomplete folder")
	}
	return out, nil
}

// probeVisible writes a probe file in dir and checks qBittorrent lists it at qbitDir, dir as
// qBittorrent sees it. A problem says what is wrong; "" means visible.
func probeVisible(ctx context.Context, client *qbitClient, dir, qbitDir, what string) (bool, string) {
	probe, err := writeProbe(dir)
	if err != nil {
		return false, fmt.Sprintf("the app cannot write to %s: %v", what, err)
	}
	defer os.Remove(probe)
	listed, err := client.directoryContent(ctx, qbitDir)
	if err != nil {
		return false, fmt.Sprintf("qBittorrent cannot open %s: %v", qbitDir, err)
	}
	for _, entry := range listed {
		if filepath.Base(strings.ReplaceAll(entry, `\`, "/")) == filepath.Base(probe) {
			return true, ""
		}
	}
	return false, fmt.Sprintf("qBittorrent sees %s, but not the file the app just wrote in %s there: the two are not the same folder (check the path map)", qbitDir, what)
}

func writeProbe(dir string) (string, error) {
	id, err := newRootID()
	if err != nil {
		return "", err
	}
	// No leading dot: qBittorrent's directory listing hides dotfiles, so a hidden probe would always
	// read as "not visible".
	path := filepath.Join(dir, "aad-qbittorrent-probe-"+id[:8]+".tmp")
	return path, os.WriteFile(path, nil, 0644)
}
