import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within } from '@testing-library/svelte'
import { tick } from 'svelte'
import { get } from 'svelte/store'
import { hasUnsavedChanges } from '../../src/lib/stores/unsavedChanges.js'
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
    vi.mocked(client.getConfig).mockResolvedValue({ ...structuredClone(saved), torrent_client: 'builtin', shoko_enabled: false, shoko_url: '', shoko_api_key: '', shoko_refresh_hours: 24,
      // What GET /config always carries; missing, the page fills them in and reads as unsaved before any edit.
      auto_delete_downloads: false, check_for_updates: false, incomplete_path: '', rename_files_for_jellyfin: false, shoko_watch: true, shoko_full_resync_days: 30, shoko_full_packs: true } as never)
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
    // Saved on the server by the sign-in: nothing left unsaved (decisions/129).
    expect(get(hasUnsavedChanges)).toBe(false)

    await fireEvent.submit(document.querySelector('form') as HTMLFormElement)
    await flush()
    expect(client.updateConfig).toHaveBeenCalledWith(expect.objectContaining({ shoko_api_key: '__saved__', shoko_url: 'http://shoko:8111' }))
    expect(JSON.stringify(vi.mocked(client.updateConfig).mock.calls[0][0])).not.toContain('"pw"')
  })

  it('says whether it follows Shoko\'s changes, and saves the switch', async () => {
    vi.mocked(client.getConfig).mockResolvedValue({ ...structuredClone(saved), shoko_enabled: true, shoko_url: 'http://shoko:8111', shoko_api_key: '__saved__', shoko_refresh_hours: 24, shoko_watch: true } as never)
    vi.mocked(client.getShokoStatus).mockResolvedValue({ enabled: true, signed_in: true, url: 'http://shoko:8111', series: 1, owned_episodes: 2, watching: false, watch_error: 'connection refused' })
    await openShokoGroup()
    expect(screen.getByTestId('shoko-watch-status').textContent).toContain('connection refused')
    await fireEvent.click(document.getElementById('shoko_watch') as HTMLInputElement)
    await flush()
    expect(screen.queryByTestId('shoko-watch-status')).toBeNull()
    await fireEvent.submit(document.querySelector('form') as HTMLFormElement)
    await flush()
    expect(client.updateConfig).toHaveBeenCalledWith(expect.objectContaining({ shoko_watch: false }))
  })

  it('saves Download Full Megapacks', async () => {
    // The daemon's default is on (DefaultConfig); GET /config always carries it.
    vi.mocked(client.getConfig).mockResolvedValue({ ...structuredClone(saved), shoko_enabled: true, shoko_url: 'http://shoko:8111', shoko_api_key: '__saved__', shoko_refresh_hours: 24, shoko_watch: true, shoko_full_packs: true } as never)
    vi.mocked(client.getShokoStatus).mockResolvedValue({ enabled: true, signed_in: true, url: 'http://shoko:8111', series: 1, owned_episodes: 2 })
    await openShokoGroup()
    const toggle = document.getElementById('shoko_full_packs') as HTMLInputElement
    expect(toggle.checked).toBe(true)
    await fireEvent.click(toggle)
    await flush()
    await fireEvent.submit(document.querySelector('form') as HTMLFormElement)
    await flush()
    expect(client.updateConfig).toHaveBeenCalledWith(expect.objectContaining({ shoko_full_packs: false }))
  })

  it("lists Shoko's import folders and offers them under Download Folders; adding one waits for Save", async () => {
    vi.mocked(client.getConfig).mockResolvedValue({ ...structuredClone(saved), shoko_enabled: true, shoko_url: 'http://shoko:8111', shoko_api_key: '__saved__', shoko_refresh_hours: 24, shoko_watch: true, incomplete_files: 'part', completed_anime_path: '/anime', download_roots: [] } as never)
    vi.mocked(client.getShokoStatus).mockResolvedValue({
      enabled: true, signed_in: true, url: 'http://shoko:8111', series: 1, owned_episodes: 2,
      import_folders: [
        { id: 2, name: 'library', path: '/anime/', app_path: '/anime/', drop_type: 'Destination', drop_source: false, watched: true },
        { id: 1, name: 'drop', path: '/import/', app_path: '/mnt/shoko/import', drop_type: 'Source', drop_source: true, watched: false },
      ],
      download_roots: [{ name: 'library', path: '/anime', in_shoko: true, folder_id: 2, folder_name: 'library' }],
    })
    await openShokoGroup()
    const folders = screen.getByTestId('shoko-folders').textContent ?? ''
    // Shoko's types read as words; a folder the path map moves shows where this app sees it.
    expect(folders).toContain('drop source')
    expect(folders).toContain('drop destination')
    expect(folders).not.toContain('Destination')
    expect(folders).toContain('/mnt/shoko/import')
    expect(screen.queryByTestId('shoko-drop-suggestion')).toBeNull()

    await fireEvent.click(screen.getByRole('button', { name: /^Library/ }))
    await flush()
    // The library is the one download folder, and Shoko's: it says so, and only the other folder
    // is offered.
    expect(screen.getByTestId('library-shoko').textContent).toContain('In Shoko: library')
    const addable = screen.getByTestId('shoko-addable')
    expect(addable.textContent).toContain('/mnt/shoko/import')
    expect(addable.textContent).not.toContain('/anime/')
    await fireEvent.click(screen.getByRole('button', { name: /^drop · / }))
    await flush()
    expect(client.updateConfig).not.toHaveBeenCalled()
    expect(screen.queryByTestId('shoko-addable')).toBeNull()
    expect(screen.getByTestId('download_root_shoko_0').textContent).toContain('Save to see')
    // Saved, the server answers for the new folder, and the row says so.
    vi.mocked(client.getShokoStatus).mockResolvedValue({
      enabled: true, signed_in: true, url: 'http://shoko:8111', series: 1, owned_episodes: 2,
      download_roots: [
        { name: 'drop', path: '/mnt/shoko/import', in_shoko: true, folder_id: 1, folder_name: 'drop' },
        { name: 'library', path: '/anime', in_shoko: true, folder_id: 2, folder_name: 'library' },
      ],
    })
    await fireEvent.submit(document.querySelector('form') as HTMLFormElement)
    await flush()
    expect(client.updateConfig).toHaveBeenCalledWith(expect.objectContaining({
      download_roots: [{ name: 'drop', path: '/mnt/shoko/import' }, { name: 'library', path: '/anime' }],
    }))
    expect(screen.getByTestId('download_root_shoko_0').textContent).toContain('In Shoko: drop')
    expect(screen.queryByTestId('library-shoko')).toBeNull() // the list answers for it now
  })

  it("doesn't offer a Shoko folder that holds a download folder or lies inside one", async () => {
    vi.mocked(client.getConfig).mockResolvedValue({ ...structuredClone(saved), shoko_enabled: true, shoko_url: 'http://shoko:8111', shoko_api_key: '__saved__', shoko_refresh_hours: 24, shoko_watch: true, incomplete_files: 'part', completed_anime_path: '/anime',
      download_roots: [{ name: 'seasonal', path: '/mnt/drop/Seasonal' }, { name: 'hdd', path: '/mnt/hdd' }] } as never)
    vi.mocked(client.getShokoStatus).mockResolvedValue({
      enabled: true, signed_in: true, url: 'http://shoko:8111', series: 1, owned_episodes: 2,
      import_folders: [
        { id: 1, name: 'drop', path: '/import/', app_path: '/mnt/drop', drop_type: 'Source', drop_source: true, watched: false },
        { id: 2, name: 'old', path: '/mnt/hdd/old/', app_path: '/mnt/hdd/old/', drop_type: 'None', drop_source: false, watched: false },
        { id: 3, name: 'new', path: '/mnt/new/', app_path: '/mnt/new/', drop_type: 'None', drop_source: false, watched: false },
      ],
    })
    await openShokoGroup()
    await fireEvent.click(screen.getByRole('button', { name: /^Library/ }))
    await flush()
    const addable = screen.getByTestId('shoko-addable').textContent ?? ''
    expect(addable).toContain('/mnt/new/')
    expect(addable).not.toContain('/mnt/drop')
    expect(addable).not.toContain('/mnt/hdd/old')
  })

  it('says which download folders are in Shoko, with the last import request', async () => {
    vi.mocked(client.getConfig).mockResolvedValue({ ...structuredClone(saved), shoko_enabled: true, shoko_url: 'http://shoko:8111', shoko_api_key: '__saved__', shoko_refresh_hours: 24, shoko_watch: true, incomplete_files: 'part', completed_anime_path: '/anime',
      download_roots: [{ name: 'drop', path: '/import' }, { name: 'outside', path: '/mnt/other' }] } as never)
    vi.mocked(client.getShokoStatus).mockResolvedValue({
      enabled: true, signed_in: true, url: 'http://shoko:8111', series: 1, owned_episodes: 2,
      last_import_request: '2026-11-24T18:00:00Z', import_error: 'HTTP 403',
      import_folders: [{ id: 1, name: 'shokodrop', path: '/import/', app_path: '/import/', drop_type: 'Source', drop_source: true, watched: false }],
      download_roots: [
        { name: 'drop', path: '/import', in_shoko: true, folder_id: 1, folder_name: 'shokodrop' },
        { name: 'outside', path: '/mnt/other', in_shoko: false },
      ],
    })
    await openShokoGroup()
    const inShoko = screen.getByTestId('shoko-roots-in').textContent ?? ''
    expect(inShoko).toContain('drop')
    expect(inShoko).not.toContain('outside')
    expect(screen.getByTestId('shoko-last-import')).not.toBeNull()
    expect(document.body.textContent).toContain('HTTP 403')

    await fireEvent.click(screen.getByRole('button', { name: /^Library/ }))
    await flush()
    expect(screen.getByTestId('download_root_shoko_0').textContent).toContain('In Shoko: shokodrop')
    expect(screen.getByTestId('download_root_shoko_1').textContent).toContain('Not in Shoko')
    // Only Shoko answers for a folder, through the server: an edited row waits for Save.
    await fireEvent.input(document.getElementById('download_root_path_1') as HTMLInputElement, { target: { value: '/mnt/elsewhere' } })
    await flush()
    expect(screen.getByTestId('download_root_shoko_1').textContent).toContain('Save to see')
    expect(document.getElementById('download_root_shoko_0')).toBeNull() // a status, not a switch
  })

  it('saves the Shoko path map', async () => {
    vi.mocked(client.getConfig).mockResolvedValue({ ...structuredClone(saved), shoko_enabled: true, shoko_url: 'http://shoko:8111', shoko_api_key: '__saved__', shoko_refresh_hours: 24, shoko_watch: true, incomplete_files: 'part' } as never)
    vi.mocked(client.getShokoStatus).mockResolvedValue({ enabled: true, signed_in: true, url: 'http://shoko:8111', series: 1, owned_episodes: 2 })
    await openShokoGroup()
    const map = screen.getByTestId('shoko-path-map')
    await fireEvent.click(within(map).getByRole('button', { name: /add/i }))
    await flush()
    await fireEvent.input(document.getElementById('shoko_map_app_0') as HTMLInputElement, { target: { value: '/mnt/anime' } })
    await fireEvent.input(document.getElementById('shoko_map_shoko_0') as HTMLInputElement, { target: { value: '/import' } })
    await fireEvent.submit(document.querySelector('form') as HTMLFormElement)
    await flush()
    expect(client.updateConfig).toHaveBeenCalledWith(expect.objectContaining({ shoko_path_map: [{ app: '/mnt/anime', shoko: '/import' }] }))
  })

  it('refuses a download folder in Shoko that would show Shoko half-written files', async () => {
    for (const extra of [
      { incomplete_files: 'in_place', incomplete_path: '' },
      { incomplete_files: 'part', incomplete_path: '/mnt/nvme/incomplete', torrent_client: 'qbittorrent', qbittorrent_url: 'http://qbit:8080' },
    ]) {
      vi.mocked(client.updateConfig).mockClear()
      vi.mocked(client.getConfig).mockResolvedValue({ ...structuredClone(saved), shoko_enabled: true, shoko_url: 'http://shoko:8111', shoko_api_key: '__saved__', shoko_refresh_hours: 24, download_roots: [{ name: 'drop', path: '/import' }], ...extra } as never)
      vi.mocked(client.getShokoStatus).mockResolvedValue({ enabled: true, signed_in: true, url: 'http://shoko:8111', series: 1, owned_episodes: 2,
        download_roots: [{ name: 'drop', path: '/import', in_shoko: true, folder_id: 1, folder_name: 'drop' }] })
      await openShokoGroup()
      await fireEvent.submit(document.querySelector('form') as HTMLFormElement)
      await flush()
      expect(client.updateConfig).not.toHaveBeenCalled()
      cleanup()
    }
  })

  it('refuses to save Shoko on with a refresh under an hour', async () => {
    vi.mocked(client.getConfig).mockResolvedValue({ ...structuredClone(saved), shoko_enabled: true, shoko_url: 'http://shoko:8111', shoko_api_key: '__saved__', shoko_refresh_hours: 0 } as never)
    await openShokoGroup()
    await fireEvent.submit(document.querySelector('form') as HTMLFormElement)
    await flush()
    expect(client.updateConfig).not.toHaveBeenCalled()
  })
})
