import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/svelte'
import { tick } from 'svelte'
import AnimeDetail from '../../src/routes/AnimeDetail.svelte'
import * as client from '../../src/lib/api/client.js'
import type { AnimeDetailResponse, AnimeInfo } from '../../src/lib/api/client.js'

// AnimeDetail.svelte only reads these named exports; stub every one it imports so the
// module mock doesn't leave any binding undefined.
vi.mock('../../src/lib/api/client.js', () => ({
  getAnimeDetail: vi.fn(),
  getAnimes: vi.fn(),
  getTorrents: vi.fn(),
  downloadEpisode: vi.fn(),
  deleteEpisode: vi.fn(),
  batchDeleteEpisodes: vi.fn(),
  batchRedownloadEpisodes: vi.fn(),
  releaseEpisode: vi.fn(),
  redownloadEpisode: vi.fn(),
  replaceEpisodeWithMagnet: vi.fn(),
  replaceAnimeWithMagnet: vi.fn(),
  updateAnimeSettings: vi.fn(),
  removeStandaloneAnime: vi.fn(),
  deleteTorrent: vi.fn(),
  getLastCheck: vi.fn(),
  updateAnimeShoko: vi.fn(),
}))

function animeInfo(overrides: Partial<AnimeInfo> = {}): AnimeInfo {
  return {
    anime_id: 42,
    name: 'Test Anime',
    episodes_downloaded: 0,
    episodes_released: 0,
    episodes_watched: 0,
    episodes_pending: 0,
    total_episodes: 0,
    latest_episode_number: 0,
    last_download_date: '',
    ...overrides,
  }
}

describe('AnimeDetail — Shoko', () => {
  it('marks the episodes already in Shoko and shows the match', async () => {
    const detail: AnimeDetailResponse = {
      anime_id: 42, total_episodes: 3, progress: 0, status: 'CURRENT',
      shoko: { status: 'matched', series_id: 1, series_name: 'Sousou no Frieren' },
      episodes: [
        { episode_number: 1, airing_at: 0, time_until_airing: 0, is_aired: true, is_watched: false, is_downloaded: false, in_shoko: true, shoko_file: 'Frieren/01.mkv' },
        { episode_number: 2, airing_at: 0, time_until_airing: 0, is_aired: true, is_watched: false, is_downloaded: false, in_shoko: true, shoko_file: 'Frieren/02.mkv', shoko_file_unavailable: true },
        { episode_number: 3, airing_at: 0, time_until_airing: 0, is_aired: true, is_watched: false, is_downloaded: false },
      ],
    }
    vi.mocked(client.getAnimeDetail).mockResolvedValue(detail)
    vi.mocked(client.getAnimes).mockResolvedValue([animeInfo({})])
    vi.mocked(client.getTorrents).mockResolvedValue([])
    vi.mocked(client.getLastCheck).mockResolvedValue({ finished_at: '2026-11-24T12:00:00Z', pass_error: '', limits: [], problems: [] })

    render(AnimeDetail, { props: { params: { id: '42' } } })
    for (let i = 0; i < 4; i++) await tick()

    expect(screen.getAllByText('In Shoko').length).toBeGreaterThan(0)
    expect(screen.getAllByText('Frieren/01.mkv').length).toBeGreaterThan(0)
    expect(screen.getByTestId('shoko-panel-status').textContent).toContain('In Shoko as Sousou no Frieren')
    // Shoko can't reach episode 2's file: still in Shoko, with a warning.
    expect(screen.getAllByText(/Shoko can't reach its file right now/).length).toBeGreaterThan(0)
  })

  it("says Waiting for Shoko for a download handed to Shoko's drop folder, until Shoko has it", async () => {
    const detail: AnimeDetailResponse = {
      anime_id: 42, total_episodes: 2, progress: 0, status: 'CURRENT',
      shoko: { status: 'matched', series_id: 1, series_name: 'Sousou no Frieren' },
      episodes: [
        { episode_number: 1, airing_at: 0, time_until_airing: 0, is_aired: true, is_watched: false, is_downloaded: true, download_date: '2026-11-24T12:00:00Z', episode_hash: 'h1', handed_to_shoko: true },
        { episode_number: 2, airing_at: 0, time_until_airing: 0, is_aired: true, is_watched: false, is_downloaded: true, download_date: '2026-11-24T12:00:00Z', episode_hash: 'h2', handed_to_shoko: true, in_shoko: true, shoko_file: 'Frieren/02.mkv' },
      ],
    }
    vi.mocked(client.getAnimeDetail).mockResolvedValue(detail)
    vi.mocked(client.getAnimes).mockResolvedValue([animeInfo({})])
    vi.mocked(client.getTorrents).mockResolvedValue([])
    vi.mocked(client.getLastCheck).mockResolvedValue({ finished_at: '2026-11-24T12:00:00Z', pass_error: '', limits: [], problems: [] })

    render(AnimeDetail, { props: { params: { id: '42' } } })
    for (let i = 0; i < 4; i++) await tick()

    expect(screen.getAllByText('Waiting for Shoko').length).toBeGreaterThan(0)
    expect(screen.getAllByText('In Shoko').length).toBeGreaterThan(0)
    expect(screen.queryAllByText('Downloaded').length).toBe(0)
  })
})
