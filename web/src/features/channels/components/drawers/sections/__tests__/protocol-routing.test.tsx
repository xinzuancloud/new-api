import { zodResolver } from '@hookform/resolvers/zod'
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
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useForm } from 'react-hook-form'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { Button } from '@/components/ui/button'
import { Form } from '@/components/ui/form'

import {
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  buildSettingJSON,
  type ChannelFormValues,
} from '../../../../lib/channel-form'
import { protocolProfilesAPI } from '../../../../lib/protocol-profiles-api'
import { ChannelProtocolRoutingSection } from '../channel-protocol-routing-section'

vi.mock('../../../../lib/protocol-profiles-api', () => ({
  protocolProfilesAPI: {
    effective: vi.fn(),
    catalog: vi
      .fn()
      .mockResolvedValue({ revision: '1', profiles: {}, channels: [] }),
  },
}))

function Editor(props: {
  disabled?: boolean
  channelId?: number
  client?: QueryClient
  values?: Partial<ChannelFormValues>
  onSave?: (value: string) => void
}) {
  const form = useForm<ChannelFormValues>({
    resolver: zodResolver(channelFormSchema),
    defaultValues: {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      name: 'Channel',
      models: 'model',
      ...props.values,
    },
  })
  return (
    <QueryClientProvider
      client={
        props.client ??
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <Form {...form}>
        <form
          onSubmit={form.handleSubmit((values) =>
            props.onSave?.(buildSettingJSON(values))
          )}
        >
          <ChannelProtocolRoutingSection
            form={form}
            channelId={props.channelId}
            disabled={props.disabled}
          />
          <Button type='submit'>Save</Button>
        </form>
      </Form>
    </QueryClientProvider>
  )
}

describe('channel protocol routing editor', () => {
  test('enabling creates a safe unverified draft and disabling preserves it', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn()
    render(<Editor onSave={onSave} />)
    const toggle = screen.getByRole('switch', {
      name: 'Enable protocol routing',
    })
    await user.click(toggle)
    const defaults = screen.getByRole('textbox', {
      name: 'Default protocol policy',
    })
    const policy = JSON.parse((defaults as HTMLTextAreaElement).value)
    expect(policy.loss_policy).toBe('safe')
    expect(policy.endpoints[0].verified).toBe(false)
    await user.click(toggle)
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(onSave).toHaveBeenCalled()
    expect(JSON.parse(onSave.mock.calls[0][0]).protocol_routing).toMatchObject({
      enabled: false,
      defaults: policy,
    })
  })

  test('invalid model policies show an accessible error and block save', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn()
    render(<Editor onSave={onSave} />)
    await user.click(
      screen.getByRole('switch', { name: 'Enable protocol routing' })
    )
    const overrides = screen.getByRole('textbox', {
      name: 'Model protocol overrides',
    })
    fireEvent.input(overrides, {
      target: { value: '{"mapped-model":{"loss_policy":"invalid"}}' },
    })
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(
      await screen.findByText(
        'Model overrides must map upstream model names to valid protocol differences'
      )
    ).toBeVisible()
    expect(overrides).toHaveAttribute('aria-invalid', 'true')
    expect(onSave).not.toHaveBeenCalled()
  })

  test('locked channel settings cannot enable routing', async () => {
    render(<Editor disabled />)
    const toggle = screen.getByRole('switch', {
      name: 'Enable protocol routing',
    })
    expect(toggle).toHaveAttribute('aria-disabled', 'true')
    await userEvent.click(toggle)
    expect(toggle).toHaveAttribute('aria-checked', 'false')
  })
})

beforeEach(() => {
  vi.mocked(protocolProfilesAPI.effective).mockReset()
})

const effectiveA: Awaited<ReturnType<typeof protocolProfilesAPI.effective>> = {
  revision: 'r1',
  profile: 'shared',
  settings: {
    enabled: true,
    defaults: {
      entry_formats: ['openai'],
      endpoints: [
        { format: 'openai', path: '/v1/chat/completions', verified: false },
      ],
      loss_policy: 'safe',
    },
  },
}

test.each(['success', 'failure'] as const)(
  'detaching with cached policy awaits a fresh saved policy: %s',
  async (outcome) => {
    const user = userEvent.setup()
    const client = new QueryClient({
      defaultOptions: {
        queries: { retry: false, staleTime: 10000 },
        mutations: { retry: false },
      },
    })
    client.setQueryData(['protocol-profiles', 'effective', 1], effectiveA)
    let resolve!: (
      value: Awaited<ReturnType<typeof protocolProfilesAPI.effective>>
    ) => void
    let reject!: (error: Error) => void
    vi.mocked(protocolProfilesAPI.effective).mockReturnValue(
      new Promise((yes, no) => {
        resolve = yes
        reject = no
      })
    )
    const onSave = vi.fn()
    const view = render(
      <Editor
        channelId={1}
        client={client}
        onSave={onSave}
        values={{
          protocol_routing_enabled: true,
          protocol_routing_profile: 'shared',
          protocol_routing_defaults: '{}',
          protocol_routing_models: '{}',
        }}
      />
    )
    await waitFor(() =>
      expect(
        screen.getByRole('combobox', { name: 'Shared protocol profile' })
      ).not.toHaveAttribute('aria-disabled', 'true')
    )
    await user.click(
      screen.getByRole('combobox', { name: 'Shared protocol profile' })
    )
    await user.click(
      await screen.findByRole('option', { name: 'Local configuration' })
    )
    const confirm = await screen.findByRole('button', { name: 'Continue' })
    expect(confirm).toBeDisabled()
    await act(async () => {
      if (outcome === 'success') {
        resolve({
          ...effectiveA,
          settings: {
            ...effectiveA.settings,
            defaults: {
              ...effectiveA.settings.defaults,
              loss_policy: 'strict',
            },
          },
        })
      } else {
        reject(new Error('refresh failed'))
      }
    })
    if (outcome === 'success') {
      await waitFor(() => expect(confirm).toBeEnabled())
      await user.click(confirm)
      await user.click(screen.getByRole('button', { name: 'Save' }))
      await waitFor(() => expect(onSave).toHaveBeenCalled())
      const saved = JSON.parse(onSave.mock.calls[0][0]).protocol_routing
      expect(saved.profile).toBeUndefined()
      expect(saved.defaults.loss_policy).toBe('strict')
    } else {
      expect(
        await within(screen.getByRole('alertdialog')).findByText(
          'Something went wrong!'
        )
      ).toBeVisible()
      expect(confirm).toBeDisabled()
      await user.click(screen.getByRole('button', { name: 'Cancel' }))
      await user.click(screen.getByRole('button', { name: 'Save' }))
      await waitFor(() => expect(onSave).toHaveBeenCalled())
      expect(JSON.parse(onSave.mock.calls[0][0]).protocol_routing.profile).toBe(
        'shared'
      )
    }
    view.unmount()
    client.clear()
  }
)
