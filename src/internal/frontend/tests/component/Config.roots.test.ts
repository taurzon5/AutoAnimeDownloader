import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'
import { tick } from 'svelte'
import Config from '../../src/routes/Config.svelte'
import { get } from 'svelte/store'
import { hasUnsavedChanges } from '../../src/lib/stores/unsavedChanges.js'
import * as client from '../../src/lib/api/client.js'

vi.mock('../../src/lib/api/client.js', () => ({
  getConfig: vi.fn(),
  updateConfig: vi.fn(),
  triggerCheck: vi.fn(),
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
  incomplete_files: 'part',
  incomplete_path: '',
  download_roots: [],
  notifications: { webhooks: [], batch_window_seconds: 60 },
}

describe('Config — download folders (decisions/154)', () => {
  beforeEach(() => {
    vi.mocked(client.getConfig).mockResolvedValue(structuredClone(saved) as never)
    vi.mocked(client.updateConfig).mockReset().mockResolvedValue({ message: 'ok' } as never)
  })

  it('lists the library with the first folder added, and saves the folders in order', async () => {
    render(Config)
    await flush()
    expect(screen.getByTestId('download-roots')).not.toBeNull()
    await fireEvent.click(screen.getByRole('button', { name: 'Add Folder' }))
    await flush()
    expect(screen.getAllByTestId('download-root')).toHaveLength(2)
    expect((document.getElementById('download_root_path_0') as HTMLInputElement).value).toBe('/mnt/anime')
    await fireEvent.input(document.getElementById('download_root_name_1') as HTMLInputElement, { target: { value: 'hdd' } })
    await fireEvent.input(document.getElementById('download_root_path_1') as HTMLInputElement, { target: { value: '/mnt/hdd/anime' } })
    await fireEvent.submit(document.querySelector('form') as HTMLFormElement)
    await flush()
    expect(client.updateConfig).toHaveBeenCalledWith(expect.objectContaining({
      download_roots: [{ name: 'library', path: '/mnt/anime' }, { name: 'hdd', path: '/mnt/hdd/anime' }],
    }))
  })

  it('refuses a folder with no name, or two with the same name', async () => {
    for (const roots of [[{ name: '', path: '/mnt/a' }], [{ name: 'x', path: '/mnt/a' }, { name: 'x', path: '/mnt/b' }]]) {
      vi.mocked(client.updateConfig).mockClear()
      vi.mocked(client.getConfig).mockResolvedValue({ ...structuredClone(saved), download_roots: roots } as never)
      const { unmount } = render(Config)
      await flush()
      await fireEvent.submit(document.querySelector('form') as HTMLFormElement)
      await flush()
      expect(client.updateConfig).not.toHaveBeenCalled()
      unmount()
    }
  })

  it("moves a folder up to the highest priority, and saves each folder's keep-free limit", async () => {
    vi.mocked(client.getConfig).mockResolvedValue({ ...structuredClone(saved), download_roots: [{ name: 'ssd', path: '/mnt/ssd' }, { name: 'hdd', path: '/mnt/hdd' }] } as never)
    render(Config)
    await flush()
    await fireEvent.click(screen.getAllByRole('button', { name: 'Move up' })[1])
    await flush()
    expect((document.getElementById('download_root_name_0') as HTMLInputElement).value).toBe('hdd')
    expect(screen.getAllByRole('button', { name: 'Move up' })[0].hasAttribute('disabled')).toBe(true)
    const keep = document.getElementById('download_root_min_free_0') as HTMLInputElement
    // Empty shows the global Min Free Disk Space it falls back to.
    expect(keep.placeholder).toBe('10')
    await fireEvent.input(keep, { target: { value: '200' } })
    await flush()
    await fireEvent.submit(document.querySelector('form') as HTMLFormElement)
    await flush()
    expect(client.updateConfig).toHaveBeenCalledWith(expect.objectContaining({
      download_roots: [{ name: 'hdd', path: '/mnt/hdd', min_free_gb: 200 }, { name: 'ssd', path: '/mnt/ssd' }],
    }))
  })
})

// What GET /config always carries; missing, the page fills them in and reads as unsaved before any edit.
const complete = {
  auto_delete_downloads: false, check_for_updates: false, rename_files_for_jellyfin: false, recent_shelf_days: 3,
  keep_removed_release_cards: true, torrent_client: 'builtin', shoko_enabled: false, shoko_url: '', shoko_api_key: '',
  delete_watched_episodes: true,
  shoko_refresh_hours: 24, shoko_watch: true, shoko_full_resync_days: 30, shoko_full_packs: true,
}

describe('Config — the library among the download folders', () => {
  beforeEach(() => {
    vi.mocked(client.updateConfig).mockReset().mockResolvedValue({ message: 'ok' } as never)
  })

  it('carries the library path on its row: no second field, and editing the row moves the library', async () => {
    vi.mocked(client.getConfig).mockResolvedValue({ ...structuredClone(saved), ...complete, download_roots: [
      { name: 'ssd', path: '/mnt/ssd/anime' }, { name: 'library', path: '/mnt/anime/' },
    ] } as never)
    render(Config)
    await flush()
    // The row's trailing slash is taken before the snapshot: nothing reads as edited (decisions/129).
    expect(get(hasUnsavedChanges)).toBe(false)
    expect(document.getElementById('completed_anime_path')).toBeNull()
    expect(screen.getByTestId('library-is-folder').textContent).toContain('library (/mnt/anime/)')
    expect(screen.getAllByTestId('download-root-library')).toHaveLength(1)

    await fireEvent.input(document.getElementById('download_root_path_1') as HTMLInputElement, { target: { value: '/mnt/big/anime' } })
    await flush()
    expect(document.getElementById('completed_anime_path')).toBeNull()
    await fireEvent.submit(document.querySelector('form') as HTMLFormElement)
    await flush()
    expect(client.updateConfig).toHaveBeenCalledWith(expect.objectContaining({ completed_anime_path: '/mnt/big/anime' }))
  })

  it('follows the library row when the folders are reordered', async () => {
    vi.mocked(client.getConfig).mockResolvedValue({ ...structuredClone(saved), download_roots: [
      { name: 'ssd', path: '/mnt/ssd/anime' }, { name: 'library', path: '/mnt/anime' },
    ] } as never)
    render(Config)
    await flush()
    await fireEvent.click(screen.getAllByRole('button', { name: /move up|subir/i })[1])
    await flush()
    await fireEvent.submit(document.querySelector('form') as HTMLFormElement)
    await flush()
    expect(client.updateConfig).toHaveBeenCalledWith(expect.objectContaining({
      completed_anime_path: '/mnt/anime',
      download_roots: [{ name: 'library', path: '/mnt/anime' }, { name: 'ssd', path: '/mnt/ssd/anime' }],
    }))
  })

  it('shows the field again, said truthfully, when the library is not one of the folders', async () => {
    vi.mocked(client.getConfig).mockResolvedValue({ ...structuredClone(saved), download_roots: [{ name: 'ssd', path: '/mnt/ssd/anime' }] } as never)
    render(Config)
    await flush()
    expect(document.getElementById('completed_anime_path')).not.toBeNull()
    expect(document.body.textContent).toMatch(/Not one of your download folders/)
  })
})
