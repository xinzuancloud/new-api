/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  act,
  render,
  renderHook,
  screen,
  waitFor,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { useChannelMutateForm } from '../../../hooks/use-channel-mutate-form'
import { useProtocolProbes } from '../../../hooks/use-protocol-probes'
import { CHANNEL_FORM_DEFAULT_VALUES } from '../../../lib/channel-form'
import {
  protocolProfilesAPI,
  type ProbeReport,
  type ProfilesResponse,
} from '../../../lib/protocol-profiles-api'
import { channelSchema } from '../../../types'
import { ProfileBinding } from '../profile-binding'
import { ProfileProbes } from '../profile-probes'

vi.mock('../../../lib/protocol-profiles-api', async (importOriginal) => ({
  ...(await importOriginal<
    typeof import('../../../lib/protocol-profiles-api')
  >()),
  protocolProfilesAPI: {
    bind: vi.fn(),
    run: vi.fn(),
    cancel: vi.fn(),
    create: vi.fn(),
    reports: vi.fn(),
    report: vi.fn(),
    apply: vi.fn(),
  },
}))
vi.mock('@/lib/handle-server-error', () => ({ handleServerError: vi.fn() }))
const initial: ProbeReport = {
  id: 'report-1',
  profile: 'shared',
  profile_revision: 'r1',
  fingerprints: {},
  created_at: 1,
  updated_at: 1,
  max_output_tokens: 256,
  allow_hosted: false,
  status: 'pending',
  cases: [],
}
const catalog: ProfilesResponse = {
  revision: 'r1',
  profiles: { shared: { name: 'Shared', defaults: {} } },
  channels: [
    {
      id: 1,
      name: 'Account 1',
      type: 1,
      base_url: 'https://example.test',
      models: ['model'],
      mapped_models: ['model'],
      profile: 'shared',
      enabled: true,
      account_resource: 'account',
    },
  ],
}
function wrapper(props: { children: ReactNode }) {
  return (
    <QueryClientProvider
      client={
        new QueryClient({
          defaultOptions: {
            queries: { retry: false },
            mutations: { retry: false },
          },
        })
      }
    >
      {props.children}
    </QueryClientProvider>
  )
}
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(protocolProfilesAPI.reports).mockResolvedValue([initial])
})

describe('foreground protocol verification', () => {
  test('runs sequential pending batches to completion', async () => {
    vi.mocked(protocolProfilesAPI.run)
      .mockResolvedValueOnce({ ...initial, status: 'pending' })
      .mockResolvedValueOnce({ ...initial, status: 'completed' })
    const hook = renderHook(useProtocolProbes, { wrapper })
    await act(async () => {
      await hook.result.current.run.mutateAsync(initial)
    })
    expect(protocolProfilesAPI.run).toHaveBeenCalledTimes(2)
    expect(hook.result.current.report?.status).toBe('completed')
    expect(hook.result.current.running).toBe(false)
  })
  test('does not poll an active lease and permits manual resume', async () => {
    vi.mocked(protocolProfilesAPI.run)
      .mockResolvedValueOnce({ ...initial, status: 'running' })
      .mockResolvedValueOnce({ ...initial, status: 'completed' })
    const hook = renderHook(useProtocolProbes, { wrapper })
    await act(async () => {
      await hook.result.current.run.mutateAsync(initial)
    })
    expect(protocolProfilesAPI.run).toHaveBeenCalledTimes(1)
    await act(async () => {
      await hook.result.current.run.mutateAsync({
        ...initial,
        status: 'running',
      })
    })
    expect(protocolProfilesAPI.run).toHaveBeenCalledTimes(2)
  })
  test('closing aborts the active request and prevents another batch', async () => {
    let signal: AbortSignal | undefined
    let finish: ((report: ProbeReport) => void) | undefined
    vi.mocked(protocolProfilesAPI.run).mockImplementation((_id, active) => {
      signal = active
      return new Promise((resolve) => {
        finish = resolve
      })
    })
    const hook = renderHook(useProtocolProbes, { wrapper })
    act(() => hook.result.current.run.mutate(initial))
    await waitFor(() => expect(signal).toBeDefined())
    hook.unmount()
    expect(signal?.aborted).toBe(true)
    await act(async () => {
      finish?.({ ...initial, status: 'pending' })
    })
    expect(protocolProfilesAPI.run).toHaveBeenCalledTimes(1)
  })
  test('cancelling aborts active requests and persists cancellation', async () => {
    let signal: AbortSignal | undefined
    vi.mocked(protocolProfilesAPI.run).mockImplementation((_id, active) => {
      signal = active
      return new Promise((_resolve, reject) =>
        active.addEventListener('abort', () =>
          reject(new DOMException('Aborted', 'AbortError'))
        )
      )
    })
    vi.mocked(protocolProfilesAPI.cancel).mockResolvedValue({
      ...initial,
      status: 'cancelled',
    })
    const hook = renderHook(useProtocolProbes, { wrapper })
    act(() => {
      hook.result.current.setReport(initial)
      hook.result.current.run.mutate(initial)
    })
    await waitFor(() => expect(signal).toBeDefined())
    await act(async () => {
      await hook.result.current.cancel.mutateAsync()
    })
    expect(signal?.aborted).toBe(true)
    expect(protocolProfilesAPI.cancel).toHaveBeenCalledWith('report-1')
    expect(hook.result.current.report?.status).toBe('cancelled')
    expect(protocolProfilesAPI.run).toHaveBeenCalledTimes(1)
  })
  test('loads and resumes a persisted report after mounting anew', async () => {
    const user = userEvent.setup()
    vi.mocked(protocolProfilesAPI.report).mockResolvedValue(initial)
    vi.mocked(protocolProfilesAPI.run).mockResolvedValue({
      ...initial,
      status: 'completed',
    })
    render(<ProfileProbes data={catalog} canOperate canWrite />, { wrapper })
    await user.click(
      await screen.findByRole('combobox', {
        name: 'Recent verification reports',
      })
    )
    await user.click(await screen.findByRole('option'))
    await user.click(
      await screen.findByRole('button', { name: 'Run / resume verification' })
    )
    await waitFor(() =>
      expect(protocolProfilesAPI.run).toHaveBeenCalledWith(
        'report-1',
        expect.any(AbortSignal)
      )
    )
  })
})

