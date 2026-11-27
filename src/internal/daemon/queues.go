package daemon

import (
	"sync/atomic"

	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/logger"
	"AutoAnimeDownloader/src/internal/nyaa"
)

// queueStore is queues.json, set once at start (SetQueueStore). Package state for the same reason
// as releaseStore: the search sits deep in the pass and in the API's manual downloads and previews,
// and none of them carries State. Unset (tests, debug runs), every anime is in the default queue.
var queueStore atomic.Pointer[files.QueueStore]

// SetQueueStore is called once by the daemon's main.
func SetQueueStore(s *files.QueueStore) { queueStore.Store(s) }

// QueueStore is the store the API edits the queues through; nil when unset.
func QueueStore() *files.QueueStore { return queueStore.Load() }

// loadQueues is the queues in their order; nil when there is no store or it can't be read, which
// reads as "everything in the default queue".
func loadQueues() []files.DownloadQueue {
	s := queueStore.Load()
	if s == nil {
		return nil
	}
	list, err := s.All()
	if err != nil {
		logger.Logger.Warn().Err(err).Msg("Failed to read the download queues, using the default queue for every anime")
		return nil
	}
	return list
}

// queueRanking is mediaID's queue's own ranking, nil for the default queue (the active priorities).
func queueRanking(mediaID int) *nyaa.Priorities {
	_, q := files.QueueOf(loadQueues(), mediaID)
	return q.Priorities
}

// queueGroups splits the pass's anime by queue, in the queues' order: the pass searches one group
// after the other, so a later queue's searches wait for the earlier queues' (decisions/164). The
// download limit stays shared: a torrent found by a later queue still starts as soon as a slot is
// free. Within a group the anime keep the list's order.
func queueGroups(animes []anilist.MediaList, queues []files.DownloadQueue) [][]anilist.MediaList {
	if len(queues) <= 1 {
		return [][]anilist.MediaList{animes}
	}
	groups := make([][]anilist.MediaList, len(queues))
	for _, a := range animes {
		pos, _ := files.QueueOf(queues, a.Media.Id)
		groups[pos] = append(groups[pos], a)
	}
	out := groups[:0]
	for _, g := range groups {
		if len(g) > 0 {
			out = append(out, g)
		}
	}
	return out
}
