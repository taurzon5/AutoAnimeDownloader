import { describe, it, expect } from 'vitest'
import {
  allSkipped,
  choiceDiff,
  defaultSkip,
  partlySkipped,
  selectionSize,
  shownName,
  toggleAll,
  toggleOne,
} from '../../src/lib/domain/fileChoice'
import type { TorrentCandidateDetail } from '../../src/lib/api/client'

const megapack: TorrentCandidateDetail = {
  description_html: '',
  nyaa_url: '',
  files: [
    { name: 'Pack/S01/01.mkv', size_bytes: 100 },
    { name: 'Pack/S02/01.mkv', size_bytes: 100 },
    { name: 'Pack/NCOP.mkv', size_bytes: 10 },
    { name: 'Pack/S01/01.ass', size_bytes: 1 },
  ],
  mapping: {
    megapack: true,
    entries: [
      { id: 1, title: 'S1', format: 'TV', episodes: [1], files: [{ name: 'Pack/S01/01.mkv', size_bytes: 100 }], bytes: 100, seed: true },
      { id: 2, title: 'S2', format: 'TV', episodes: [1], files: [{ name: 'Pack/S02/01.mkv', size_bytes: 100 }], bytes: 100 },
    ],
    unmapped: [{ name: 'Pack/NCOP.mkv', size_bytes: 10, reason: 'creditless' }],
    total_bytes: 211,
    mapped_bytes: 200,
  },
}

describe('fileChoice', () => {
  it('starts a megapack with the unmapped videos unticked and an ordinary pack with nothing', () => {
    expect([...defaultSkip(megapack)]).toEqual(['Pack/NCOP.mkv'])
    expect(defaultSkip({ ...megapack, mapping: { ...megapack.mapping!, megapack: false } }).size).toBe(0)
    expect(defaultSkip(null).size).toBe(0)
  })

  it('toggles an entry as a whole and reads all, part or none of it', () => {
    const names = ['a', 'b']
    let skip = toggleAll(names, new Set())
    expect(allSkipped(names, skip)).toBe(true)
    skip = toggleOne('a', skip)
    expect(partlySkipped(names, skip)).toBe(true)
    expect(allSkipped(names, skip)).toBe(false)
    skip = toggleAll(names, skip)
    expect([...skip].sort()).toEqual(['a', 'b'])
    skip = toggleAll(names, skip)
    expect(skip.size).toBe(0)
    expect(allSkipped([], new Set())).toBe(false)
  })

  it('counts the ticked bytes and files against the whole torrent', () => {
    expect(selectionSize(megapack.files, defaultSkip(megapack))).toEqual({ bytes: 201, totalBytes: 211, count: 3, total: 4 })
  })

  it('turns the panel into what to skip and what to want', () => {
    const files = [{ path: 'a', skipped: true }, { path: 'b' }, { path: 'c' }]
    expect(choiceDiff(files, new Set(['b', 'c']))).toEqual({ skip: ['b', 'c'], want: ['a'] })
    expect(choiceDiff(files, new Set(['a']))).toEqual({ skip: [], want: [] })
  })

  it('drops the shared top folder from the shown name only', () => {
    expect(shownName('Pack/S01/01.mkv', megapack.files)).toBe('S01/01.mkv')
    expect(shownName('Pack/S01/01.mkv', [...megapack.files, { name: 'Other/x.mkv' }])).toBe('Pack/S01/01.mkv')
    expect(shownName('single.mkv', [{ name: 'single.mkv' }])).toBe('single.mkv')
  })
})
