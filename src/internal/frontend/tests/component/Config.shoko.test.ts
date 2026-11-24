import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'
import { tick } from 'svelte'
import { get } from 'svelte/store'
import { toasts } from '../../src/lib/stores/toast.js'
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

const saved = {
  anilist_usernames: [],
  completed_anime_path: '/mnt/anime',
  check_interval: 10,
  max_episodes_per_anime: 12,
  max_batch_torrent_size_gb: 100,
  max_episode_torrent_size_gb: 5,
  min_seeders: 3,
  max_search_pages: 5,
  nyaa_request_delay_seconds: 0,
  episode_retry_limit: 5,
  max_concurrent_downloads: 3,
  watched_episodes_to_keep: 2,
  min_free_disk_gb: 10,
  incomplete_min_free_disk_gb: 10,
  library_mode: 'direct',
  torrent_client: 'qbittorrent',
  qbittorrent_url: 'http://127.0.0.1:8080',
  qbittorrent_username: 'admin',
  qbittorrent_password: '__saved__',
  qbittorrent_api_key: '',
  qbittorrent_category: 'aad',
  qbittorrent_path_map: [],
  qbittorrent_disable_seeding_upload_kib: 16,
  notifications: { webhooks: [], batch_window_seconds: 60 },
}

async function openShokoGroup() {
  render(Config)
  await flush()
  await fireEvent.click(screen.getByRole('button', { name: /^Shoko/ }))
  await flush()
}

describe('Config — Shoko', () => {
  beforeEach(() => {
    vi.mocked(client.getConfig).mockResolvedValue({ ...structuredClone(saved), torrent_client: 'builtin', shoko_enabled: false, shoko_url: '', shoko_api_key: '', shoko_refresh_hours: 24 } as never)
    vi.mocked(client.updateConfig).mockReset().mockResolvedValue({ message: 'ok' })
    vi.mocked(client.shokoLogin).mockReset().mockResolvedValue(undefined)
    vi.mocked(client.getShokoStatus).mockReset().mockResolvedValue({ enabled: false, signed_in: false, series: 0, owned_episodes: 0 })
  })

  it('signs in with the password once, and a later Save keeps the saved key', async () => {
    await openShokoGroup()
    expect(screen.getByTestId('shoko-status').textContent).toContain('Not signed in')
    await fireEvent.input(document.getElementById('shoko_url') as HTMLInputElement, { target: { value: 'http://shoko:8111/' } })
    await fireEvent.input(document.getElementById('shoko_username') as HTMLInputElement, { target: { value: 'admin' } })
    await fireEvent.input(document.getElementById('shoko_password') as HTMLInputElement, { target: { value: 'pw' } })
    vi.mocked(client.getShokoStatus).mockResolvedValue({ enabled: false, signed_in: true, url: 'http://shoko:8111', series: 0, owned_episodes: 0 })
    await fireEvent.click(screen.getByRole('button', { name: 'Sign in to Shoko' }))
    await flush()
    expect(client.shokoLogin).toHaveBeenCalledWith('http://shoko:8111/', 'admin', 'pw')
    expect((document.getElementById('shoko_password') as HTMLInputElement).value).toBe('')
    expect(screen.getByTestId('shoko-status').textContent).toContain('Signed in to http://shoko:8111')

    await fireEvent.submit(document.querySelector('form') as HTMLFormElement)
    await flush()
    expect(client.updateConfig).toHaveBeenCalledWith(expect.objectContaining({ shoko_api_key: '__saved__', shoko_url: 'http://shoko:8111' }))
    expect(JSON.stringify(vi.mocked(client.updateConfig).mock.calls[0][0])).not.toContain('"pw"')
  })

  it('refuses to save Shoko on with a refresh under an hour', async () => {
    vi.mocked(client.getConfig).mockResolvedValue({ ...structuredClone(saved), shoko_enabled: true, shoko_url: 'http://shoko:8111', shoko_api_key: '__saved__', shoko_refresh_hours: 0 } as never)
    await openShokoGroup()
    await fireEvent.submit(document.querySelector('form') as HTMLFormElement)
    await flush()
    expect(client.updateConfig).not.toHaveBeenCalled()
  })
})
