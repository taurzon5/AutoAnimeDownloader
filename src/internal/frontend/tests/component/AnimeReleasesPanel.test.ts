import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/svelte'
import { tick } from 'svelte'
import AnimeReleasesPanel from '../../src/components/AnimeReleasesPanel.svelte'
import * as client from '../../src/lib/api/client.js'

vi.mock('../../src/lib/api/client.js', () => ({ getAnimeReleases: vi.fn() }))

const flush = async () => {
  for (let i = 0; i < 4; i++) {
    await tick()
    await Promise.resolve()
  }
}

describe('AnimeReleasesPanel', () => {
  beforeEach(() => vi.mocked(client.getAnimeReleases).mockReset())

  it('shows one card per torrent, with its episodes, state, tags and how it came', async () => {
    vi.mocked(client.getAnimeReleases).mockResolvedValue([
      {
        hash: 'pack', name: '[Erai-raws] Show 01-12 [1080p CR WEB-DL AVC AAC]', detail_url: 'https://nyaa.si/view/1',
        episodes: [6, 7, 8, 9, 10, 11], origin: 'search', added_at: '2026-11-24T10:00:00Z', size: 9 * 2 ** 30,
        seeders: 40, leechers: 3, trusted: true, state: 'seeding', progress: 1, recorded: true,
        release: { group: 'erai-raws', resolution: '1080p', source: 'web-dl', service: 'cr', video_codec: 'h.264', audio_codec: 'aac', audio_languages: ['ja'], subtitle_languages: ['en'], assumed: ['audio_languages', 'subtitle_languages'] },
      },
      { hash: 'old', name: 'old torrent', episodes: [3], state: 'missing', progress: 0, recorded: false, release: {} },
    ])
    render(AnimeReleasesPanel, { props: { animeId: 7 } })
    await flush()

    expect(client.getAnimeReleases).toHaveBeenCalledWith(7)
    const cards = screen.getAllByTestId('release-card')
    expect(cards).toHaveLength(2)
    const pack = cards[0]
    expect(pack.querySelector('a')?.getAttribute('href')).toBe('https://nyaa.si/view/1')
    expect(pack.textContent).toContain('Episodes 6–11')
    expect(pack.textContent).toContain('Seeding')
    expect(pack.textContent).toContain('Found by a check')
    expect(pack.textContent).toContain('40 seeders, 3 leechers when chosen')
    expect(pack.textContent).toContain('Trusted')
    const tags = [...pack.querySelectorAll('[data-testid="release-tags"] span')].map((s) => s.textContent?.trim())
    expect(tags).toContain('WEB-DL CR')
    expect(tags).toContain('H.264')
    expect(tags).toContain('Audio JA?')
    expect(tags).not.toContain('WEB-DL CR?')

    const old = cards[1]
    expect(old.textContent).toContain('Episode 3')
    expect(old.textContent).toContain('Not in the torrent client')
    expect(old.textContent).toContain('Added before release details were kept')
  })

  it('shows nothing for an anime with no torrent', async () => {
    vi.mocked(client.getAnimeReleases).mockResolvedValue([])
    render(AnimeReleasesPanel, { props: { animeId: 7 } })
    await flush()
    expect(screen.queryByTestId('releases-panel')).toBeNull()
  })

  it('reads the cards again when the page reloads', async () => {
    vi.mocked(client.getAnimeReleases).mockResolvedValue([])
    const { rerender } = render(AnimeReleasesPanel, { props: { animeId: 7, tick: 1 } })
    await flush()
    await rerender({ animeId: 7, tick: 2 })
    await flush()
    expect(client.getAnimeReleases).toHaveBeenCalledTimes(2)
  })
})
