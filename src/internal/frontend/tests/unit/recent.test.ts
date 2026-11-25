import { describe, it, expect } from 'vitest'
import { recentShelf, recentCaption, rangesText, RECENT_SHELF_CAP } from '../../src/lib/domain/recent.js'
import type { AnimeInfo, AnimeRecent } from '../../src/lib/api/client.js'
import * as m from '../../src/lib/i18n/messages.js'

const anime = (id: number, recent?: AnimeRecent): AnimeInfo =>
  ({ anime_id: id, name: `A${id}`, episodes_downloaded: 1, episodes_released: 1, episodes_watched: 0, episodes_pending: 0, total_episodes: 0, latest_episode_number: 1, last_download_date: '', recent }) as AnimeInfo

const recent = (minutesAgo: number, ranges = [{ from: 1, to: 1 }], is_batch = false): AnimeRecent => ({
  ranges,
  is_batch,
  downloaded_at: new Date(Date.UTC(2026, 10, 24, 12) - minutesAgo * 60_000).toISOString(),
})

describe('recentShelf', () => {
  it('keeps the 12 newest downloads, newest first, and skips animes without recent', () => {
    const list = [anime(99)]
    for (let i = 1; i <= 15; i++) list.push(anime(i, recent(i)))
    const shelf = recentShelf(list)
    expect(RECENT_SHELF_CAP).toBe(12)
    expect(shelf.map((a) => a.anime_id)).toEqual([1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12])
  })

  it('is empty when nothing is recent', () => {
    expect(recentShelf([anime(1), anime(2)])).toEqual([])
  })
})

describe('recentCaption', () => {
  // The spec's four rows, in both locales.
  const rows: { r: AnimeRecent; en: string; pt: string }[] = [
    { r: recent(0, [{ from: 5, to: 5 }]), en: 'Episode 5', pt: 'Episódio 5' },
    { r: recent(0, [{ from: 1, to: 3 }]), en: 'Episodes 1–3', pt: 'Episódios 1–3' },
    { r: recent(0, [{ from: 1, to: 12 }], true), en: '1–12 · pack', pt: '1–12 · lote' },
    { r: recent(0, [{ from: 1, to: 3 }, { from: 5, to: 5 }], true), en: 'Episodes 1–3, 5', pt: 'Episódios 1–3, 5' },
  ]
  for (const { r, en, pt } of rows) {
    it(en, () => {
      const c = recentCaption(r)
      const text = (locale: 'en' | 'pt-BR') =>
        c.kind === 'episode'
          ? m.recent_caption_episode({ n: String(c.n) }, { locale })
          : c.kind === 'pack'
            ? m.recent_caption_pack({ span: c.span }, { locale })
            : m.recent_caption_episodes({ span: c.span }, { locale })
      expect(text('en')).toBe(en)
      expect(text('pt-BR')).toBe(pt)
    })
  }

  it('writes runs with an en dash', () => {
    expect(rangesText([{ from: 1, to: 2 }, { from: 4, to: 4 }, { from: 6, to: 9 }])).toBe('1–2, 4, 6–9')
  })
})
