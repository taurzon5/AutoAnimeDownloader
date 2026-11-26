import { describe, it, expect } from 'vitest'
import { mediaFormatLabel, mediaStatusLabel } from '../../src/lib/domain/mediaLabels.js'

// Review finding 33: AniList's codes were shown raw ("TV SHORT · NOT YET RELEASED").
describe('mediaLabels', () => {
  it('reads AniList codes as words, and an unknown code with spaces', () => {
    expect(mediaFormatLabel('TV')).toBe('TV')
    expect(mediaFormatLabel('TV_SHORT')).toBe('TV short')
    expect(mediaStatusLabel('NOT_YET_RELEASED')).toBe('Not yet released')
    expect(mediaStatusLabel('RELEASING')).toBe('Releasing')
    expect(mediaFormatLabel('SOMETHING_NEW')).toBe('SOMETHING NEW')
  })
})
