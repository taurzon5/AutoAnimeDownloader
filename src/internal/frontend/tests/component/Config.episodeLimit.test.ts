// Max Episodes per Anime as a switch: one number in config.json, 0 meaning off, drawn as a switch
// with the amount under it. Greyed out while direct mode keeps downloads forever (the daemon ignores
// the limit then: Config.LiftsEpisodeLimit).
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'
import { tick } from 'svelte'
import Config from '../../src/routes/Config.svelte'
import * as client from '../../src/lib/api/client.js'

vi.mock('../../src/lib/api/client.js', () => ({
  getConfig: vi.fn(),
  updateConfig: vi.fn(),
  triggerCheck: vi.fn(),
  testTorrentClient: vi.fn(),
  shokoLogin: vi.fn(),
  getShokoStatus: vi.fn(),
  shokoSync: vi.fn(),
  SECRET_PLACEHOLDER: '__saved__',
}))

const flush = async () => {
  for (let i = 0; i < 5; i++) {
    await tick()
    await Promise.resolve()
  }
}

// A config as GET /config sends it: every field present, so nothing reads as unsaved on load.
// Hardlink mode, so the limit applies.
const saved = {
  anilist_usernames: [], mal_usernames: [], completed_anime_path: '/mnt/anime', check_interval: 10,
  max_episodes_per_anime: 0, max_batch_torrent_size_gb: 100, max_episode_torrent_size_gb: 5, min_seeders: 3,
  max_search_pages: 5, nyaa_request_delay_seconds: 0, episode_retry_limit: 5, max_concurrent_downloads: 3,
  watched_episodes_to_keep: 2, min_free_disk_gb: 10, incomplete_min_free_disk_gb: 10, library_mode: 'hardlink',
  incomplete_files: 'part', incomplete_path: '', download_roots: [], rename_files_for_jellyfin: false,
  episode_numbering: 'season', auto_delete_downloads: false, check_for_updates: false, recent_shelf_days: 3,
  keep_removed_release_cards: true, torrent_client: 'builtin', shoko_enabled: false, shoko_url: '',
  shoko_api_key: '', shoko_refresh_hours: 24, shoko_watch: true, shoko_full_resync_days: 30, shoko_full_packs: true,
  delete_watched_episodes: true, notifications: { webhooks: [], batch_window_seconds: 60 },
}

beforeEach(() => {
  vi.mocked(client.getConfig).mockResolvedValue(structuredClone(saved) as never)
  vi.mocked(client.updateConfig).mockReset().mockResolvedValue({ message: 'ok' } as never)
  vi.mocked(client.getShokoStatus).mockReset().mockResolvedValue({ enabled: false, signed_in: false, series: 0, owned_episodes: 0 } as never)
})

async function openDownloads(overrides: Record<string, unknown> = {}) {
  vi.mocked(client.getConfig).mockResolvedValue({ ...structuredClone(saved), ...overrides } as never)
  render(Config)
  await flush()
  await fireEvent.click(screen.getByRole('button', { name: /^Downloads/ }))
  await flush()
}

const limitSwitch = () => document.getElementById('episode_limit') as HTMLInputElement
const amount = () => document.getElementById('max_episodes_per_anime') as HTMLInputElement | null

const submit = async () => {
  await fireEvent.submit(document.querySelector('form') as HTMLFormElement)
  await flush()
}

describe('Config — episode limit switch', () => {
  it('a saved 0 shows the switch off and no amount', async () => {
    await openDownloads()
    expect(limitSwitch().checked).toBe(false)
    expect(amount()).toBeNull()
  })

  it('a saved limit shows the switch on with its amount', async () => {
    await openDownloads({ max_episodes_per_anime: 8 })
    expect(limitSwitch().checked).toBe(true)
    expect(amount()?.value).toBe('8')
  })

  it('turning it off saves 0, and turning it back on restores the amount', async () => {
    await openDownloads({ max_episodes_per_anime: 8 })
    await fireEvent.click(limitSwitch())
    await flush()
    expect(amount()).toBeNull()
    await fireEvent.click(limitSwitch())
    await flush()
    expect(amount()?.value).toBe('8')
    await fireEvent.click(limitSwitch())
    await flush()
    await submit()
    expect(client.updateConfig).toHaveBeenCalledWith(expect.objectContaining({ max_episodes_per_anime: 0 }))
  })

  it('turning it on from off starts at 12', async () => {
    await openDownloads()
    await fireEvent.click(limitSwitch())
    await flush()
    expect(amount()?.value).toBe('12')
  })

  it('an amount of 0 with the switch on stays on screen and blocks Save', async () => {
    await openDownloads({ max_episodes_per_anime: 8 })
    await fireEvent.input(amount() as HTMLInputElement, { target: { value: '0' } })
    await flush()
    expect(amount()).not.toBeNull()
    await submit()
    expect(client.updateConfig).not.toHaveBeenCalled()
  })

  it('is greyed out while direct mode keeps downloads forever', async () => {
    await openDownloads({ library_mode: 'direct', auto_delete_downloads: false, max_episodes_per_anime: 8 })
    expect(limitSwitch().disabled).toBe(true)
    expect(limitSwitch().checked).toBe(false)
    expect(amount()).toBeNull()
    expect(screen.getByText(/no limit applies/)).toBeTruthy()
  })
})
