import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent, render, within } from '@testing-library/svelte'
import { tick } from 'svelte'
import AnimeDetail from '../../src/routes/AnimeDetail.svelte'
import * as client from '../../src/lib/api/client.js'
import type { AnimeDetailResponse, AnimeInfo } from '../../src/lib/api/client.js'

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
  deleteAnime: vi.fn(),
  unblacklistAnime: vi.fn(),
  getLastCheck: vi.fn(),
}))

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(client.getLastCheck).mockResolvedValue({
    finished_at: '0001-01-01T00:00:00Z', pass_error: '', problems: [], limits: [],
  })
  vi.mocked(client.getAnimeDetail).mockResolvedValue(detail)
  vi.mocked(client.getTorrents).mockResolvedValue([])
  vi.mocked(client.deleteAnime).mockResolvedValue({ removed: false, deleted_episodes: 0, blacklisted: true })
  vi.mocked(client.unblacklistAnime).mockResolvedValue(undefined)
})

const detail: AnimeDetailResponse = { anime_id: 42, total_episodes: 1, progress: 0, status: 'CURRENT', episodes: [] }

function animeInfo(overrides: Partial<AnimeInfo> = {}): AnimeInfo {
  return {
    anime_id: 42, name: 'Test Anime', episodes_downloaded: 0, episodes_released: 0, episodes_watched: 0,
    episodes_pending: 0, total_episodes: 0, latest_episode_number: 0, last_download_date: '',
    ...overrides,
  }
}

async function openDialog(anime: AnimeInfo) {
  vi.mocked(client.getAnimes).mockResolvedValue([anime])
  const view = render(AnimeDetail, { props: { params: { id: '42' } } })
  await tick(); await tick()
  await fireEvent.click(view.getByRole('button', { name: /^(delete|excluir)$/i }))
  await tick()
  return { view, dialog: view.getByRole('dialog') }
}

describe('AnimeDetail — Delete (decisions/155)', () => {
  it('an anime added by hand is removed with its files ticked, blacklist unticked', async () => {
    const { dialog } = await openDialog(animeInfo({ is_standalone: true }))
    const boxes = within(dialog).getAllByRole('checkbox') as HTMLInputElement[]
    expect(boxes.map((b) => b.checked)).toEqual([true, false])

    await fireEvent.click(within(dialog).getByRole('button', { name: /^(delete|excluir)$/i }))
    expect(client.deleteAnime).toHaveBeenCalledWith(42, { deleteFiles: true, blacklist: false })
  })

  it('a list anime says the list would bring it back, with blacklist ticked and files unticked', async () => {
    const { dialog } = await openDialog(animeInfo({
      accounts: [{ source: 'anilist', username: 'alice', episodes_watched: 0, episodes_pending: 0 }],
    }))
    expect(dialog.textContent).toMatch(/alice \(AniList\)/)
    const boxes = within(dialog).getAllByRole('checkbox') as HTMLInputElement[]
    expect(boxes.map((b) => b.checked)).toEqual([false, true])

    await fireEvent.click(within(dialog).getByRole('button', { name: /^(blacklist|colocar na lista negra)$/i }))
    expect(client.deleteAnime).toHaveBeenCalledWith(42, { deleteFiles: false, blacklist: true })
  })

  it('a list anime with nothing ticked cannot be confirmed', async () => {
    const { dialog } = await openDialog(animeInfo({
      accounts: [{ source: 'anilist', username: 'alice', episodes_watched: 0, episodes_pending: 0 }],
    }))
    const [, blacklist] = within(dialog).getAllByRole('checkbox')
    await fireEvent.click(blacklist)
    await tick()
    const confirm = within(dialog).getByRole('button', { name: /^(blacklist|colocar na lista negra)$/i })
    expect((confirm as HTMLButtonElement).disabled).toBe(true)
  })

  it('a blacklisted anime offers Remove from blacklist, and the dialog hides the blacklist box', async () => {
    vi.mocked(client.getAnimes).mockResolvedValue([animeInfo({ app_blacklisted: true, is_blacklisted: true })])
    const view = render(AnimeDetail, { props: { params: { id: '42' } } })
    await tick(); await tick()
    await fireEvent.click(view.getByRole('button', { name: /remove from blacklist|tirar da lista negra/i }))
    expect(client.unblacklistAnime).toHaveBeenCalledWith(42)

    await fireEvent.click(view.getByRole('button', { name: /^(delete|excluir)$/i }))
    await tick()
    expect(within(view.getByRole('dialog')).getAllByRole('checkbox')).toHaveLength(1)
  })
})
