import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'
import { tick } from 'svelte'
import Config from '../../src/routes/Config.svelte'
import * as client from '../../src/lib/api/client.js'

vi.mock('../../src/lib/api/client.js', () => ({
  getConfig: vi.fn(),
  updateConfig: vi.fn(),
  triggerCheck: vi.fn(),
}))

const flush = async () => {
  for (let i = 0; i < 5; i++) {
    await tick()
    await Promise.resolve()
  }
}

describe('Config — load failure', () => {
  // The form's starting values are placeholders, not the user's config: rendering the form after a
  // failed load let a Save overwrite the real config with them.
  it('shows a retry instead of the form when the config fails to load', async () => {
    vi.mocked(client.getConfig).mockRejectedValueOnce(new Error('daemon unreachable'))

    const { container } = render(Config)
    await flush()

    expect(container.querySelector('form')).toBeNull()
    expect(screen.getByRole('alert')).toBeTruthy()
    expect(client.updateConfig).not.toHaveBeenCalled()

    vi.mocked(client.getConfig).mockReturnValueOnce(new Promise(() => {}) as never)
    const retry = screen.getByRole('button')
    await fireEvent.click(retry)
    await flush()
    expect(client.getConfig).toHaveBeenCalledTimes(2)
  })
})
