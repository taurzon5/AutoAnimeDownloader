import { describe, it, expect } from 'vitest'
import * as m from '../../src/lib/i18n/messages.js'

// Count messages pick the singular with an exact match on 1 (`count=1` / `count=*` in
// messages/en.json), not with `: plural`, and never hedge with "episode(s)". These pin both ends.
const forms = (fn: (i: { count: number }) => string) => [0, 1, 2].map((count) => fn({ count }))

describe('count messages', () => {
  it('singular only for exactly one', () => {
    expect(forms(m.status_animes_count)).toEqual(['0 animes', '1 anime', '2 animes'])
    expect(forms(m.downloads_group_count)).toEqual(['0 torrents', '1 torrent', '2 torrents'])
  })

  it('selects each count on its own when a message has two', () => {
    expect(m.lastcheck_no_seeders({ candidates: 1, seeders: 3 })).toBe('1 torrent found, without at least 3 seeders.')
  })
})
