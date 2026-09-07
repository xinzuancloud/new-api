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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import {
  createRoutingPolicyFormSchema,
  parseRoutingPolicy,
  routingPolicyFormDefaults,
  serializeRoutingPolicy,
} from '../lib/routing-policy'
import { RoutingPolicySection } from '../routing-policy-section'

const clients: QueryClient[] = []
afterEach(() => clients.splice(0).forEach((client) => client.clear()))

function renderPolicy(value = '') {
  const client = new QueryClient({
    defaultOptions: { mutations: { retry: false } },
  })
  clients.push(client)
  const actions = document.createElement('div')
  const result = render(
    <QueryClientProvider client={client}>
      <SettingsPageProvider actionsContainer={actions}>
        <RoutingPolicySection defaultValue={value} />
      </SettingsPageProvider>
    </QueryClientProvider>
  )
  result.container.append(actions)
  return result
}

const defaults = {
  enabled: false,
  group_tag_order: {},
  max_attempts_per_tag: 3,
  max_total_attempts: 0,
  rate_limit_cooldown_seconds: 60,
  quota_cooldown_seconds: 3600,
  quota_error_keywords: [],
  request_timeout_seconds: 300,
}

describe('routing policy settings', () => {
  test('missing configuration keeps routing disabled and applies bounded defaults', () => {
    expect(parseRoutingPolicy('')).toEqual(defaults)
    expect(parseRoutingPolicy('{}')).toEqual(defaults)
    expect(() => parseRoutingPolicy('{')).toThrow()
  })

  test.each([
    { max_attempts_per_tag: 0, max_total_attempts: 32 },
    { max_attempts_per_tag: 3, max_total_attempts: 0 },
    { max_attempts_per_tag: 256, max_total_attempts: 1024 },
  ])('loading and saving attempt modes preserves %j', (attempts) => {
    const stored = JSON.stringify({ ...defaults, ...attempts })
    const values = createRoutingPolicyFormSchema((key) => key).parse(
      routingPolicyFormDefaults(stored)
    )
    expect(JSON.parse(serializeRoutingPolicy(values))).toEqual({
      ...defaults,
      ...attempts,
    })
  })

  test('legacy policy without a total attempt limit inherits retry settings', () => {
    const values = routingPolicyFormDefaults('{"max_attempts_per_tag":3}')
    expect(values.max_attempts_per_tag).toBe(3)
    expect(values.max_total_attempts).toBe(0)
  })

  test('all-channel mode and a separate request limit save together with stopping behavior explained', async () => {
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    renderPolicy()
    const attempts = screen.getByRole('spinbutton', {
      name: 'Attempts per tag (0 = all available)',
    })
    const total = screen.getByRole('spinbutton', {
      name: 'Total attempts per request',
    })
    expect(attempts).toHaveAttribute('max', '256')
    expect(total).toHaveAttribute('max', '1024')
    expect(total).toHaveValue(0)
    expect(total).toHaveAccessibleDescription(
      /Zero uses Retry Times plus the initial attempt/
    )
    expect(
      screen.getByText(
        'When trying all channels, reaching the total attempt limit or timeout ends the request; it does not skip to another supplier.'
      )
    ).toBeVisible()
    fireEvent.change(attempts, { target: { value: '0' } })
    fireEvent.change(total, { target: { value: '32' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() => expect(put).toHaveBeenCalledOnce())
    expect(put).toHaveBeenCalledWith('/api/option/', {
      key: 'RoutingPolicy',
      value: JSON.stringify({
        ...defaults,
        max_attempts_per_tag: 0,
        max_total_attempts: 32,
      }),
    })
  })

  test('saving rows preserves tag priority and explicit zero cooldowns in one policy', () => {
    const schema = createRoutingPolicyFormSchema((key) => key)
    const values = schema.parse({
      ...defaults,
      groups: [{ group: ' team-a ', tags: ' primary \n fallback ' }],
      quota_error_keywords: ' balance exhausted \n no quota ',
      rate_limit_cooldown_seconds: 0,
      quota_cooldown_seconds: 0,
    })
    expect(JSON.parse(serializeRoutingPolicy(values))).toEqual({
      ...defaults,
      group_tag_order: { 'team-a': ['primary', 'fallback'] },
      quota_error_keywords: ['balance exhausted', 'no quota'],
      rate_limit_cooldown_seconds: 0,
      quota_cooldown_seconds: 0,
    })
  })

  test.each([
    { max_attempts_per_tag: -1 },
    { max_attempts_per_tag: 257 },
    { max_total_attempts: -1 },
    { max_total_attempts: 1025 },
    { max_total_attempts: 1.5 },
    { request_timeout_seconds: 0 },
    { request_timeout_seconds: 1801 },
    { rate_limit_cooldown_seconds: -1 },
    { rate_limit_cooldown_seconds: 3601 },
    { quota_cooldown_seconds: 604801 },
    { max_attempts_per_tag: 1.5 },
    { groups: [{ group: '', tags: 'primary' }] },
    { groups: [{ group: 'a', tags: '' }] },
    { groups: [{ group: 'a', tags: 'primary\nprimary' }] },
    {
      groups: [
        { group: 'a', tags: 'primary' },
        { group: ' a ', tags: 'fallback' },
      ],
    },
  ])('rejects invalid bounds or ambiguous group rows: %j', (override) => {
    expect(
      createRoutingPolicyFormSchema((key) => key).safeParse({
        ...defaults,
        groups: [],
        quota_error_keywords: '',
        ...override,
      }).success
    ).toBe(false)
  })

  test('adding and removing group rows saves their ordered tags atomically', async () => {
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    renderPolicy()
    const user = userEvent.setup()
    expect(screen.getByText('No group routing rules')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Add group rule' }))
    await user.type(screen.getByRole('textbox', { name: 'Group' }), 'team-a')
    await user.type(
      screen.getByRole('textbox', { name: 'Channel tags in priority order' }),
      'primary\nfallback'
    )
    await user.click(screen.getByRole('button', { name: 'Add group rule' }))
    await user.click(
      screen.getAllByRole('button', { name: 'Remove group rule' })[1]
    )
    const toggle = screen.getByRole('switch', { name: 'Enable routing policy' })
    toggle.focus()
    await user.keyboard(' ')
    expect(toggle).toBeChecked()
    await user.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() => expect(put).toHaveBeenCalledOnce())
    expect(put).toHaveBeenCalledWith('/api/option/', {
      key: 'RoutingPolicy',
      value: JSON.stringify({
        ...defaults,
        enabled: true,
        group_tag_order: { 'team-a': ['primary', 'fallback'] },
      }),
    })
  })

  test.each([
    'Attempts per tag (0 = all available)',
    'Total attempts per request',
  ])('empty %s displays validation and never saves as zero', async (name) => {
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    renderPolicy()
    const input = screen.getByRole('spinbutton', { name })
    fireEvent.change(input, { target: { value: '' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() => expect(input).toHaveAttribute('aria-invalid', 'true'))
    expect(put).not.toHaveBeenCalled()
  })

  test('failed save keeps edits and allows retry', async () => {
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValueOnce({ data: { success: false, message: 'Rejected' } })
      .mockResolvedValueOnce({ data: { success: true } })
    renderPolicy()
    fireEvent.change(
      screen.getByRole('spinbutton', {
        name: 'Attempts per tag (0 = all available)',
      }),
      { target: { value: '2' } }
    )
    fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    await screen.findByRole('alert')
    expect(
      screen.getByRole('spinbutton', {
        name: 'Attempts per tag (0 = all available)',
      })
    ).toHaveValue(2)
    fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() => expect(put).toHaveBeenCalledTimes(2))
  })

  test('invalid stored policy blocks overwrite and explains the load failure', () => {
    renderPolicy('{broken')
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Unable to load routing policy'
    )
    expect(screen.getByRole('button', { name: 'Save Changes' })).toBeDisabled()
  })
})
