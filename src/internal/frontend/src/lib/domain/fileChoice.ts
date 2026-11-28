// Which files of a torrent download (megapack spec §4, decisions/165). Pure: the picker and the
// torrent file panel keep a Set of unticked paths and ask these for defaults, toggles and sizes.
import type { MegapackEntry, TorrentCandidateDetail } from '../api/client.js'

/**
 * The files unticked when a pick opens: for a megapack, the videos the mapping places nowhere
 * (creditless openings, menus, unknown specials) — everything it places is ticked (spec decision 3).
 * An ordinary pack starts with everything ticked.
 */
export function defaultSkip(detail: TorrentCandidateDetail | null | undefined): Set<string> {
  if (!detail?.mapping?.megapack) return new Set()
  return new Set(detail.mapping.unmapped.map((f) => f.name))
}

/** Whether every one of the names is unticked: an entry's box shows off. */
export function allSkipped(names: string[], skip: Set<string>): boolean {
  return names.length > 0 && names.every((n) => skip.has(n))
}

/** Some but not all unticked: the entry's box is indeterminate. */
export function partlySkipped(names: string[], skip: Set<string>): boolean {
  const n = names.filter((x) => skip.has(x)).length
  return n > 0 && n < names.length
}

/** Ticking an entry ticks all its files; unticking one that has any ticked unticks them all. */
export function toggleAll(names: string[], skip: Set<string>): Set<string> {
  const next = new Set(skip)
  if (allSkipped(names, skip)) names.forEach((n) => next.delete(n))
  else names.forEach((n) => next.add(n))
  return next
}

export function toggleOne(name: string, skip: Set<string>): Set<string> {
  const next = new Set(skip)
  if (next.has(name)) next.delete(name)
  else next.add(name)
  return next
}

export function entryFileNames(entry: MegapackEntry): string[] {
  return entry.files.map((f) => f.name)
}

/** What a choice downloads: the ticked files' bytes and count, against the whole torrent. */
export function selectionSize(
  files: { name: string; size_bytes: number }[],
  skip: Set<string>,
): { bytes: number; totalBytes: number; count: number; total: number } {
  let bytes = 0
  let totalBytes = 0
  let count = 0
  for (const f of files) {
    totalBytes += f.size_bytes
    if (!skip.has(f.name)) {
      bytes += f.size_bytes
      count++
    }
  }
  return { bytes, totalBytes, count, total: files.length }
}

/** The change from what the torrent has now to what the panel shows: what to skip and what to want. */
export function choiceDiff(
  files: { path: string; skipped?: boolean }[],
  skip: Set<string>,
): { skip: string[]; want: string[] } {
  const out = { skip: [] as string[], want: [] as string[] }
  for (const f of files) {
    const now = Boolean(f.skipped)
    const next = skip.has(f.path)
    if (next && !now) out.skip.push(f.path)
    if (!next && now) out.want.push(f.path)
  }
  return out
}

/**
 * The name to show for a file of the list: without the torrent's top folder when every file
 * shares it. Megapack paths start with a long release folder that fills the row, so every row
 * read the same (`[Trix] Kimetsu no Yaiba S01-05 (BD 1080p AV1…`). The full path stays on `title`.
 */
export function shownName(name: string, files: { name: string }[]): string {
  const slash = name.indexOf('/')
  if (slash < 0) return name
  const top = name.slice(0, slash + 1)
  return files.every((f) => f.name.startsWith(top)) ? name.slice(slash + 1) : name
}
