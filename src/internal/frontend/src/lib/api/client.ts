import { toast } from '../stores/toast.js'
import { animeIdFromEndpoint, isUnexpectedFailure, reportHref, type ReportContext } from '../report.js'
import { backendHealth } from '../stores/backendHealth.js'
import * as m from '../i18n/messages.js'

// Uses the page's current host when no environment variable is set
// This lets the frontend work both locally and when accessed remotely
const getApiBaseUrl = (): string => {
  if (import.meta.env.VITE_API_BASE_URL) {
    return import.meta.env.VITE_API_BASE_URL
  }

  if (typeof window === 'undefined') {
    // Fallback for non-browser environments (build time, SSR, etc.)
    return 'http://localhost:8091/api/v1'
  }

  // Uses a relative path since the frontend is served by the backend itself
  // This works both locally and through Cloudflare Tunnel/a reverse proxy
  return `${window.location.origin}/api/v1`
}

const API_BASE_URL = getApiBaseUrl()

/** Error toasts with a Report link stay longer: 4s isn't enough to read one and decide to click. */
const REPORT_TOAST_MS = 10000

interface ApiResponse<T> {
  success: boolean
  data: T
  error?: {
    code: string
    message: string
  }
}

/**
 * API error that keeps the envelope's `code`. The message is still the server's (nothing changes
 * for whoever only displays it), but whoever needs to DECIDE by the reason — the 409 of the
 * standalone POST, whose five codes become five different toasts — doesn't need to parse text for
 * it.
 */
