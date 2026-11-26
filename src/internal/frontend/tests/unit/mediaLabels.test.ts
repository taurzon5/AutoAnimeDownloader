import { describe, it, expect } from 'vitest'
import { mediaFormatLabel, mediaStatusLabel } from '../../src/lib/domain/mediaLabels.js'

// Review finding 33: AniList's codes were shown raw ("TV SHORT · NOT YET RELEASED"), English in pt-BR too.
describe('mediaLabels', () => {
  it('reads AniList codes as words, and an unknown code with spaces', () => {
    expect(mediaFormatLabel('TV')).toBe('TV')
    expect(mediaFormatLabel('TV_SHORT')).toMatch(/^(TV short|TV curto)$/)
    expect(mediaStatusLabel('NOT_YET_RELEASED')).toMatch(/^(Not yet released|Ainda não lançado)$/)
    expect(mediaStatusLabel('RELEASING')).toMatch(/^(Releasing|Em Exibição)$/)
    expect(mediaFormatLabel('SOMETHING_NEW')).toBe('SOMETHING NEW')
  })
})
