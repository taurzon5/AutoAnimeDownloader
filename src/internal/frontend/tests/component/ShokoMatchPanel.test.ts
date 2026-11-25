import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'
import { tick } from 'svelte'
import ShokoMatchPanel from '../../src/components/ShokoMatchPanel.svelte'
import * as client from '../../src/lib/api/client.js'

vi.mock('../../src/lib/api/client.js', () => ({ updateAnimeShoko: vi.fn(), updateAnimeShokoPacks: vi.fn() }))

const flush = async () => {
  for (let i = 0; i < 4; i++) {
    await tick()
    await Promise.resolve()
  }
}

describe('ShokoMatchPanel', () => {
  beforeEach(() => {
    vi.mocked(client.updateAnimeShoko).mockReset().mockResolvedValue(undefined)
    vi.mocked(client.updateAnimeShokoPacks).mockReset().mockResolvedValue(undefined)
  })

  it('asks to confirm a partial match and saves the series and offset', async () => {
    render(ShokoMatchPanel, { props: { animeId: 42, shoko: { status: 'partial', series_id: 3, series_name: 'Two cours' } } })
    expect(screen.getByTestId('shoko-panel-status').textContent).toContain('check it by hand')
    await fireEvent.input(document.getElementById('shoko_offset') as HTMLInputElement, { target: { value: '12' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Confirm match' }))
    await flush()
    expect(client.updateAnimeShoko).toHaveBeenCalledWith(42, { series_id: 3, offset: 12 })
  })

  it('offers "not in Shoko" for an automatic match, and no way back to automatic', async () => {
    render(ShokoMatchPanel, { props: { animeId: 42, shoko: { status: 'matched', series_id: 1, series_name: 'Frieren', offset: 12 } } })
    expect(screen.getByTestId('shoko-panel-status').textContent).toContain("series' episode 13")
    expect((document.getElementById('shoko_offset') as HTMLInputElement).value).toBe('12')
    expect(screen.queryByRole('button', { name: 'Back to automatic' })).toBeNull()
    await fireEvent.click(screen.getByRole('button', { name: 'Not in Shoko' }))
    await flush()
    expect(client.updateAnimeShoko).toHaveBeenCalledWith(42, { none: true })
  })

  it('goes back to automatic from a match set by hand', async () => {
    render(ShokoMatchPanel, { props: { animeId: 42, shoko: { status: 'none', by_hand: true } } })
    expect(screen.queryByRole('button', { name: 'Not in Shoko' })).toBeNull()
    await fireEvent.click(screen.getByRole('button', { name: 'Back to automatic' }))
    await flush()
    expect(client.updateAnimeShoko).toHaveBeenCalledWith(42, { auto: true })
  })

  it('lets an anime with no match be matched by hand', async () => {
    render(ShokoMatchPanel, { props: { animeId: 42, shoko: { status: 'none' } } })
    await fireEvent.input(document.getElementById('shoko_series_id') as HTMLInputElement, { target: { value: '7' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Confirm match' }))
    await flush()
    expect(client.updateAnimeShoko).toHaveBeenCalledWith(42, { series_id: 7, offset: 0 })
  })

  it('shows nothing without Shoko', () => {
    render(ShokoMatchPanel, { props: { animeId: 42, shoko: undefined } })
    expect(screen.queryByTestId('shoko-panel')).toBeNull()
  })

  it("chooses per anime what a pack does with the episodes already in Shoko", async () => {
    render(ShokoMatchPanel, { props: { animeId: 42, shoko: { status: 'matched', series_id: 1, keep_pack_episodes: true, keep_pack_episodes_default: true } } })
    const group = screen.getByTestId('shoko-packs')
    const def = screen.getByRole('radio', { name: 'Default (Download them)' })
    expect(def.getAttribute('aria-checked')).toBe('true')
    await fireEvent.click(screen.getByRole('radio', { name: 'Remove them' }))
    await flush()
    expect(client.updateAnimeShokoPacks).toHaveBeenCalledWith(42, 'remove')
    expect(screen.getByRole('radio', { name: 'Remove them' }).getAttribute('aria-checked')).toBe('true')
    expect(group.textContent).toContain('Download them')
  })

  it('shows the global default as Remove when the switch is off, and the anime\'s own choice', async () => {
    render(ShokoMatchPanel, { props: { animeId: 42, shoko: { status: 'matched', series_id: 1, pack_episodes: 'download', keep_pack_episodes: true, keep_pack_episodes_default: false } } })
    expect(screen.getByRole('radio', { name: 'Default (Remove them)' }).getAttribute('aria-checked')).toBe('false')
    expect(screen.getByRole('radio', { name: 'Download them' }).getAttribute('aria-checked')).toBe('true')
    await fireEvent.click(screen.getByRole('radio', { name: 'Default (Remove them)' }))
    await flush()
    expect(client.updateAnimeShokoPacks).toHaveBeenCalledWith(42, 'default')
  })
})
