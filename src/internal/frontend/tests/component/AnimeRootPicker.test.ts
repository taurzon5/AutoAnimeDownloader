import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'
import { tick } from 'svelte'
import AnimeRootPicker from '../../src/components/AnimeRootPicker.svelte'
import * as client from '../../src/lib/api/client.js'

vi.mock('../../src/lib/api/client.js', () => ({ getConfig: vi.fn(), getAnimeSettings: vi.fn(), updateAnimeRoot: vi.fn() }))

const flush = async () => {
  for (let i = 0; i < 5; i++) {
    await tick()
    await Promise.resolve()
  }
}

const roots = [{ name: 'ssd', path: '/mnt/ssd' }, { name: 'hdd', path: '/mnt/hdd' }]

describe('AnimeRootPicker (decisions/154)', () => {
  beforeEach(() => {
    vi.mocked(client.updateAnimeRoot).mockReset().mockResolvedValue(undefined)
    vi.mocked(client.getAnimeSettings).mockResolvedValue({ root: 'hdd' })
  })

  it("shows the anime's folder and saves another one, or the fill order", async () => {
    vi.mocked(client.getConfig).mockResolvedValue({ library_mode: 'direct', download_roots: roots } as never)
    render(AnimeRootPicker, { props: { animeId: 42 } })
    await flush()
    const select = document.getElementById('anime_root') as HTMLSelectElement
    expect(select.value).toBe('hdd')
    await fireEvent.change(select, { target: { value: 'ssd' } })
    await flush()
    expect(client.updateAnimeRoot).toHaveBeenCalledWith(42, 'ssd')
    await fireEvent.change(select, { target: { value: '' } })
    await flush()
    expect(client.updateAnimeRoot).toHaveBeenLastCalledWith(42, '')
  })

  it('stays hidden with one folder, or in hardlink mode', async () => {
    for (const config of [{ library_mode: 'direct', download_roots: [roots[0]] }, { library_mode: 'hardlink', download_roots: roots }]) {
      vi.mocked(client.getConfig).mockResolvedValue(config as never)
      const { unmount } = render(AnimeRootPicker, { props: { animeId: 42 } })
      await flush()
      expect(screen.queryByTestId('anime-root-picker')).toBeNull()
      unmount()
    }
  })
})
