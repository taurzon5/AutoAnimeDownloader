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
  // GET /config always carries these; missing, their inputs bind null and read as cleared.
  recent_shelf_days: 3,
  shoko_full_resync_days: 30,
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

  it('shows that qBittorrent sees each download folder the test checked', async () => {
    vi.mocked(client.testTorrentClient).mockResolvedValueOnce({
      app_version: 'v5.3.0', webapi_version: '2.16.2', library_checked: true, library_visible: true, library_path: '/downloads',
      folders: [{ name: 'hdd', path: '/mnt/hdd/anime', visible: true }],
    })
    await openClientGroup()
    await fireEvent.click(screen.getByRole('button', { name: 'Test connection' }))
    await flush()
    expect(screen.getByTestId('qbittorrent-test-result').textContent).toContain('download folder hdd at /mnt/hdd/anime')
  })

  it('sends the incomplete folder to the test and shows that qBittorrent sees it', async () => {
    vi.mocked(client.getConfig).mockResolvedValue({ ...structuredClone(saved), incomplete_path: '/mnt/nvme/incomplete' } as never)
    vi.mocked(client.testTorrentClient).mockResolvedValueOnce({
      app_version: 'v5.3.0', webapi_version: '2.16.2', library_checked: true, library_visible: true, library_path: '/downloads',
      incomplete_checked: true, incomplete_visible: true, incomplete_path: '/incomplete',
    })
    await openClientGroup()
    await fireEvent.click(screen.getByRole('button', { name: 'Test connection' }))
    await flush()
    expect(client.testTorrentClient).toHaveBeenCalledWith(expect.objectContaining({ incomplete_path: '/mnt/nvme/incomplete' }))
    expect(screen.getByTestId('qbittorrent-test-result').textContent).toContain('incomplete folder at /incomplete')
  })

  it('says which sign-in is in use and suggests pread disk I/O when the app does not manage qBittorrent', async () => {
    vi.mocked(client.testTorrentClient).mockResolvedValueOnce({
      app_version: 'v5.3.0', webapi_version: '2.16.2', library_checked: false, library_visible: false, sign_in: 'api_key', disk_io_type: 0,
    })
    await openClientGroup()
    await fireEvent.click(screen.getByRole('button', { name: 'Test connection' }))
    await flush()
    expect(screen.getByTestId('qbittorrent-test-result').textContent).toContain('Signed in with the API key')
    expect(screen.getByTestId('qbittorrent-test-diskio').textContent).toContain('Simple pread/pwrite')

    await fireEvent.click(document.getElementById('qbittorrent_manage_settings') as HTMLInputElement)
    await flush()
    expect(screen.queryByTestId('qbittorrent-test-diskio')).toBeNull()
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

  it('saves the switch that lets the app manage qBittorrent\'s settings', async () => {
    vi.mocked(client.updateConfig).mockResolvedValueOnce({ message: 'ok' })
    await openClientGroup()
    const toggle = document.getElementById('qbittorrent_manage_settings') as HTMLInputElement
    expect(toggle.checked).toBe(false)
    await fireEvent.click(toggle)
    await fireEvent.submit(document.querySelector('form') as HTMLFormElement)
    await flush()
    expect(client.updateConfig).toHaveBeenCalledWith(expect.objectContaining({ qbittorrent_manage_settings: true }))
  })

  it.each([
    [{ queueing_enabled: true, max_active_downloads: 3 }, '3 at a time'],
    [{ queueing_enabled: true, max_active_downloads: -1 }, 'no limit'],
    [{ queueing_enabled: false, max_active_downloads: 3 }, 'is off'],
  ])('shows qBittorrent\'s own download queue from the test (%o)', async (queue, want) => {
    vi.mocked(client.testTorrentClient).mockResolvedValueOnce({
      app_version: 'v5.3.0', webapi_version: '2.16.2', library_checked: false, library_visible: false, ...queue,
    })
    await openClientGroup()
    await fireEvent.click(screen.getByRole('button', { name: 'Test connection' }))
    await flush()
    expect(screen.getByTestId('qbittorrent-test-result').textContent).toContain(want)
  })

  it('says in Downloads which queue applies and what disable seeding means with qBittorrent', async () => {
    await openClientGroup()
    await fireEvent.click(screen.getByRole('button', { name: 'Downloads' }))
    await flush()
    expect(screen.getByTestId('max-concurrent-qbittorrent').textContent).toContain("doesn't apply")
    expect(screen.getByTestId('disable-seeding-qbittorrent').textContent).toContain('almost no upload')
    expect(document.getElementById('qbittorrent_disable_seeding_upload_kib')).toBeNull()
    await fireEvent.click(document.getElementById('disable_seeding') as HTMLInputElement)
    await flush()
    expect((document.getElementById('qbittorrent_disable_seeding_upload_kib') as HTMLInputElement).value).toBe('16')

    await fireEvent.click(screen.getByRole('button', { name: /Torrent client/ }))
    await flush()
    await fireEvent.click(document.getElementById('qbittorrent_manage_settings') as HTMLInputElement)
    await fireEvent.click(screen.getByRole('button', { name: 'Downloads' }))
    await flush()
    expect(screen.getByTestId('max-concurrent-qbittorrent').textContent).toContain("sets qBittorrent's own download queue")

    await fireEvent.click(screen.getByRole('button', { name: /Torrent client/ }))
    await flush()
    await fireEvent.click(document.getElementById('use_qbittorrent') as HTMLInputElement)
    await fireEvent.click(screen.getByRole('button', { name: 'Downloads' }))
    await flush()
    expect(screen.queryByTestId('max-concurrent-qbittorrent')).toBeNull()
    expect(screen.queryByTestId('disable-seeding-qbittorrent')).toBeNull()
  })

  it('refuses to save an upload limit under 1 KiB/s with qBittorrent', async () => {
    vi.mocked(client.getConfig).mockResolvedValue({ ...structuredClone(saved), disable_seeding: true, qbittorrent_disable_seeding_upload_kib: 0 } as never)
    await openClientGroup()
    await fireEvent.submit(document.querySelector('form') as HTMLFormElement)
    await flush()
    expect(client.updateConfig).not.toHaveBeenCalled()
    expect(get(toasts).map((t) => t.message).join(' ')).toContain('at least 1 KiB/s')
    // The page opens the group with the field to fix.
    expect(document.getElementById('qbittorrent_disable_seeding_upload_kib')).not.toBeNull()
  })

  it('does not block a save on a 0 upload limit while disable seeding is off', async () => {
    vi.mocked(client.getConfig).mockResolvedValue({ ...structuredClone(saved), disable_seeding: false, qbittorrent_disable_seeding_upload_kib: 0 } as never)
    vi.mocked(client.updateConfig).mockResolvedValueOnce({ message: 'ok' })
    await openClientGroup()
    await fireEvent.submit(document.querySelector('form') as HTMLFormElement)
    await flush()
    expect(client.updateConfig).toHaveBeenCalled()
  })

  it('hides the qBittorrent fields with the built-in client', async () => {
    await openClientGroup()
    await fireEvent.click(document.getElementById('use_qbittorrent') as HTMLInputElement)
    await flush()
    expect(document.getElementById('qbittorrent_url')).toBeNull()
  })
})

