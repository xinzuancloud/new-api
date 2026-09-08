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
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useForm } from 'react-hook-form'
import { describe, expect, test, vi } from 'vitest'

import { Button } from '@/components/ui/button'
import { Form } from '@/components/ui/form'

import {
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  buildSettingJSON,
  type ChannelFormValues,
} from '../../../../lib/channel-form'
import { ChannelProtocolRoutingSection } from '../channel-protocol-routing-section'

vi.mock('../../../../lib/protocol-profiles-api', () => ({
  protocolProfilesAPI: {
    catalog: vi
      .fn()
      .mockResolvedValue({ revision: '1', profiles: {}, channels: [] }),
  },
}))

function Editor(props: {
  disabled?: boolean
  onSave?: (value: string) => void
}) {
  const form = useForm<ChannelFormValues>({
    resolver: zodResolver(channelFormSchema),
    defaultValues: {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      name: 'Channel',
      models: 'model',
    },
  })
  return (
    <QueryClientProvider
      client={
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
