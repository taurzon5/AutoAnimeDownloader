import { describe, it, expect } from 'vitest'
import { render, fireEvent } from '@testing-library/svelte'
import { tick } from 'svelte'
import RedownloadMissingDialog from '../../src/components/RedownloadMissingDialog.svelte'
import type { TorrentInfo } from '../../src/lib/api/client.js'

function torrent(hash: string, episode: number): TorrentInfo {
  return {
    hash, name: `[Grp] Show - ${episode}`, status: 'stopped', queue_position: 0, completed: true,
    anime_name: 'Show', anime_id: 1, episode_number: episode, is_batch: false,
    bytes_completed: 0, bytes_total: 10, bytes_uploaded: 0, progress: 1, download_speed: 0,
    upload_speed: 0, peers_total: 0, eta_seconds: null, seeded_for_seconds: 0, files_missing: true,
  }
}

const list = [torrent('aaa', 1), torrent('bbb', 2), torrent('ccc', 3)]

function confirmButton(getAllByRole: (role: string) => HTMLElement[]) {
  const button = getAllByRole('button').find((b) => /^Redownload \d/.test(b.textContent?.trim() ?? ''))
  expect(button).toBeTruthy()
  return button as HTMLElement
}

describe('RedownloadMissingDialog', () => {
  it('lists every torrent with missing files, all ticked', () => {
    const { getAllByRole, getByText } = render(RedownloadMissingDialog, { props: { open: true, torrents: list } })
    const boxes = getAllByRole('checkbox') as HTMLInputElement[]
    expect(boxes).toHaveLength(3)
    expect(boxes.every((b) => b.checked)).toBe(true)
    expect(getByText('Show — 2')).toBeInTheDocument()
    expect(confirmButton(getAllByRole).textContent).toContain('3')
  })

  it('sends only the torrents left ticked', async () => {
    let hashes: string[] | null = null
    const { getAllByRole } = render(RedownloadMissingDialog, {
      props: { open: true, torrents: list },
      events: { confirm: (e: CustomEvent<{ hashes: string[] }>) => { hashes = e.detail.hashes } },
    })
    const boxes = getAllByRole('checkbox') as HTMLInputElement[]
    await fireEvent.click(boxes[1])

    const button = confirmButton(getAllByRole)
    expect(button.textContent).toContain('2')
    await fireEvent.click(button)
    expect(hashes).toEqual(['aaa', 'ccc'])
  })

  // The Downloads list refreshes every few seconds while the dialog is up: a refresh must not
  // tick again what the user unticked. Reopening starts over with everything ticked.
  it('keeps the choice across a list refresh and resets it on the next opening', async () => {
    const { getAllByRole, rerender } = render(RedownloadMissingDialog, { props: { open: true, torrents: list } })
    await fireEvent.click((getAllByRole('checkbox') as HTMLInputElement[])[0])

    await rerender({ torrents: list.map((t) => ({ ...t })) })
    await tick()
    expect((getAllByRole('checkbox') as HTMLInputElement[]).map((b) => b.checked)).toEqual([false, true, true])

    await rerender({ open: false })
    await tick()
    await rerender({ open: true })
    await tick()
    expect((getAllByRole('checkbox') as HTMLInputElement[]).every((b) => b.checked)).toBe(true)
  })
})
