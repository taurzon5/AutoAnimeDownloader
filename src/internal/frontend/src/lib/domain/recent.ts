// The Recents shelf of the Status page: animes with episodes downloaded in the last
// `recent_shelf_days` and still unwatched (the API's `recent`, built after the list merge). Pure
// and i18n-free: the caption comes back as its shape, and the screen picks the message — the same
// boundary as `animeState.ts`.
import type { AnimeInfo, AnimeRecent } from '../api/client.js'

/** How many cards the shelf shows; the rest keep `recent` on the payload. Not a setting. */
export const RECENT_SHELF_CAP = 12

/** The animes on the shelf: those with `recent`, newest download first, at most `cap`. */
export function recentShelf(animes: AnimeInfo[], cap = RECENT_SHELF_CAP): AnimeInfo[] {
  return animes
    .filter((a) => a.recent && a.recent.ranges.length > 0)
    .sort((a, b) => Date.parse(b.recent!.downloaded_at) - Date.parse(a.recent!.downloaded_at))
    .slice(0, cap)
}

/** "1–3, 5": the runs, an en dash inside a run. */
export function rangesText(ranges: AnimeRecent['ranges']): string {
  return ranges.map((r) => (r.from === r.to ? String(r.from) : `${r.from}–${r.to}`)).join(', ')
}

export type RecentCaption =
  | { kind: 'episode'; n: number }
  | { kind: 'episodes'; span: string }
  | { kind: 'pack'; span: string }

/**
 * The card's caption: one episode, one pack (one run, all from one torrent), or a list of runs.
 * Several runs read as episodes even from one torrent: "1–3, 5 · pack" would claim a pack with a
 * hole.
 */
export function recentCaption(recent: AnimeRecent): RecentCaption {
  const [first] = recent.ranges
  if (recent.ranges.length === 1 && first.from === first.to) return { kind: 'episode', n: first.from }
  if (recent.ranges.length === 1 && recent.is_batch) return { kind: 'pack', span: rangesText(recent.ranges) }
  return { kind: 'episodes', span: rangesText(recent.ranges) }
}