describe('profile binding preview', () => {
  test('only applies after preview confirmation with the saved fingerprint', async () => {
    const user = userEvent.setup()
    const preview = {
      revision: 'r1',
      fingerprint: 'opaque',
      changes: [
        {
          id: 1,
          name: 'Account 1',
          before: { enabled: true, profile: 'shared', defaults: {} },
          after: { enabled: true, defaults: {} },
        },
      ],
    }
    vi.mocked(protocolProfilesAPI.bind).mockResolvedValue(preview)
    render(<ProfileBinding data={catalog} canWrite />, { wrapper })
    await user.click(screen.getByRole('checkbox', { name: '1 · Account 1' }))
    await user.click(screen.getByRole('button', { name: 'Preview binding' }))
    await screen.findByRole('alertdialog')
    expect(protocolProfilesAPI.bind).toHaveBeenCalledExactlyOnceWith({
      revision: 'r1',
      profile: '',
      channel_ids: [1],
      preview: true,
    })
    await user.click(screen.getByRole('button', { name: 'Apply' }))
    await waitFor(() =>
      expect(protocolProfilesAPI.bind).toHaveBeenLastCalledWith({
        revision: 'r1',
        profile: '',
        channel_ids: [1],
        preview: false,
        fingerprint: 'opaque',
      })
    )
  })
  test('revision conflicts stay visible and never apply stale data', async () => {
    const user = userEvent.setup()
    vi.mocked(protocolProfilesAPI.bind).mockRejectedValue({
      response: {
        status: 409,
        data: { message: 'protocol_revision_conflict' },
      },
    })
    render(<ProfileBinding data={catalog} canWrite />, { wrapper })
    await user.click(screen.getByRole('checkbox', { name: '1 · Account 1' }))
    await user.click(screen.getByRole('button', { name: 'Preview binding' }))
    expect(
      await screen.findByText(
        'Protocol configuration changed. Refresh and preview again.'
      )
    ).toBeVisible()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(protocolProfilesAPI.bind).toHaveBeenCalledTimes(1)
  })
})

afterEach(() => vi.restoreAllMocks())

test('ordinary channel save refreshes protocol catalog and effective policy queries', async () => {
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
  const client = new QueryClient({
    defaultOptions: {
      queries: { staleTime: Infinity },
      mutations: { retry: false },
    },
  })
  client.setQueryData(['protocol-profiles', 'catalog'], catalog)
  client.setQueryData(['protocol-profiles', 'effective', 1], {
    revision: 'r1',
    profile: 'shared',
    settings: { enabled: true, defaults: {} },
  })
  const currentRow = channelSchema.parse({
    id: 1,
    name: 'Account 1',
    type: 1,
    key: '',
    status: 1,
    created_time: 0,
    test_time: 0,
    response_time: 0,
    balance_updated_time: 0,
  })
  const hook = renderHook(
    () =>
      useChannelMutateForm({
        currentRow,
        isEditing: true,
        isMultiKeyChannel: false,
        onSuccess: () => {},
      }),
    {
      wrapper: (props) => (
        <QueryClientProvider client={client}>
          {props.children}
        </QueryClientProvider>
      ),
    }
  )
  await act(async () => {
    await hook.result.current.mutateAsync({
      ...CHANNEL_FORM_DEFAULT_VALUES,
      name: 'Renamed',
      models: 'model',
    })
  })
  expect(
    client.getQueryState(['protocol-profiles', 'catalog'])?.isInvalidated
  ).toBe(true)
  expect(
    client.getQueryState(['protocol-profiles', 'effective', 1])?.isInvalidated
  ).toBe(true)
  hook.unmount()
  client.clear()
})
