import { writable } from 'svelte/store'
import type { ApiError, TorrentCandidate, TorrentCandidateMedia } from '../api/client.js'

/**
 * In-memory only: Add fills this before pushing `#/add/:id/torrents` so the first paint is
 * free. A reload of that URL is a cache miss and fetches. Confirm clears it. Nothing is
 * written to localStorage — a pack list is not a preference.
 */
export interface PickerCache {
  mediaId: number
  query: string
  items: TorrentCandidate[]
  error: ApiError | null
  media?: TorrentCandidateMedia
  /**
   * The user confirmed taking the anime off the app's blacklist on the Add screen: the picker's add
   * sends it, or the daemon refuses with BLACKLISTED (decisions/155).
   */
  unblacklist?: boolean
  /** The download queue chosen on the Add screen (decisions/164); absent = the default queue. */
  queue?: string
}

const store = writable<PickerCache | null>(null)

export const pickerCache = {
  subscribe: store.subscribe,
  set: (value: PickerCache | null) => store.set(value),
  clear: () => store.set(null),
}
