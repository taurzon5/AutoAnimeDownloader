import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'
import { tick } from 'svelte'
import Config from '../../src/routes/Config.svelte'
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
