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
  notifications: { webhooks: [], batch_window_seconds: 60 },
}

async function openClientGroup() {
  render(Config)
  await flush()
  await fireEvent.click(screen.getByRole('button', { name: /Torrent client/ }))
  await flush()
}

describe('Config — torrent client', () => {
  beforeEach(() => {
    vi.mocked(client.getConfig).mockResolvedValue(structuredClone(saved) as never)
    vi.mocked(client.updateConfig).mockReset()
    vi.mocked(client.testTorrentClient).mockReset()
  })

  it('shows the saved qBittorrent settings, the password only as the placeholder', async () => {
    await openClientGroup()
    expect((document.getElementById('qbittorrent_url') as HTMLInputElement).value).toBe('http://127.0.0.1:8080')
    const password = document.getElementById('qbittorrent_password') as HTMLInputElement
    expect(password.type).toBe('password')
    expect(password.value).toBe('__saved__')
  })

  it('tests the connection with the settings being edited and shows what qBittorrent answered', async () => {
    vi.mocked(client.testTorrentClient).mockResolvedValueOnce({
      app_version: 'v5.1.0', webapi_version: '2.11.3', library_checked: true, library_visible: true, library_path: '/downloads',
    })
    await openClientGroup()
    await fireEvent.click(screen.getByRole('button', { name: 'Test connection' }))
    await flush()

    expect(client.testTorrentClient).toHaveBeenCalledWith(expect.objectContaining({
      qbittorrent_url: 'http://127.0.0.1:8080', qbittorrent_password: '__saved__',
    }))
    const result = screen.getByTestId('qbittorrent-test-result').textContent ?? ''
    expect(result).toContain('v5.1.0')
    expect(result).toContain('/downloads')
  })

  it('shows why the test failed', async () => {
    vi.mocked(client.testTorrentClient).mockRejectedValueOnce(new Error('qBittorrent refused the credentials'))
    await openClientGroup()
    await fireEvent.click(screen.getByRole('button', { name: 'Test connection' }))
    await flush()
    expect(screen.getByTestId('qbittorrent-test-error').textContent).toContain('refused the credentials')
  })

  it('saves the placeholder back untouched, so the saved password stays', async () => {
    vi.mocked(client.updateConfig).mockResolvedValueOnce({ message: 'ok', restart_required: true })
    await openClientGroup()
    await fireEvent.submit(document.querySelector('form') as HTMLFormElement)
    await flush()
    expect(client.updateConfig).toHaveBeenCalledWith(expect.objectContaining({
      torrent_client: 'qbittorrent', qbittorrent_password: '__saved__',
    }))
  })

  it('hides the qBittorrent fields with the built-in client', async () => {
    await openClientGroup()
    await fireEvent.click(document.getElementById('use_qbittorrent') as HTMLInputElement)
    await flush()
    expect(document.getElementById('qbittorrent_url')).toBeNull()
  })
})
