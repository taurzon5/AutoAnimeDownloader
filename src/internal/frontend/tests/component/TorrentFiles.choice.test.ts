import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte'
import TorrentFiles from '../../src/components/TorrentFiles.svelte'
import * as client from '../../src/lib/api/client.js'
import { locale } from '../../src/lib/stores/locale.js'

vi.mock('../../src/lib/api/client.js', async () => {
  const actual = await vi.importActual<typeof client>('../../src/lib/api/client.js')
  return {
    ApiError: actual.ApiError,
    getTorrentFiles: vi.fn(),
    chooseTorrentFiles: vi.fn(),
  }
})

const getTorrentFiles = vi.mocked(client.getTorrentFiles)
const chooseTorrentFiles = vi.mocked(client.chooseTorrentFiles)

const files = [
  { path: 'Pack/Show - 01.mkv', size: 10, bytes_completed: 10, episode: 1 },
  { path: 'Pack/Show - 02.mkv', size: 10, bytes_completed: 0, episode: 2, skipped: true },
  { path: 'Pack/Show - 03.mkv', size: 10, bytes_completed: 10, episode: 3 },
]

beforeEach(() => {
  vi.clearAllMocks()
  locale.set('en')
  getTorrentFiles.mockResolvedValue(files)
  chooseTorrentFiles.mockResolvedValue({ skipped: 1, wanted: 1, deleted: 1, kept: 0, records_removed: 1, records_added: 1 })
})

// The Downloads panel's boxes (decisions/165): read-only unless selectable; unticking asks first,
// since a file on disk is deleted, and sends the change with the optional block.
describe('TorrentFiles — choosing files', () => {
  it('has no boxes when not selectable', async () => {
    render(TorrentFiles, { hash: 'abc', mode: 'raw' })
    await screen.findByText('Show - 01.mkv')
    expect(screen.queryByRole('checkbox')).toBeNull()
    expect(screen.getByText('Not downloaded')).toBeTruthy()
  })

  it('sends what changed after a confirm, with block when ticked', async () => {
    render(TorrentFiles, { hash: 'abc', mode: 'raw', selectable: true })
    const first = await screen.findByRole('checkbox', { name: 'Download Show - 01.mkv' })
    expect(first).toBeChecked()
    expect(screen.getByRole('checkbox', { name: 'Download Show - 02.mkv' })).not.toBeChecked()

    await fireEvent.click(first)
    await fireEvent.click(screen.getByRole('checkbox', { name: 'Download Show - 02.mkv' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Save file choice' }))
    await screen.findByRole('dialog')
    expect(screen.getByText('Stop downloading 1 file?')).toBeTruthy()
    await fireEvent.click(screen.getByRole('checkbox', { name: "Don't download those episodes automatically" }))
    await fireEvent.click(screen.getByRole('button', { name: 'Stop downloading' }))

    await waitFor(() => expect(chooseTorrentFiles).toHaveBeenCalledWith('abc', {
      skip: ['Pack/Show - 01.mkv'],
      want: ['Pack/Show - 02.mkv'],
      block: true,
    }))
  })

  it('ticking a skipped file back saves without asking', async () => {
    render(TorrentFiles, { hash: 'abc', mode: 'raw', selectable: true })
    await fireEvent.click(await screen.findByRole('checkbox', { name: 'Download Show - 02.mkv' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Save file choice' }))
    await waitFor(() => expect(chooseTorrentFiles).toHaveBeenCalledWith('abc', { skip: [], want: ['Pack/Show - 02.mkv'], block: false }))
    expect(screen.queryByRole('dialog')).toBeNull()
  })
})
