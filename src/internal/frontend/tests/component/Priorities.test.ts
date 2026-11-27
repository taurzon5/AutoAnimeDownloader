import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte'
import Priorities from '../../src/routes/Priorities.svelte'
import * as client from '../../src/lib/api/client.js'
import type { Config, Priorities as P } from '../../src/lib/api/client.js'

vi.mock('../../src/lib/api/client.js', () => ({
  getConfig: vi.fn(),
  getPriorityDefaults: vi.fn(),
  updatePriorities: vi.fn(),
  getQueues: vi.fn(),
  createQueue: vi.fn(),
  renameQueue: vi.fn(),
  orderQueues: vi.fn(),
  deleteQueue: vi.fn(),
  updateQueuePriorities: vi.fn(),
}))

const getConfig = vi.mocked(client.getConfig)
const getPriorityDefaults = vi.mocked(client.getPriorityDefaults)
const updatePriorities = vi.mocked(client.updatePriorities)

/** The seven lists minus criteria_order, the only one without an add field. */
const LISTS_WITH_ADD = 6

const CRITERIA = ['uncensored', 'source', 'resolution', 'health', 'codec', 'fansub', 'audio', 'size']

function priorities(over: Partial<P> = {}): P {
  return {
    criteria_order: CRITERIA,
    fansubs: ['subsplease', 'erai-raws'],
    resolutions: ['1080p', '720p'],
    sources: ['bdremux'],
    codecs: ['h.264', 'hevc', 'av1', 'xvid'],
    audio: ['flac'],
    ignore_list: ['[dub]'],
    ...over,
  }
}

/** Renders with the user's config diverging (or not) from the backend defaults. jsdom's locale is en. */
async function setup(userPriorities: P = priorities(), ready = 'Codec') {
  getConfig.mockResolvedValue({ priorities: userPriorities } as Config)
  getPriorityDefaults.mockResolvedValue(priorities())
  render(Priorities)
  await waitFor(() => expect(getConfig).toHaveBeenCalled())
  await screen.findByText(ready)
}

beforeEach(() => vi.clearAllMocks())

describe('Priorities', () => {
  // Unchecking doesn't remove it from the screen: a token outside the list already ranks as the
  // worst in the backend, so it stays visible, unchecked, and comes back with one click.
  it("shows the canonical token missing from the user's list, unchecked", async () => {
    await setup(priorities({ codecs: ['h.264', 'hevc'] }))

    // The Checkbox's "✓" glyph is part of the <label>'s textContent, hence the regex matcher.
    expect((screen.getByLabelText(/Use av1$/) as HTMLInputElement).checked).toBe(false)
    expect((screen.getByLabelText(/Use h\.264$/) as HTMLInputElement).checked).toBe(true)
  })

  it('checking/unchecking adds to and removes from the saved list', async () => {
    await setup(priorities({ codecs: ['h.264', 'hevc', 'av1', 'xvid'] }))

    await fireEvent.click(screen.getByLabelText(/Use av1$/))
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(updatePriorities).toHaveBeenCalled())
    expect(updatePriorities.mock.calls[0][0].codecs).toEqual(['h.264', 'hevc', 'xvid'])
  })

  // ✕ only on what the user created (including the inert leftovers of an old config.json, like "x265").
  it('only offers to remove an item that is not in the default', async () => {
    await setup(priorities({ codecs: ['h.264', 'x265'] }))

    expect(screen.getByLabelText('Remove x265')).toBeTruthy()
    expect(screen.queryByLabelText('Remove h.264')).toBeNull()
  })

  // criteria_order is a closed set (the backend silently skips a criterion it doesn't know) and
  // all eight always show, checked or not: there's no add field there.
  it('criteria order has no add field, and shows the missing criterion unchecked', async () => {
    await setup(priorities({ criteria_order: ['resolution', 'fansub'] }))

    expect(screen.getAllByPlaceholderText('Add item')).toHaveLength(LISTS_WITH_ADD)
    expect((screen.getByLabelText(/Use codec$/) as HTMLInputElement).checked).toBe(false)
    expect((screen.getByLabelText(/Use resolution$/) as HTMLInputElement).checked).toBe(true)
  })

  it('the codec preset only promotes what is already in the list', async () => {
    await setup(priorities({ codecs: ['hevc', 'av1', 'h.264', 'xvid'] }))

    await fireEvent.click(screen.getByRole('button', { name: 'I prefer compatibility' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(updatePriorities).toHaveBeenCalled())
    expect(updatePriorities.mock.calls[0][0].codecs).toEqual(['h.264', 'hevc', 'av1', 'xvid'])
  })
})

describe('Priorities — unsaved changes', () => {
  // Dirty is "differs from what was loaded", not "was touched".
  it('a change makes it dirty, undoing it clears it, saving clears it', async () => {
    const { get } = await import('svelte/store')
    const { hasUnsavedChanges } = await import('../../src/lib/stores/unsavedChanges.js')
    await setup()
    expect(get(hasUnsavedChanges)).toBe(false)

    await fireEvent.click(screen.getByLabelText(/Use xvid$/))
    expect(get(hasUnsavedChanges)).toBe(true)
    await fireEvent.click(screen.getByLabelText(/Use xvid$/))
    expect(get(hasUnsavedChanges)).toBe(false)

    await fireEvent.click(screen.getByLabelText(/Use xvid$/))
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(get(hasUnsavedChanges)).toBe(false))
  })
})

