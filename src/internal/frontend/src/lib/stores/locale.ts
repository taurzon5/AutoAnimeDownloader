import { writable } from 'svelte/store'
import { setLocale, locales } from '../i18n/runtime.js'

// The web UI is English only (decisions/156). The store stays because every screen reads text as
// `$locale && m.x()`, the pattern that re-rendered on a language change; it never changes now.
export type Locale = 'en'

const isBrowser = typeof window !== 'undefined'

// A browser that used the Portuguese UI keeps PARAGLIDE_LOCALE in localStorage, and paraglide may
// have left its cookie: setting English here overwrites both.
if (isBrowser) {
  try {
    localStorage.removeItem('PARAGLIDE_LOCALE')
  } catch {
    // storage blocked: nothing to clean
  }
}
setLocale('en', { reload: false })

const { subscribe, set } = writable<Locale>('en')

export const locale = {
  subscribe,
  set: (newLocale: Locale) => set(newLocale),
}

export { locales }
