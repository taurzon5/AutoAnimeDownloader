import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte'
import AnimeQueuePicker from '../../src/components/AnimeQueuePicker.svelte'
import * as client from '../../src/lib/api/client.js'

vi.mock('../../src/lib/api/client.js', () => ({
  getQueues: vi.fn(),
  moveAnimeToQueue: vi.fn(),
}))

const getQueues = vi.mocked(client.getQueues)
const moveAnimeToQueue = vi.mocked(client.moveAnimeToQueue)

const queues: client.Queue[] = [
  { id: 'default', name: 'Default', default: true, animes: [], priorities: {} as client.Priorities },
  { id: 'later', name: 'Later', default: false, animes: [21], priorities: {} as client.Priorities },
]

beforeEach(() => vi.clearAllMocks())

describe('AnimeQueuePicker (decisions/164)', () => {
  it("shows the anime's queue and moves it", async () => {
    getQueues.mockResolvedValue(queues)
    moveAnimeToQueue.mockResolvedValue(undefined)
    render(AnimeQueuePicker, { animeId: 21 })
    const select = (await screen.findByLabelText('Download queue')) as HTMLSelectElement
    expect(select.value).toBe('later')

    await fireEvent.change(select, { target: { value: 'default' } })
    await waitFor(() => expect(moveAnimeToQueue).toHaveBeenCalledWith(21, 'default'))
  })

  it('is hidden with a single queue', async () => {
    getQueues.mockResolvedValue(queues.slice(0, 1))
    render(AnimeQueuePicker, { animeId: 21 })
    await waitFor(() => expect(getQueues).toHaveBeenCalled())
    expect(screen.queryByTestId('anime-queue-picker')).toBeNull()
  })
})
