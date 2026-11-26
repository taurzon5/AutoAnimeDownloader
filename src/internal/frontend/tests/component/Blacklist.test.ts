import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import Blacklist from '../../src/routes/Blacklist.svelte'
import * as client from '../../src/lib/api/client.js'

vi.mock('../../src/lib/api/client.js', () => ({
  getBlacklist: vi.fn(),
  unblacklistAnime: vi.fn(),
}))

beforeEach(() => {
  vi.clearAllMocks()
})

describe('Blacklist page (decisions/155)', () => {
  it('lists the blacklisted anime and takes one off', async () => {
    vi.mocked(client.getBlacklist).mockResolvedValue([
      { anime_id: 21, name: 'Witch', format: 'TV', blacklisted_at: '2026-11-25T22:13:00Z', paused: 2, episodes: 8 },
      { anime_id: 30, name: '', blacklisted_at: '2026-11-24T10:00:00Z', paused: 0, episodes: 0 },
    ])
    vi.mocked(client.unblacklistAnime).mockResolvedValue(undefined)
    render(Blacklist)

    await screen.findByText('Witch')
    expect(screen.getByText(/anime 30/i)).toBeTruthy()
    expect(document.body.textContent).toMatch(/8 (episodes kept|episódios mantidos)/)
    expect(document.body.textContent).toMatch(/2 (downloads paused|downloads pausados)/)

    const [first] = screen.getAllByRole('button', { name: /remove from blacklist|tirar da lista negra/i })
    await fireEvent.click(first)
    expect(client.unblacklistAnime).toHaveBeenCalledWith(21)
    await waitFor(() => expect(screen.queryByText('Witch')).toBeNull())
  })

  it('says so when nothing is blacklisted', async () => {
    vi.mocked(client.getBlacklist).mockResolvedValue([])
    render(Blacklist)
    await screen.findByText(/nothing is blacklisted|nada na lista negra/i)
  })
})
