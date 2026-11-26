// Deterministic across dev machines/CI regardless of the host's local timezone — formatDate()
// intentionally renders in the *runtime's* local zone (same as the pre-existing
// `new Date(x).toLocaleString()` call sites it's meant to replace), so the test process pins
// TZ instead of forcing UTC inside the implementation.
process.env.TZ = 'UTC'

import { describe, it, expect } from 'vitest'
import { formatSpeed, formatSpeedParts, formatBytes, formatPercent, formatEta, formatDate, formatScore } from '../../src/lib/domain/format'

describe('formatSpeed', () => {
  it('formats zero as an idle dash regardless of locale', () => {
    expect(formatSpeed(0, 'en')).toBe('—')
    expect(formatSpeed(0, 'en')).toBe('—')
  })

  it('formats sub-1024 values as whole bytes', () => {
    expect(formatSpeed(512, 'en')).toBe('512 B/s')
  })

  it('uses a dot decimal separator in en', () => {
    expect(formatSpeed(1536, 'en')).toBe('1.5 KB/s')
  })

  it('groups thousands with a comma for large-in-unit values', () => {
    // 1048470 B/s -> 1023.896... KB/s (stays in KB/s: dividing again would drop under 1024)
    expect(formatSpeed(1048470, 'en')).toBe('1,023.9 KB/s')
  })
})

describe('formatSpeedParts', () => {
  it('splits the number from its unit so the Status hero can size them separately', () => {
    expect(formatSpeedParts(1536, 'en')).toEqual({ value: '1.5', unit: 'KB/s' })
  })

  it('keeps B/s as the unit for sub-1024 values', () => {
    expect(formatSpeedParts(512, 'en')).toEqual({ value: '512', unit: 'B/s' })
  })

  it('returns the idle dash with no unit when there is no traffic', () => {
    expect(formatSpeedParts(0, 'en')).toEqual({ value: '—', unit: '' })
  })

  it('re-joins into exactly what formatSpeed returns', () => {
    for (const bytes of [0, 512, 1536, 1048470, 1073741824]) {
      const { value, unit } = formatSpeedParts(bytes, 'en')
      expect(unit ? `${value} ${unit}` : value).toBe(formatSpeed(bytes, 'en'))
    }
  })
})

describe('formatBytes', () => {
  it('formats sub-1024 values as whole bytes', () => {
    expect(formatBytes(512, 'en')).toBe('512 B')
  })

  it('auto-selects a unit and uses locale-aware separators', () => {
    expect(formatBytes(1048470, 'en')).toBe('1,023.9 KB')
  })

  it('reaches GB for large values', () => {
  })
})

describe('formatPercent', () => {
  it('rounds a 0..1 fraction to a whole percentage', () => {
    expect(formatPercent(0.4567, 'en')).toBe('46%')
    expect(formatPercent(0.4567, 'en')).toBe('46%')
  })

  it('clamps at 0% and 100%', () => {
    expect(formatPercent(0, 'en')).toBe('0%')
  })
})

describe('formatEta', () => {
  it('renders null as a dash', () => {
    expect(formatEta(null, 'en')).toBe('—')
  })

  it('renders seconds under a minute', () => {
    expect(formatEta(45, 'en')).toBe('45s')
  })

  it('renders minutes and seconds', () => {
    expect(formatEta(150, 'en')).toBe('2m 30s')
  })

  it('renders hours and minutes', () => {
    expect(formatEta(7380, 'en')).toBe('2h 3m')
  })

  it('renders days for very long ETAs', () => {
    expect(formatEta(180000, 'en')).toBe('2d 2h')
  })
})

describe('formatDate', () => {
  it('renders a nullish date as a dash', () => {
    expect(formatDate(undefined, 'en')).toBe('—')
    expect(formatDate('', 'en')).toBe('—')
  })

  it('renders month/day/year order in en', () => {
    expect(formatDate('2026-03-04T10:30:00Z', 'en')).toBe('3/4/26, 10:30 AM')
  })
})

describe('formatScore', () => {
  const text = (t: string) => ({ kind: 'text', text: t })

  it('shows the score in the account\'s own format', () => {
    expect(formatScore(8.5, 'en', 85, 'POINT_100')).toEqual(text('85/100'))
    expect(formatScore(8, 'en', 8, 'POINT_10')).toEqual(text('8/10'))
    expect(formatScore(8, 'en', 4, 'POINT_5')).toEqual(text('★ 4/5'))
    expect(formatScore(8.5, 'en', 3, 'POINT_3')).toEqual({ kind: 'smiley', mood: 'good' })
    expect(formatScore(6, 'en', 2, 'POINT_3')).toEqual({ kind: 'smiley', mood: 'ok' })
    expect(formatScore(3.5, 'en', 1, 'POINT_3')).toEqual({ kind: 'smiley', mood: 'bad' })
  })

  it('falls back to the 0–10 score when the format is unknown', () => {
    expect(formatScore(9, 'en')).toEqual(text('9/10'))
    expect(formatScore(8.5, 'en', 85, 'SOMETHING_NEW')).toEqual(text('8.5/10'))
  })

  it('is null for an unscored entry', () => {
    expect(formatScore(0, 'en', 0, 'POINT_100')).toBeNull()
    expect(formatScore(undefined, 'en')).toBeNull()
  })
})
