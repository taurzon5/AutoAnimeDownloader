import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/svelte'
import { tick } from 'svelte'
import Status from '../../src/routes/Status.svelte'
import * as client from '../../src/lib/api/client.js'
import type { AnimeInfo, StatusResponse } from '../../src/lib/api/client.js'

vi.mock('../../src/lib/api/client.js', () => ({
  getStatus: vi.fn(),
  getAnimes: vi.fn(),
  getConfig: vi.fn(),
  triggerCheck: vi.fn(),
  startDaemon: vi.fn(),
  stopDaemon: vi.fn(),
  getTorrents: vi.fn(),
  getLastCheck: vi.fn(),
}))

vi.mock('../../src/lib/websocket/client.js', () => ({
  WebSocketClient: class {
    connect() {}
    disconnect() {}
  },
}))

const status: StatusResponse = { status: 'running', last_check: '2026-11-24T12:00:00Z', has_error: false, version: '1.0.0', disk_total: 100, disk_free: 90, disk_low: false }

const anime = (id: number, name: string, recent?: AnimeInfo['recent']): AnimeInfo =>
  ({ anime_id: id, name, episodes_downloaded: 1, episodes_released: 1, episodes_watched: 0, episodes_pending: 0, total_episodes: 12, latest_episode_number: 1, last_download_date: '', cover_image: `https://img/${id}.jpg`, recent }) as AnimeInfo

const flush = async () => {
  for (let i = 0; i < 4; i++) {
    await tick()
    await Promise.resolve()
  }
}

describe('Status — Recents shelf', () => {
  beforeEach(() => {
    vi.mocked(client.getStatus).mockResolvedValue(status)
    vi.mocked(client.getConfig).mockResolvedValue({ check_interval: 60 } as never)
    vi.mocked(client.getTorrents).mockResolvedValue([])
    vi.mocked(client.getLastCheck).mockResolvedValue({ finished_at: '0001-01-01T00:00:00Z', pass_error: '', problems: [], limits: [] })
  })

  it('shows one card per anime with recent, newest first, linked to the anime', async () => {
    vi.mocked(client.getAnimes).mockResolvedValue([
      anime(1, 'Older', { ranges: [{ from: 5, to: 5 }], is_batch: false, downloaded_at: '2026-11-24T08:00:00Z' }),
      anime(2, 'Newer', { ranges: [{ from: 1, to: 12 }], is_batch: true, downloaded_at: '2026-11-24T11:00:00Z' }),
      anime(3, 'Nothing new'),
    ])
    render(Status)
    await flush()

    const shelf = await screen.findByTestId('recent-shelf')
    const cards = shelf.querySelectorAll('[data-testid="recent-card"]')
    expect(cards).toHaveLength(2)
    expect(cards[0].getAttribute('href')).toBe('#/status/2')
    expect(cards[0].textContent).toContain('1–12 · pack')
    expect(cards[1].textContent).toContain('Episode 5')
    expect(shelf.textContent).not.toContain('Nothing new')
  })

  it('is absent when nothing is recent', async () => {
    vi.mocked(client.getAnimes).mockResolvedValue([anime(3, 'Nothing new')])
    render(Status)
    await flush()
    expect(screen.queryByTestId('recent-shelf')).toBeNull()
  })

  it('makes a last-check row a link to its anime, and leaves one without an id as text', async () => {
    vi.mocked(client.getAnimes).mockResolvedValue([anime(269, 'Bleach')])
    vi.mocked(client.getLastCheck).mockResolvedValue({
      finished_at: '2026-11-24T12:00:00Z',
      pass_error: '',
      problems: [
        { anime_id: 269, anime_name: 'Bleach', episodes: [12], code: 'all_above_size_limit', candidates: 8, limit_gb: 3 },
        { anime_id: 0, anime_name: 'Unknown', episodes: [1], code: 'all_above_size_limit', candidates: 1, limit_gb: 3 },
      ],
      limits: [{ anime_id: 21, anime_name: 'One Piece', code: 'max_episodes_per_anime', downloaded: 12, pending: 35, batch_skipped: 'no_result' }],
    } as never)
    render(Status)
    await flush()

    const rows = (await screen.findByTestId('last-check-report')).querySelectorAll('[data-testid="last-check-row"]')
    const byName = (n: string) => [...rows].find((r) => r.textContent?.includes(n))!
    expect(byName('Bleach').tagName).toBe('A')
    expect(byName('Bleach').getAttribute('href')).toBe('#/status/269')
    expect(byName('Bleach').querySelector('img')?.getAttribute('src')).toBe('https://img/269.jpg')
    expect(byName('One Piece').getAttribute('href')).toBe('#/status/21')
    expect(byName('Unknown').tagName).not.toBe('A')
  })
})