export class ApiError extends Error {
  constructor(
    message: string,
    readonly code: string,
    readonly status: number,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

interface ApiRequestOptions {
  /** Cancels the request. The add screen's search aborts the previous one on every submit. */
  signal?: AbortSignal
  /**
   * Suppresses the automatic error toast on failure. For endpoints polled on a short interval
   * (e.g. getTorrents(), hit every 2-15s by Downloads/Status/AnimeDetail) a transient failure
   * must not spam a toast on every tick — the caller degrades silently and shows nothing instead.
   * The error is still logged and still rethrown, so callers that DO want to surface it (their
   * own try/catch + toast.error) keep working exactly as before.
   */
  silent?: boolean
  /** Sees the response of a successful request — for what the server sends in headers. */
  onResponse?: (response: Response) => void
}

async function apiRequest<T>(
  method: string,
  endpoint: string,
  body: unknown = null,
  opts: ApiRequestOptions = {},
): Promise<T> {
  const url = `${API_BASE_URL}${endpoint}`
  const options: RequestInit = {
    method,
    headers: {
      'Content-Type': 'application/json',
    },
    signal: opts.signal,
  }

  if (body) {
    options.body = JSON.stringify(body)
  }

  let responseStatus = 0
  try {
    const response = await fetch(url, options)
    responseStatus = response.status
    const data: ApiResponse<T> = await response.json()

    if (!response.ok) {
      throw new ApiError(
        data.error?.message || `HTTP error! status: ${response.status}`,
        data.error?.code ?? 'UNKNOWN',
        response.status,
      )
    }

    backendHealth.recordSuccess(endpoint)
    opts.onResponse?.(response)
    return data.data
  } catch (error) {
    console.error('API request failed:', error)
    // Before any toast: a repeated 5xx on an endpoint with `silent: true` was INVISIBLE — the app
    // degraded silently and the user only saw frozen numbers. Backend health is counted here
    // precisely because this is the only point every request goes through.
    backendHealth.recordFailure(endpoint, responseStatus)
    if (!opts.silent) {
      const isAnilistEndpoint = /\/animes\/\d+\/episodes$/.test(endpoint)
      const message =
        isAnilistEndpoint && (responseStatus === 500 || responseStatus === 0)
          ? m.api_toast_anilist()
          : error instanceof Error
            ? error.message
            : m.api_toast_unknown()
      if (isUnexpectedFailure(responseStatus, error)) {
        const raw = error instanceof Error ? error.message : String(error)
        toast.error(message, REPORT_TOAST_MS, {
          href: reportHref({
            error: raw,
            context: `${method} ${endpoint} ${responseStatus}`,
            anime: animeIdFromEndpoint(endpoint),
          }),
          label: m.report_toast_link(),
        })
      } else {
        toast.error(message)
      }
    }
    throw error
  }
}

export interface StatusResponse {
  status: string
  last_check: string
  has_error: boolean
  version: string
  disk_total: number
  disk_free: number
  /** The incomplete folder's drive, only when it is set and not the library's drive. */
  incomplete_disk_total?: number
  incomplete_disk_free?: number
  /**
   * The daemon would refuse a new torrent of size 0: free minus reserved, against
   * min_free_disk_gb. Computed on the server on purpose — a threshold duplicated in the
   * frontend would disagree with the daemon.
   */
  disk_low: boolean
  /**
   * Optional on purpose: the WebSocket's `status_update` carries only three fields, and Status
   * synthesizes a StatusResponse from it. Also absent on a daemon older than the frontend.
   * Consumers treat absence as "no information", never as "all is well".
   */
  anilist?: AnilistHealth
  /** When the daemon last crashed, while the report is unhandled. Absent on the WebSocket push. */
  pending_crash_at?: string | null
  /** A newer release the daemon's update check found, and its page; "" when none. Absent on the WebSocket push. */
  latest_version?: string
  latest_version_url?: string
}

/**
 * Last known AniList state, written by any call (the daemon's pass or the frontend's poll). A code,
 * never a ready sentence: the daemon doesn't know the browser's locale.
 */
export interface AnilistHealth {
  state: 'ok' | 'rate_limited' | 'outage' | 'app_bug'
  /** AniList's raw message — a blocked-IP 403 carries the reason written out to be read. */
  message?: string
  /** End of the 429 timeout, derived from Retry-After. Absent when it doesn't apply. */
  retry_at?: string
  since?: string
  /**
   * Date of the disk cache snapshot. Absent when there never was one. It's not a fact about
   * AniList: it's what answers "how old is what I'm seeing?" while it's down.
   */
  cache_saved_at?: string
}

export interface WebhookPreset {
  name: string
  url: string
  method: string
  headers: Record<string, string>
  body: string
  events: string[]
  /**
   * Which animes this webhook notifies about: "anilist:<user>", "mal:<user>", "standalone".
   * Absent/empty = every anime. An anime matches when any of its sources is listed.
   */
  anime_sources?: string[]
}

export interface Priorities {
  criteria_order: string[]
  fansubs: string[]
  resolutions: string[]
  sources: string[]
  codecs: string[]
  audio: string[]
  ignore_list: string[]
}

export interface Config {
  anilist_username?: string
  anilist_usernames: string[]
  mal_usernames?: string[]
  completed_anime_path: string
  check_interval: number
  max_episodes_per_anime: number
  /** Torrent size ceilings in GiB. 0 disables. */
  max_batch_torrent_size_gb: number
  max_episode_torrent_size_gb: number
  /** Seeders floor: a torrent below this is dropped from the search. 0 disables. */
  min_seeders: number
  /** Ceiling of Nyaa pages per search. The search only goes deeper if it has few candidates. */
  max_search_pages: number
  /** Minimum seconds between two Nyaa page requests, across every search. 0 disables. */
  nyaa_request_delay_seconds: number
  /** Slack the disk guard keeps on the library volume, in GiB. 0 disables the policy. */
  min_free_disk_gb: number
  episode_retry_limit: number
  max_concurrent_downloads: number
  delete_watched_episodes: boolean
  watched_episodes_to_keep: number
  excluded_list?: string
  excluded_lists: string[]
  rename_files_for_jellyfin: boolean
  /** "hardlink" (default) or "direct": write torrents straight to <Series>/Season NN/, no hardlinks. */
  library_mode: "hardlink" | "direct"
  /** Direct mode: false = keep forever (no automatic deletion, no per-anime episode limit). */
  auto_delete_downloads: boolean
  /** Direct mode: "part" writes <name>.part until the torrent completes; "in_place" doesn't. */
  incomplete_files: "part" | "in_place"
  /** Direct mode: existing folder where torrents download before moving into the library ("" = off). */
  incomplete_path: string
  incomplete_min_free_disk_gb: number
  /** Direct mode + Jellyfin renaming: "season" numbers a split cour across its season (S02E13). */
  episode_numbering: "season" | "entry"
  /** No upload at all, and each torrent is removed once organized into the library. */
  disable_seeding: boolean
  /** Asks GitHub once a day whether a newer release exists (the update badge and update_available). */
  check_for_updates: boolean
  download_statuses: string[]
  download_media_statuses: string[]
  delete_statuses: string[]
  notifications: {
    webhooks: WebhookPreset[]
    batch_window_seconds: number
  }
  priorities: Priorities
  /** "builtin" (rain, the default) or "qbittorrent". Read at startup: changing it needs a restart. */
  torrent_client?: "builtin" | "qbittorrent" | ""
  qbittorrent_url?: string
  qbittorrent_username?: string
  /** A saved secret comes back as SECRET_PLACEHOLDER; sending it back keeps the saved value. */
  qbittorrent_password?: string
  qbittorrent_api_key?: string
  qbittorrent_category?: string
  /** The same folder as the app and as qBittorrent see it. Empty = identical paths. */
  qbittorrent_path_map?: PathMapping[]
  /** The app sets qBittorrent's global settings (for now its download queue, from
   *  max_concurrent_downloads). Off, qBittorrent's own queue applies. */
  qbittorrent_manage_settings?: boolean
  /** disable_seeding's upload limit per torrent with qBittorrent, KiB/s, at least 1. */
  qbittorrent_disable_seeding_upload_kib?: number
  /** Episodes already in Shoko are never searched or downloaded. */
  shoko_enabled?: boolean
  shoko_url?: string
  /** The key Shoko gave the app at sign-in; a saved one comes back as SECRET_PLACEHOLDER. */
  shoko_api_key?: string
  /** How often Shoko is read again, in hours (at least 1). */
  shoko_refresh_hours?: number
  /** Follow Shoko's changes as they happen (its event hub); the refresh stays the safety net. */
  shoko_watch?: boolean
  /** Read all of Shoko again every N days; 0 = never on its own. */
  shoko_full_resync_days?: number
}

export interface PathMapping {
  app: string
  qbittorrent: string
}

/** What GET /config shows in place of a saved secret (files.SecretPlaceholder). */
export const SECRET_PLACEHOLDER = "__saved__"

export interface UpdateConfigResult {
  message: string
  /** The torrent client changed: it is picked at startup. */
  restart_required?: boolean
}

export interface TorrentClientTestResult {
  app_version: string
  webapi_version: string
  /** How the app signed in: the API key (used when set) or the WebUI username and password. */
  sign_in?: 'api_key' | 'password'
  library_checked: boolean
  library_visible: boolean
  library_path?: string
  /** The same check for incomplete_path, when set: qBittorrent downloads staged torrents there. */
  incomplete_checked?: boolean
  incomplete_visible?: boolean
  incomplete_path?: string
  problem?: string
  /** qBittorrent's own download queue; max_active_downloads -1 = no limit. */
  queueing_enabled?: boolean
  max_active_downloads?: number
  /** qBittorrent's disk_io_type: 0 Default, 1 memory-mapped, 2 POSIX, 3 and 4 pread. */
  disk_io_type?: number
}

export interface AnimeInfo {
  anime_id: number
  name: string
  episodes_downloaded: number
  episodes_released: number
  episodes_watched: number
  /** Released, not watched and not downloaded — the "behind" count. */
  episodes_pending: number
  total_episodes: number
  latest_episode_number: number
  last_download_date: string
  cover_image?: string
  is_blacklisted?: boolean
  /** Tracked by the standalone file, not by an AniList or MyAnimeList list. Origin, not state. */
  is_standalone?: boolean
  /**
   * Unix timestamp (seconds) of the next episode's premiere; absent when no episode is scheduled.
   * It's a timestamp, not a ready countdown, so the countdown doesn't freeze with a cached response
   * — whoever renders counts down on the client.
   */
  next_airing_at?: number
  /**
   * AniList media status (RELEASING, HIATUS, FINISHED, CANCELLED, NOT_YET_RELEASED). Copied at
   * merge; the dashboard type filter is client-side. Absent when the last merge had none.
   */
  media_status?: string
  /**
   * Other titles of the same anime (romaji, English, kanji, synonyms). Only the search uses them.
   */
  alt_names?: string[]
  /**
   * The list accounts that track this anime, each with its own progress. `episodes_watched` /
   * `episodes_pending` above are the combined view (lowest progress across accounts); the
   * dashboard user filter projects onto these instead. Absent for standalones and for orphans no
   * account tracks.
   */
  accounts?: AnimeAccount[]
}

export type AccountSource = 'anilist' | 'mal'

/** One account's progress on an anime. `source` + `username` is the identity. */
export interface AnimeAccount {
  source: AccountSource
  username: string
  episodes_watched: number
  episodes_pending: number
}

/** Reasons an anime can't be added as standalone — they come ready from the server. */
export type BlockReason = '' | 'standalone' | 'tracked' | 'downloaded'

export interface AniListSearchResult {
  id: number
  title: string
  format: string
  status: string
  year: number
  /** 0 when AniList doesn't know the total (common for an anime still airing). */
  episodes: number
  cover?: string
  block_reason: BlockReason
}

export interface LogsResponse {
  lines: string[]
}

export interface TorrentInfo {
  hash: string
  name: string
  status: string
  /** 1-based place in the download queue's waiting line; 0 = not waiting. */
  queue_position: number
  completed: boolean
  anime_name?: string
  anime_id?: number
  episode_number: number | null
  is_batch: boolean
  bytes_completed: number
  bytes_total: number
  bytes_uploaded: number
  progress: number
  download_speed: number
  upload_speed: number
  peers_total: number
  eta_seconds: number | null
  seeded_for_seconds: number
  /** A finished direct-mode torrent whose library file was deleted outside the app. */
  files_missing?: boolean
  /** 0–1, only while status is "moving": the copy from the incomplete folder into the library. */
  move_progress?: number
}

/** A file inside a torrent. */
export interface TorrentFile {
  /** RAW relative path, as in the metadata — fansub, resolution and codec come from here. */
  path: string
  size: number
  /** null = unknown (stopped torrent: rain releases the pieces). It's not "nothing downloaded". */
  bytes_completed: number | null
  /** Number extracted from the FILE NAME by the backend; null when nothing matches. */
  episode: number | null
  /**
   * Video codec read from the file HEADER (Matroska/EBML), not from the name — most releases don't
   * write the codec in the name. Absent when it can't be answered: not a `.mkv`, or not finished
   * downloading yet (rain doesn't download sequentially, so the header may not be on disk).
   */
  codec?: string
}

/** One line of the last check's report: an (anime, code) pair. */
export interface Issue {
  anime_id: number
  anime_name: string
  /** Only comes with a problem. A limit has no "affected episodes" — it has downloaded/pending. */
  episodes?: number[]
  code: string
  candidates?: number
  limit_gb?: number
  min_seeders?: number
  downloaded?: number
  pending?: number
  batch_skipped?: string
}

/** The report of the LAST pass, and only of it. It's not history. */
export interface CheckReport {
  finished_at: string
  pass_error: string
  /**
   * Classified cause of the abort. The frontend builds the sentence from HERE; `pass_error` is the
   * raw text, shown collapsed for whoever opens an issue.
   */
  pass_error_code?: string
  problems: Issue[]
  limits: Issue[]
}

export async function getStatus(opts: { silent?: boolean } = {}): Promise<StatusResponse> {
  // `silent` for pollers: the AppShell queries every 30s only to feed the banner, and a daemon
  // that's down can't become a toast every half minute.
  return apiRequest<StatusResponse>('GET', '/status', null, opts)
}

export async function getLastCheck(): Promise<CheckReport> {
  // Polled together with the torrents poll of Status and AnimeDetail: a transient failure should
  // degrade silently instead of toasting on every tick.
  return apiRequest<CheckReport>('GET', '/last-check', null, { silent: true })
}

export async function getConfig(): Promise<Config> {
  return apiRequest<Config>('GET', '/config')
}

export async function updateConfig(config: Config): Promise<UpdateConfigResult> {
  return apiRequest<UpdateConfigResult>('PUT', '/config', config)
}

/** Tests qBittorrent with the settings being edited (unsaved); a secret still at the placeholder
 * uses the saved one. Throws with the reason when it cannot connect or sign in. */
export async function testTorrentClient(config: Config): Promise<TorrentClientTestResult> {
  return apiRequest<TorrentClientTestResult>('POST', '/torrent-client/test', {
    completed_anime_path: config.completed_anime_path,
    incomplete_path: config.incomplete_path ?? '',
    qbittorrent_url: config.qbittorrent_url ?? '',
    qbittorrent_username: config.qbittorrent_username ?? '',
    qbittorrent_password: config.qbittorrent_password ?? '',
    qbittorrent_api_key: config.qbittorrent_api_key ?? '',
    qbittorrent_path_map: config.qbittorrent_path_map ?? [],
  })
}

export async function getPriorityDefaults(): Promise<Priorities> {
  return apiRequest<Priorities>('GET', '/config/priorities/defaults')
}

export interface GetAnimesOptions {
  /**
   * Called on every successful response with the accounts (`<source>:<username>` keys) whose list
   * fetch failed for it — `[]` when none did. No anime carries a failed account (X-Failed-Accounts).
   */
  onFailedAccounts?: (keys: string[]) => void
}

export async function getAnimes(opts: GetAnimesOptions = {}): Promise<AnimeInfo[]> {
  return apiRequest<AnimeInfo[]>('GET', '/animes', null, {
    onResponse: opts.onFailedAccounts
      ? (res) => {
          const header = res.headers.get('X-Failed-Accounts') ?? ''
          opts.onFailedAccounts!(header ? header.split(',') : [])
        }
      : undefined,
  })
}

export interface AnimeEpisodeInfo {
  /**
   * The episode's identity in the action routes: (anime, number). There's no episode_id anymore.
   */
  episode_number: number
  airing_at: number
  time_until_airing: number
  is_aired: boolean
  is_watched: boolean
  is_downloaded: boolean
  download_date?: string
  is_manually_managed?: boolean
  is_blocked?: boolean
  episode_hash?: string
  /** The pack's real range (from the torrent name). Absent/0 = unknown — falls back to min/max. */
  batch_start?: number
  batch_end?: number
  /** The user already has it in Shoko: never searched or downloaded. shoko_file is its path there. */
  in_shoko?: boolean
  shoko_file?: string
  /** Shoko can't reach that file right now (an unplugged drive); it still counts as in Shoko. */
  shoko_file_unavailable?: boolean
}

export interface AnimeDetailResponse {
  // anime_id is the AniList MEDIA id: it's the internal key and also builds the
  // anilist.co/anime/{id} link. There's no separate anilist_id anymore.
  anime_id: number
  total_episodes: number
  progress: number
  status: string
  cover_image?: string
  episodes: AnimeEpisodeInfo[]
  /**
   * The configured accounts that have this anime on their list, in any status, each with its own
   * entry. Absent for a standalone no account tracks.
   */
  accounts?: AnimeListAccount[]
  /** How the anime lines up with Shoko in the last pass; absent with Shoko off. */
  shoko?: AnimeShokoInfo
}

/** "matched" (series and offset), "partial" (check it by hand) or "none"; by_hand = set on this page. */
export interface AnimeShokoInfo {
  status: 'matched' | 'partial' | 'none'
  series_id?: number
  series_name?: string
  offset?: number
  by_hand?: boolean
}

/** The Shoko match set by hand: exactly one of a series (with an offset), none, or auto. */
export interface ShokoMatchRequest {
  series_id?: number
  offset?: number
  none?: boolean
  auto?: boolean
}

export interface ShokoStatus {
  enabled: boolean
  url?: string
  signed_in: boolean
  last_sync?: string
  last_full_sync?: string
  last_error?: string
  series: number
  owned_episodes: number
  /** Connected to Shoko's event hub (shoko_watch). */
  watching?: boolean
  last_event?: string
  watch_error?: string
}

/** Signs the daemon in to Shoko; only the API key Shoko returns is saved, never the password. */
export async function shokoLogin(url: string, username: string, password: string): Promise<void> {
  return apiRequest<void>('POST', '/shoko/login', { url, username, password })
}

export async function getShokoStatus(): Promise<ShokoStatus> {
  return apiRequest<ShokoStatus>('GET', '/shoko/status')
}

/** Starts a check pass that syncs with Shoko first; full reads all of Shoko again. */
export async function shokoSync(full: boolean): Promise<void> {
  return apiRequest<void>('POST', '/shoko/sync', { full })
}

export async function updateAnimeShoko(animeId: number, match: ShokoMatchRequest): Promise<void> {
  return apiRequest<void>('PUT', `/animes/${animeId}/settings`, { shoko: match })
}

/** One account's own list entry for an anime (the detail screen's "on lists" panel). */
export interface AnimeListAccount {
  source: AccountSource
  username: string
  /** List status: CURRENT, PLANNING, COMPLETED, PAUSED, DROPPED, REPEATING. */
  status: string
  progress: number
  /** 0–10 whatever the account's score format; absent = not scored. */
  score?: number
  /** The score in the account's own format; both absent when the format is unknown. */
  score_raw?: number
  /** POINT_100, POINT_10_DECIMAL, POINT_10, POINT_5 or POINT_3 (MAL: always POINT_10). */
  score_format?: string
  /** AniList avatar URL; absent for MAL. */
  avatar?: string
}

export interface AnimeSettings {
  /** Manual progress — only read for a standalone anime (a list anime's comes from its list). */
  progress?: number
}

export async function getAnimeDetail(animeId: number): Promise<AnimeDetailResponse> {
  return apiRequest<AnimeDetailResponse>('GET', `/animes/${animeId}/episodes`)
}

export async function downloadEpisode(animeId: number, episodeNumber: number): Promise<void> {
  return apiRequest<void>('POST', `/animes/${animeId}/episodes/${episodeNumber}/download`)
}

export async function deleteEpisode(animeId: number, episodeNumber: number): Promise<void> {
  return apiRequest<void>('DELETE', `/animes/${animeId}/episodes/${episodeNumber}`)
}

export async function releaseEpisode(animeId: number, episodeNumber: number): Promise<void> {
  return apiRequest<void>('POST', `/animes/${animeId}/episodes/${episodeNumber}/release`)
}

export async function redownloadEpisode(animeId: number, episodeNumber: number): Promise<void> {
  return apiRequest<void>('POST', `/animes/${animeId}/episodes/${episodeNumber}/redownload`)
}

export async function replaceEpisodeWithMagnet(animeId: number, episodeNumber: number, magnet: string): Promise<void> {
  return apiRequest<void>('POST', `/animes/${animeId}/episodes/${episodeNumber}/replace`, { magnet })
}

export async function replaceAnimeWithMagnet(animeId: number, magnet: string): Promise<void> {
  return apiRequest<void>('POST', `/animes/${animeId}/replace`, { magnet })
}

export async function updateAnimeSettings(animeId: number, settings: AnimeSettings): Promise<void> {
  return apiRequest<void>('PUT', `/animes/${animeId}/settings`, settings)
}

export async function triggerCheck(): Promise<void> {
  return apiRequest<void>('POST', '/check')
}

export async function startDaemon(): Promise<void> {
  return apiRequest<void>('POST', '/daemon/start')
}

export async function stopDaemon(): Promise<void> {
  return apiRequest<void>('POST', '/daemon/stop')
}

export async function getLogs(lines?: number): Promise<LogsResponse> {
  const endpoint = lines ? `/logs?lines=${lines}` : '/logs'
  return apiRequest<LogsResponse>('GET', endpoint)
}

export interface DiagnosticsReport {
  /** The redacted bundle: what the preview shows and the downloaded file carries. */
  bundle: string
  /** Short summary for the issue URL. */
  environment: string
}

/** `silent`: the report screen draws its own failure — it's the screen for reporting failures. */
export async function getDiagnostics(titles: boolean, ctx: ReportContext): Promise<DiagnosticsReport> {
  const params = new URLSearchParams({ titles: String(titles) })
  for (const [key, value] of Object.entries(ctx)) {
    if (value) params.set(key, value)
  }
  return apiRequest<DiagnosticsReport>('GET', `/diagnostics?${params.toString()}`, null, { silent: true })
}

/** Marks the pending crash as handled; it moves to the crashes/ folder. Idempotent. */
export async function dismissPendingCrash(): Promise<void> {
  return apiRequest<void>('DELETE', '/diagnostics/crash', null, { silent: true })
}

export async function getTorrents(): Promise<TorrentInfo[]> {
  // Polled on a short interval by Downloads.svelte (2s), Status.svelte (5s), and
  // AnimeDetail.svelte (2s/15s adaptive) — a transient failure must degrade silently rather
  // than toast on every tick.
  return apiRequest<TorrentInfo[]>('GET', '/torrents', null, { silent: true })
}

/**
 * Files inside a torrent. Fetched ON DEMAND (when the row expands), never embedded in `GET
 * /torrents`: that one is a 2s poll, and putting the list in it would multiply the payload by N for
 * data almost nobody is looking at.
 *
 * `silent` because the open panel refreshes on the screen's same poll tick.
 */
export async function getTorrentFiles(hash: string): Promise<TorrentFile[]> {
  return apiRequest<TorrentFile[]>('GET', `/torrents/${hash}/files`, null, { silent: true })
}

export async function pauseTorrent(hash: string): Promise<void> {
  return apiRequest<void>('POST', `/torrents/${hash}/pause`)
}

export async function resumeTorrent(hash: string): Promise<void> {
  return apiRequest<void>('POST', `/torrents/${hash}/resume`)
}

export async function prioritizeTorrent(hash: string): Promise<void> {
  return apiRequest<void>('POST', `/torrents/${hash}/prioritize`)
}

/**
 * Batch prioritize: ONE request, applied in the order given — N calls to `prioritizeTorrent`
 * would front-push past each other and reverse the batch. Unknown or already-completed hashes
 * are ignored by the backend, so the whole call cannot fail because one episode finished
 * between the render and the click.
 */
export async function prioritizeTorrents(hashes: string[]): Promise<void> {
  return apiRequest<void>('POST', '/torrents/prioritize', { hashes })
}

/** What the bulk "redownload missing files" did. */
export interface RedownloadMissingResult {
  redownloaded: number
  skipped_no_space: number
}

/**
 * Downloads again the missing files of finished direct-mode torrents (decisions/139): the given
 * hashes, or every such torrent when none are given.
 */
export async function redownloadMissingFiles(hashes?: string[]): Promise<RedownloadMissingResult> {
  return apiRequest<RedownloadMissingResult>('POST', '/torrents/redownload-missing', hashes ? { hashes } : undefined)
}

export async function announceTorrent(hash: string): Promise<void> {
  return apiRequest<void>('POST', `/torrents/${hash}/announce`)
}

export async function deleteTorrent(
  hash: string,
  opts: { keepData: boolean; block: boolean },
): Promise<void> {
  const params = new URLSearchParams({
    keep_data: String(opts.keepData),
    block: String(opts.block),
  })
  return apiRequest<void>('DELETE', `/torrents/${hash}?${params.toString()}`)
}

/**
 * Search on AniList. `silent` because the screen draws its own failure notice next to the results —
 * a toast on top of that would say the same thing twice.
 */
export async function searchAniList(
  q: string,
  includeUnreleased = false,
  signal?: AbortSignal,
): Promise<AniListSearchResult[]> {
  // The parameter only travels when it's true: the backend already assumes false, and sending
  // `=false` on every search only bloats the log URL.
  const unreleased = includeUnreleased ? '&include_unreleased=true' : ''
  return apiRequest<AniListSearchResult[]>(
    'GET',
    `/anilist/search?q=${encodeURIComponent(q)}${unreleased}`,
    null,
    { silent: true, signal },
  )
}

export interface TorrentCandidate {
  name: string
  magnet: string
  size_bytes: number
  seeders: number
  leechers: number
  fansub: string
  resolution: string
  is_batch: boolean
  range_start: number
  range_end: number
  nyaa_id: number
}

export interface TorrentCandidateMedia {
  title: string
  cover_image?: string
  format: string
  episodes?: number
}

export interface TorrentCandidates {
  query: string
  media: TorrentCandidateMedia
  items: TorrentCandidate[]
}

export interface TorrentCandidateDetail {
  description_html: string
  files: { name: string; size_bytes: number }[]
  nyaa_url: string
}

/**
 * Ranked whole-title packs for the picker. `silent` because the picker draws the Nyaa-down
 * state itself — a toast on top of that would say the same thing twice. `q` only travels when
 * non-empty: the backend already treats a missing param as "use the title".
 */
export async function getTorrentCandidates(
  id: number,
  query?: string,
  signal?: AbortSignal,
): Promise<TorrentCandidates> {
  const trimmed = query?.trim() ?? ''
  const q = trimmed ? `?q=${encodeURIComponent(trimmed)}` : ''
  return apiRequest<TorrentCandidates>('GET', `/animes/${id}/torrent-candidates${q}`, null, {
    silent: true,
    signal,
  })
}

/**
 * Sanitised Nyaa view page for one candidate. Fetched on select, never up front — eight
 * `/view/<id>` on mount is the cost sources.md called out. `silent`: the pane shows the error.
 */
export async function getTorrentCandidateDetail(
  id: number,
  nyaaId: number,
  signal?: AbortSignal,
): Promise<TorrentCandidateDetail> {
  return apiRequest<TorrentCandidateDetail>(
    'GET',
    `/animes/${id}/torrent-candidates/${nyaaId}`,
    null,
    { silent: true, signal },
  )
}

/**
 * Starts tracking a standalone anime and downloads whatever is already aired. Returns how many went
 * in. Optional magnet fields skip the daemon's auto-pick — the picker confirm is what fills them.
 * `silent` because the caller (AddAnime / the picker) toasts the 409s itself.
 */
export async function addStandaloneAnime(
  mediaId: number,
  opts?: { magnet?: string; name?: string; nyaa_id?: number; size_bytes?: number },
): Promise<{ added: number }> {
  const body: Record<string, unknown> = { media_id: mediaId }
  if (opts?.magnet) body.magnet = opts.magnet
  if (opts?.name) body.name = opts.name
  if (opts?.nyaa_id !== undefined) body.nyaa_id = opts.nyaa_id
  if (opts?.size_bytes !== undefined) body.size_bytes = opts.size_bytes
  return apiRequest<{ added: number }>('POST', '/standalone-animes', body, { silent: true })
}

export async function removeStandaloneAnime(mediaId: number, deleteEpisodes: boolean): Promise<void> {
  const params = new URLSearchParams({ delete_episodes: String(deleteEpisodes) })
  return apiRequest<void>('DELETE', `/standalone-animes/${mediaId}?${params.toString()}`)
}

// One request for N episodes: a pack they share is only freed when its episodes leave together.
export async function batchDeleteEpisodes(animeId: number, episodes: number[]): Promise<void> {
  return apiRequest<void>('POST', `/animes/${animeId}/episodes/batch-delete`, { episodes })
}

export interface BatchRedownloadResult {
  redownloaded: number[]
  failed: { episode: number; error: string }[]
}

// One failure does not stop the others; the result lists both.
export async function batchRedownloadEpisodes(animeId: number, episodes: number[]): Promise<BatchRedownloadResult> {
  return apiRequest<BatchRedownloadResult>('POST', `/animes/${animeId}/episodes/batch-redownload`, { episodes })
}

export interface RenderedWebhook {
  method: string
  url: string
  headers: Record<string, string>
  body: string
}

// The preset goes in the body, not by name: the form is previewed before it is saved.
export async function previewWebhook(preset: WebhookPreset): Promise<RenderedWebhook> {
  return apiRequest<RenderedWebhook>('POST', '/notifications/webhooks/preview', preset)
}

export async function testWebhook(name: string): Promise<void> {
  const res = await fetch(
    `/api/v1/notifications/webhooks/${encodeURIComponent(name)}/test`,
    { method: 'POST' }
  )
  if (!res.ok) {
    const data = await res.json()
    throw new Error(data.error?.message ?? 'Failed to test webhook')
  }
}

