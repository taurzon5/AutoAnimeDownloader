import { test, expect, type Page } from '@playwright/test'
import { baseConfig, setupStatusPageMocks } from './helpers'

// The Priorities page with two download queues (decisions/164): the queues come first, a click
// opens one queue's criteria, and the queues ticked are edited together.

const ranking = {
  criteria_order: ['uncensored', 'resolution', 'health', 'source', 'codec', 'fansub', 'audio', 'size'],
  fansubs: ['subsplease', 'erai-raws'],
  resolutions: ['1080p', '720p'],
  sources: ['bd', 'web-dl'],
  codecs: ['h.264', 'hevc', 'av1', 'xvid'],
  audio: ['flac', 'aac'],
  ignore_list: ['[dub]'],
}

type Put = { url: string; body: Record<string, unknown> }

async function setup(page: Page): Promise<Put[]> {
  await setupStatusPageMocks(page)
  const puts: Put[] = []
  await page.route('**/api/v1/config', route =>
    route.fulfill({ json: { success: true, data: { ...baseConfig.data, priorities: ranking } } }),
  )
  await page.route('**/api/v1/config/priorities/defaults', route =>
    route.fulfill({ json: { success: true, data: ranking } }),
  )
  await page.route('**/api/v1/queues', route =>
    route.fulfill({
      json: {
        success: true,
        data: [
          { id: 'default', name: 'Default', default: true, animes: [], priorities: ranking },
          { id: 'watch-first', name: 'Watch first', default: false, animes: [21], priorities: { ...ranking, resolutions: ['720p', '1080p'], ignore_list: [] } },
        ],
      },
    }),
  )
  await page.route('**/api/v1/config/priorities', route => {
    puts.push({ url: route.request().url(), body: route.request().postDataJSON() })
    return route.fulfill({ json: { success: true, data: { message: 'ok' } } })
  })
  await page.route('**/api/v1/queues/*/priorities', route => {
    puts.push({ url: route.request().url(), body: route.request().postDataJSON() })
    return route.fulfill({ json: { success: true, data: { message: 'ok' } } })
  })
  return puts
}

test('lists the queues first and opens the criteria of the one clicked', async ({ page }) => {
  await setup(page)
  await page.goto('/#/priorities')

  await expect(page.getByText('Watch first', { exact: true })).toBeVisible()
  await expect(page.getByText('Codec', { exact: true })).toHaveCount(0)

  await page.getByRole('button', { name: 'Change the criteria of Watch first' }).click()
  await expect(page.getByRole('heading', { name: 'Criteria of Watch first' })).toBeVisible()
  await expect(page.getByText('Codec', { exact: true })).toBeVisible()
  // The block list is the default queue's alone.
  await expect(page.getByText('Block list', { exact: true })).toHaveCount(0)

  await page.getByRole('button', { name: 'Close' }).click()
  await expect(page.getByText('Codec', { exact: true })).toHaveCount(0)
})

test('edits the queues ticked together', async ({ page }) => {
  const puts = await setup(page)
  await page.goto('/#/priorities')

  await page.getByRole('checkbox', { name: /Select all queues$/ }).check({ force: true })
  await expect(page.getByTestId('several-queues-note')).toContainText('2 queues at once')
  // A change to the codec list gives it to both; the resolutions, left alone, stay each queue's own.
  await page.getByRole('checkbox', { name: /Use av1$/ }).uncheck({ force: true })
  await page.getByRole('button', { name: 'Save' }).click()

  await expect.poll(() => puts.length).toBe(2)
  const byUrl = (part: string) => puts.find((p) => p.url.includes(part))!.body
  expect(byUrl('/config/priorities').codecs).toEqual(['h.264', 'hevc', 'xvid'])
  expect(byUrl('/config/priorities').resolutions).toEqual(['1080p', '720p'])
  expect(byUrl('/queues/watch-first/priorities').codecs).toEqual(['h.264', 'hevc', 'xvid'])
  expect(byUrl('/queues/watch-first/priorities').resolutions).toEqual(['720p', '1080p'])
  expect(byUrl('/queues/watch-first/priorities').ignore_list).toEqual([])
})

test('the queue list does not overflow at phone width', async ({ page }) => {
  await setup(page)
  await page.setViewportSize({ width: 375, height: 800 })
  await page.goto('/#/priorities')
  await page.getByRole('button', { name: 'Change the criteria of Watch first' }).click()
  await expect(page.getByText('Codec', { exact: true })).toBeVisible()
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(overflow).toBeLessThanOrEqual(0)
})
