// Settings fixes from the 2026-11-25 UI review (findings 9, 11, 13, 14, 15, 19).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
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
  max_episodes_per_anime: 12, max_batch_torrent_size_gb: 100, max_episode_torrent_size_gb: 5, min_seeders: 3,
  max_search_pages: 5, nyaa_request_delay_seconds: 0, episode_retry_limit: 5, max_concurrent_downloads: 3,
  watched_episodes_to_keep: 2, min_free_disk_gb: 10, incomplete_min_free_disk_gb: 10, library_mode: 'direct',
  incomplete_files: 'part', incomplete_path: '', download_roots: [], rename_files_for_jellyfin: false,
  episode_numbering: 'season', auto_delete_downloads: false, check_for_updates: false, recent_shelf_days: 3,
  keep_removed_release_cards: true, torrent_client: 'builtin', shoko_enabled: false, shoko_url: '',
  shoko_api_key: '', shoko_refresh_hours: 24, shoko_watch: true, shoko_full_resync_days: 30, shoko_full_packs: true,
  delete_watched_episodes: true, notifications: { webhooks: [], batch_window_seconds: 60 },
}

beforeEach(() => {
  vi.mocked(client.getConfig).mockResolvedValue(structuredClone(saved) as never)
  vi.mocked(client.updateConfig).mockReset().mockResolvedValue({ message: 'ok' } as never)
  vi.mocked(client.triggerCheck).mockReset().mockResolvedValue(undefined as never)
  vi.mocked(client.getShokoStatus).mockReset().mockResolvedValue({ enabled: false, signed_in: false, series: 0, owned_episodes: 0 } as never)
})
afterEach(() => { window.location.hash = '' })

const submit = async () => {
  await fireEvent.submit(document.querySelector('form') as HTMLFormElement)
  await flush()
}

describe('Config — review fixes', () => {
  it('9: qBittorrent without direct mode is stopped before Save, in the switch\'s words', async () => {
    vi.mocked(client.getConfig).mockResolvedValue({ ...structuredClone(saved), library_mode: 'hardlink', torrent_client: 'qbittorrent', qbittorrent_url: 'http://q:8080' } as never)
    render(Config)
    await flush()
    await submit()
    expect(client.updateConfig).not.toHaveBeenCalled()
  })

  it('11: split-season numbering shows only with renaming on', async () => {
    render(Config)
    await flush()
    expect(document.getElementById('episode_numbering')).toBeNull()
    await fireEvent.click(document.getElementById('rename_files_for_jellyfin') as HTMLInputElement)
    await flush()
    expect(document.getElementById('episode_numbering')).not.toBeNull()
  })

  it('13 and 14: a signed-in Shoko that is off can\'t run a full resync, and can sign out', async () => {
    vi.mocked(client.getShokoStatus).mockResolvedValue({ enabled: false, signed_in: true, url: 'http://shoko:8111', series: 1, owned_episodes: 2 } as never)
    vi.mocked(client.getConfig).mockResolvedValue({ ...structuredClone(saved), shoko_url: 'http://shoko:8111', shoko_api_key: '__saved__' } as never)
    render(Config)
    await flush()
    await fireEvent.click(screen.getByRole('button', { name: /^Shoko/ }))
    await flush()
    expect(screen.getByTestId('shoko-full-sync-off')).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: 'Sign out' }))
    await submit()
    expect(client.updateConfig).toHaveBeenCalledWith(expect.objectContaining({ shoko_url: '', shoko_enabled: false }))
  })

  it('15: Run check now waits for unsaved edits to be saved', async () => {
    render(Config)
    await flush()
    const run = screen.getByRole('button', { name: 'Check now' }) as HTMLButtonElement
    expect(run.disabled).toBe(false)
    await fireEvent.input(document.getElementById('completed_anime_path') as HTMLInputElement, { target: { value: '/mnt/other' } })
    await flush()
    expect(run.disabled).toBe(true)
    expect(screen.getByTestId('run-check-save-first')).toBeTruthy()
  })

  it('19: the missing-configuration banner goes away after a successful save', async () => {
    window.location.hash = '#/config?missingConfig=true'
    render(Config)
    await flush()
    expect(screen.queryByRole('alert')).not.toBeNull()
    await submit()
    expect(client.updateConfig).toHaveBeenCalled()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('39: a switch names its hint for screen readers', async () => {
    render(Config)
    await flush()
    const toggle = document.getElementById('rename_files_for_jellyfin') as HTMLInputElement
    expect(toggle.getAttribute('aria-describedby')).toBe('rename_files_for_jellyfin-hint')
    expect(document.getElementById('rename_files_for_jellyfin-hint')?.textContent).toBeTruthy()
  })
})