describe('Config — number fields', () => {
  beforeEach(() => {
    vi.mocked(client.getConfig).mockResolvedValue(structuredClone(saved) as never)
    vi.mocked(client.updateConfig).mockReset().mockResolvedValue({ message: 'ok' } as never)
  })

  async function setAndSave(group: RegExp, id: string, value: string) {
    render(Config)
    await flush()
    await fireEvent.click(screen.getByRole('button', { name: group }))
    await flush()
    await fireEvent.input(document.getElementById(id) as HTMLInputElement, { target: { value } })
    await flush()
    await fireEvent.submit(document.querySelector('form') as HTMLFormElement)
    await flush()
  }

  it('a cleared number field is not saved as 0 (which would turn the disk floor off)', async () => {
    await setAndSave(/^Library/, 'min_free_disk_gb', '')
    expect(client.updateConfig).not.toHaveBeenCalled()
  })

  it('an episode retry limit of 0 is refused: it would try no magnet at all', async () => {
    await setAndSave(/^Torrent search/, 'episode_retry_limit', '0')
    expect(client.updateConfig).not.toHaveBeenCalled()
  })

  it('control: a valid edit saves', async () => {
    await setAndSave(/^Torrent search/, 'episode_retry_limit', '1')
    expect(client.updateConfig).toHaveBeenCalledWith(expect.objectContaining({ episode_retry_limit: 1 }))
  })
})