describe('Priorities — download queues (decisions/164)', () => {
  const getQueues = vi.mocked(client.getQueues)
  const createQueue = vi.mocked(client.createQueue)
  const orderQueues = vi.mocked(client.orderQueues)
  const updateQueuePriorities = vi.mocked(client.updateQueuePriorities)

  function queue(over: Partial<client.Queue> = {}): client.Queue {
    return { id: 'default', name: 'Default', default: true, animes: [], priorities: priorities(), ...over }
  }

  const twoQueues = () => [
    queue(),
    queue({ id: 'judas', name: 'Judas first', default: false, animes: [21, 22], priorities: priorities({ codecs: ['hevc', 'h.264'], ignore_list: [] }) }),
  ]

  // The queues come first; a queue's criteria open on a click on it, and a second click closes them.
  it('lists the queues first and opens the criteria of the one clicked', async () => {
    getQueues.mockResolvedValue(twoQueues())
    await setup(priorities(), 'Judas first')
    // Twice: beside the name on a wide screen, under it on a phone (CSS picks one).
    expect(screen.getAllByText('2 anime')).toHaveLength(2)
    expect(screen.queryByText('Codec')).toBeNull()
    expect(screen.getByText('Click a queue above to change its criteria.')).toBeTruthy()

    const open = screen.getByRole('button', { name: 'Change the criteria of Judas first' })
    await fireEvent.click(open)
    expect(open.getAttribute('aria-expanded')).toBe('true')
    expect(screen.getByRole('heading', { name: 'Criteria of Judas first' })).toBeTruthy()
    expect(screen.getByText('Codec')).toBeTruthy()

    await fireEvent.click(open)
    expect(screen.queryByText('Codec')).toBeNull()
  })

  it("edits and saves the clicked queue's ranking, with the ignore list shared", async () => {
    getQueues.mockResolvedValue(twoQueues())
    await setup(priorities(), 'Judas first')

    await fireEvent.click(screen.getByRole('button', { name: 'Change the criteria of Judas first' }))
    expect(screen.getByText(/The block list is shared by every queue/)).toBeTruthy()
    expect(screen.queryByText('[dub]')).toBeNull()

    await fireEvent.click(screen.getByLabelText(/Use av1$/))
    expect(screen.getAllByText('Unsaved')).toHaveLength(2) // beside the name, or under it on a phone
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(updateQueuePriorities).toHaveBeenCalled())
    expect(updateQueuePriorities.mock.calls[0][0]).toBe('judas')
    expect(updateQueuePriorities.mock.calls[0][1].codecs).toEqual(['hevc', 'h.264', 'av1'])
    expect(updatePriorities).not.toHaveBeenCalled()
    await waitFor(() => expect(screen.queryAllByText('Unsaved')).toHaveLength(0))
  })

  // Advanced: one set of criteria copied onto the queues ticked, each still editable on its own;
  // the block list, shared by every queue, is not part of it.
  it('gives the same criteria to the queues ticked, keeping their block list', async () => {
    getQueues.mockResolvedValue(twoQueues())
    await setup(priorities(), 'Judas first')

    const details = screen.getByTestId('same-criteria') as HTMLDetailsElement
    details.open = true
    await fireEvent(details, new Event('toggle'))
    await screen.findByText(/Set the criteria once/)
    expect(screen.queryByText('Block list')).toBeNull()

    // Starts from the default queue's criteria: drop av1 from them, then give them to Judas only.
    await fireEvent.click(screen.getByLabelText(/Use av1$/))
    await fireEvent.click(screen.getByRole('checkbox', { name: /Default$/ }))
    await fireEvent.click(screen.getByRole('button', { name: 'Apply to 1 queue' }))

    await fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(updateQueuePriorities).toHaveBeenCalledTimes(1))
    expect(updateQueuePriorities.mock.calls[0][0]).toBe('judas')
    expect(updateQueuePriorities.mock.calls[0][1].codecs).toEqual(['h.264', 'hevc', 'xvid'])
    expect(updateQueuePriorities.mock.calls[0][1].fansubs).toEqual(['subsplease', 'erai-raws'])
    expect(updateQueuePriorities.mock.calls[0][1].ignore_list).toEqual([])
    expect(updatePriorities).not.toHaveBeenCalled()
  })

  // Like an open form, edits to the draft that were not applied yet are lost on leaving
  // (decisions/129).
  it('counts edits to the draft not applied yet as unsaved', async () => {
    const { get } = await import('svelte/store')
    const { hasUnsavedChanges } = await import('../../src/lib/stores/unsavedChanges.js')
    getQueues.mockResolvedValue(twoQueues())
    await setup(priorities(), 'Judas first')

    const details = screen.getByTestId('same-criteria') as HTMLDetailsElement
    details.open = true
    await fireEvent(details, new Event('toggle'))
    await screen.findByText(/Set the criteria once/)
    expect(get(hasUnsavedChanges)).toBe(false)

    // xvid, the last one: ticked again it comes back to the end, where it was.
    await fireEvent.click(screen.getByLabelText(/Use xvid$/))
    expect(get(hasUnsavedChanges)).toBe(true)
    await fireEvent.click(screen.getByLabelText(/Use xvid$/))
    expect(get(hasUnsavedChanges)).toBe(false)
  })

  it('opens the only queue at once', async () => {
    getQueues.mockResolvedValue([queue()])
    await setup()
    expect(screen.getByText('Codec')).toBeTruthy()
    expect(screen.queryByTestId('same-criteria')).toBeNull()
  })

  it('makes a queue and reorders the queues', async () => {
    getQueues.mockResolvedValue([queue()])
    createQueue.mockResolvedValue(queue({ id: 'later', name: 'Later', default: false }))
    orderQueues.mockResolvedValue(undefined)
    await setup()

    await fireEvent.input(screen.getByLabelText('New queue name'), { target: { value: 'Later' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Add queue' }))
    await waitFor(() => expect(createQueue).toHaveBeenCalledWith('Later'))

    await fireEvent.click(await screen.findByLabelText('Move Later up'))
    await waitFor(() => expect(orderQueues).toHaveBeenCalledWith(['later', 'default']))
  })

  it('shows no queue section when the queues cannot be read', async () => {
    getQueues.mockRejectedValue(new Error('down'))
    await setup()
    expect(screen.queryByTestId('queues')).toBeNull()
  })
})
