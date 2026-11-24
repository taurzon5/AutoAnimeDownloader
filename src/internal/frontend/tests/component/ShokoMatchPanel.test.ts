import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'
import { tick } from 'svelte'
import ShokoMatchPanel from '../../src/components/ShokoMatchPanel.svelte'
import * as client from '../../src/lib/api/client.js'

vi.mock('../../src/lib/api/client.js', () => ({ updateAnimeShoko: vi.fn() }))

const flush = async () => {
  for (let i = 0; i < 4; i++) {
    await tick()
    await Promise.resolve()
  }
}

describe('ShokoMatchPanel', () => {
  beforeEach(() => vi.mocked(client.updateAnimeShoko).mockReset().mockResolvedValue(undefined))

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
    expect(document.getElementById('shoko_series_id')).toBeNull()
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

  it('shows nothing without Shoko', () => {
    render(ShokoMatchPanel, { props: { animeId: 42, shoko: undefined } })
    expect(screen.queryByTestId('shoko-panel')).toBeNull()
  })
})
