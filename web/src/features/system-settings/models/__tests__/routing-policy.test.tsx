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
    { max_attempts_per_tag: 0 },
    { max_attempts_per_tag: 11 },
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

  test('empty numeric input displays validation and never saves as zero', async () => {
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    renderPolicy()
    const input = screen.getByRole('spinbutton', { name: 'Attempts per tag' })
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
      screen.getByRole('spinbutton', { name: 'Attempts per tag' }),
      { target: { value: '2' } }
    )
    fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    await screen.findByRole('alert')
    expect(
      screen.getByRole('spinbutton', { name: 'Attempts per tag' })
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
