// The settings layout from the 2026-11-25 UI review: a General group, the Cleanup block under Delete
// Downloads Automatically, Shoko's sign-in before its switch, and what qBittorrent's manage switch
// applies.
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
const saved = {
  anilist_usernames: [], mal_usernames: [], completed_anime_path: '/mnt/anime', check_interval: 10,
  max_episodes_per_anime: 0, max_batch_torrent_size_gb: 100, max_episode_torrent_size_gb: 5, min_seeders: 3,
  max_search_pages: 5, nyaa_request_delay_seconds: 0, episode_retry_limit: 5, max_concurrent_downloads: 3,
  watched_episodes_to_keep: 2, min_free_disk_gb: 10, incomplete_min_free_disk_gb: 10, library_mode: 'direct',
  incomplete_files: 'part', incomplete_path: '', download_roots: [], rename_files_for_jellyfin: false,
  episode_numbering: 'season', auto_delete_downloads: false, check_for_updates: false, recent_shelf_days: 3,
  keep_removed_release_cards: true, torrent_client: 'builtin', shoko_enabled: false, shoko_url: '',
  shoko_api_key: '', shoko_refresh_hours: 24, shoko_watch: true, shoko_full_resync_days: 30, shoko_full_packs: true,
  delete_watched_episodes: true, disable_seeding: false, notifications: { webhooks: [], batch_window_seconds: 60 },
}

beforeEach(() => {
  vi.mocked(client.updateConfig).mockReset().mockResolvedValue({ message: 'ok' } as never)
  vi.mocked(client.getShokoStatus).mockReset().mockResolvedValue({ enabled: false, signed_in: false, series: 0, owned_episodes: 0 } as never)
})

async function open(overrides: Record<string, unknown> = {}, group?: RegExp) {
  vi.mocked(client.getConfig).mockResolvedValue({ ...structuredClone(saved), ...overrides } as never)
  render(Config)
  await flush()
  if (group) {
    await fireEvent.click(screen.getByRole('button', { name: group }))
    await flush()
  }
}

const byId = (id: string) => document.getElementById(id) as HTMLInputElement | null

describe('Config — layout', () => {
  it('General holds the app and display preferences, out of Library', async () => {
    await open()
    expect(byId('check_for_updates')).toBeNull()
    expect(byId('recent_shelf_days')).toBeNull()
    await fireEvent.click(screen.getByRole('button', { name: /^General/ }))
    await flush()
    for (const id of ['check_for_updates', 'keep_removed_release_cards', 'recent_shelf_days']) {
      expect(byId(id), id).not.toBeNull()
    }
    expect(screen.getByRole('button', { name: /show again/i })).toBeTruthy()
  })

  it('Cleanup sits under Delete Downloads Automatically and is dimmed while direct mode keeps everything', async () => {
    await open()
    expect(byId('delete_watched_episodes')).not.toBeNull()
    expect(screen.getByTestId('cleanup-inactive')).toBeTruthy()
    await fireEvent.click(byId('auto_delete_downloads') as HTMLInputElement)
    await flush()
    expect(screen.queryByTestId('cleanup-inactive')).toBeNull()
  })

  it('Cleanup is not dimmed in hardlink mode', async () => {
    await open({ library_mode: 'hardlink' })
    expect(screen.queryByTestId('cleanup-inactive')).toBeNull()
  })

  it('Use Shoko waits for the sign-in, and turning it on without one is stopped before Save', async () => {
    await open({}, /^Shoko/)
    const sw = byId('shoko_enabled') as HTMLInputElement
    expect(sw.disabled).toBe(true)
    expect(screen.getByTestId('shoko-sign-in-first')).toBeTruthy()
  })

  it('Use Shoko is free once signed in', async () => {
    await open({ shoko_url: 'http://shoko:8111', shoko_api_key: '__saved__' }, /^Shoko/)
    expect((byId('shoko_enabled') as HTMLInputElement).disabled).toBe(false)
    expect(screen.queryByTestId('shoko-sign-in-first')).toBeNull()
  })

  it('a config turned on without a sign-in is caught before Save', async () => {
    await open({ shoko_enabled: true })
    await fireEvent.submit(document.querySelector('form') as HTMLFormElement)
    await flush()
    expect(client.updateConfig).not.toHaveBeenCalled()
  })

  it("qBittorrent's manage switch says what it applies and links to Downloads", async () => {
    await open({ torrent_client: 'qbittorrent', qbittorrent_url: 'http://q:8080', qbittorrent_manage_settings: true, max_concurrent_downloads: 4 }, /^Torrent Client/)
    expect(screen.getByTestId('qbittorrent-manage-summary').textContent).toContain('4 downloads at a time')
    await fireEvent.click(screen.getByRole('button', { name: 'Change in Downloads' }))
    await flush()
    expect(byId('max_concurrent_downloads')).not.toBeNull()
  })
})
