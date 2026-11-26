/**
 * AniList's media format and status codes as the user reads them (review finding 33): the codes
 * ("TV_SHORT", "NOT_YET_RELEASED") stayed English in pt-BR. Statuses reuse the labels Settings
 * already shows for the airing statuses to download. An unknown code falls back to the code with
 * spaces, as before.
 */
import * as m from '../i18n/messages.js'

const FORMATS: Record<string, () => string> = {
  TV: () => 'TV',
  TV_SHORT: m.media_format_tv_short,
  MOVIE: m.media_format_movie,
  SPECIAL: m.media_format_special,
  OVA: () => 'OVA',
  ONA: () => 'ONA',
  MUSIC: m.media_format_music,
}

const STATUSES: Record<string, () => string> = {
  RELEASING: m.config_status_releasing,
  FINISHED: m.config_status_finished,
  CANCELLED: m.config_status_cancelled,
  HIATUS: m.config_status_hiatus,
  NOT_YET_RELEASED: m.media_status_not_yet_released,
}

const raw = (code: string) => code.replace(/_/g, ' ')

export function mediaFormatLabel(code: string): string {
  return FORMATS[code]?.() ?? raw(code)
}

export function mediaStatusLabel(code: string): string {
  return STATUSES[code]?.() ?? raw(code)
}
