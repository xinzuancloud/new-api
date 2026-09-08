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
import { describe, expect, test } from 'vitest'

import { channelSchema } from '../../types'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  buildSettingJSON,
  channelFormSchema,
  transformChannelToFormDefaults,
} from '../channel-form'
import { protocolModelPolicySchema } from '../protocol-routing'

const policy = {
  entry_formats: ['openai', 'claude', 'openai_responses'],
  endpoints: [
    {
      format: 'openai',
      path: '/v1/chat/completions',
      features: ['stream'],
      verified: false,
    },
  ],
  loss_policy: 'safe',
}
const routing = {
  enabled: true,
  account_resource: 'supplier-a',
  quota_scope: 'account',
  defaults: policy,
  models: { 'mapped-model': policy },
}
const form = {
  ...CHANNEL_FORM_DEFAULT_VALUES,
  name: 'Channel',
  models: 'model',
}

describe('protocol routing channel configuration', () => {
  test('saving an existing channel preserves routing and unrelated settings', () => {
    const channel = channelSchema.parse({
      id: 1,
      type: 1,
      key: '',
      name: 'Channel',
      models: 'model',
      status: 1,
      created_time: 0,
      test_time: 0,
      response_time: 0,
      balance_updated_time: 0,
      setting: JSON.stringify({
        protocol_routing: routing,
        custom_flag: 'keep',
      }),
    })
    const loaded = transformChannelToFormDefaults(channel)
    const saved = JSON.parse(buildSettingJSON({ ...loaded, name: 'Renamed' }))
    expect(saved.protocol_routing).toEqual(routing)
    expect(saved.custom_flag).toBe('keep')
  })

  test('new legacy channels omit protocol routing', () => {
    expect(JSON.parse(buildSettingJSON(form))).not.toHaveProperty(
      'protocol_routing'
    )
  })

  test.each([
    'https://other.test/path',
    '//other.test/path',
    '/path?key=secret',
    '/path#fragment',
  ])('rejects unsafe endpoint %s before saving', (path) => {
    expect(
      channelFormSchema.safeParse({
        ...form,
        protocol_routing_enabled: true,
        protocol_routing_defaults: JSON.stringify({
          ...policy,
          endpoints: [{ ...policy.endpoints[0], path }],
        }),
        protocol_routing_models: '{}',
      }).success
    ).toBe(false)
  })

  test('accepts partial model differences over inline defaults', () => {
    expect(
      channelFormSchema.safeParse({
        ...form,
        protocol_routing_enabled: true,
        protocol_routing_defaults: JSON.stringify(policy),
        protocol_routing_models: JSON.stringify({
          'mapped-model': { loss_policy: 'strict' },
        }),
      }).success
    ).toBe(true)
  })
})

describe('protocol routing validation boundaries', () => {
  test('disabled empty configuration can be loaded and saved', () => {
    const emptyRouting = {
      enabled: false,
      defaults: { entry_formats: null, endpoints: null },
    }
    const values = {
      ...form,
      setting: JSON.stringify({ protocol_routing: emptyRouting }),
      protocol_routing_enabled: false,
      protocol_routing_defaults: JSON.stringify(emptyRouting.defaults),
    }
    expect(channelFormSchema.safeParse(values).success).toBe(true)
    expect(JSON.parse(buildSettingJSON(values)).protocol_routing.enabled).toBe(
      false
    )
  })

  test('disabling and clearing policies cannot accidentally preserve enabled routing', () => {
    const values = {
      ...form,
      setting: JSON.stringify({ protocol_routing: routing }),
      protocol_routing_enabled: false,
      protocol_routing_defaults: '',
    }
    expect(JSON.parse(buildSettingJSON(values)).protocol_routing.enabled).toBe(
      false
    )
  })

  test.each([
    { ...policy, entry_formats: ['openai', 'openai'] },
    {
      ...policy,
      endpoints: [{ ...policy.endpoints[0], features: ['invented'] }],
    },
    {
      ...policy,
      endpoints: [{ ...policy.endpoints[0], features: ['tools', 'tools'] }],
    },
    { ...policy, endpoints: [{ ...policy.endpoints[0], path: '/v1/../chat' }] },
    { ...policy, endpoints: [{ ...policy.endpoints[0], path: '/v1//chat' }] },
    { ...policy, endpoints: [{ ...policy.endpoints[0], path: '/v1/chat/' }] },
    {
      ...policy,
      endpoints: [{ ...policy.endpoints[0], path: '/v1/%2e/chat' }],
    },
    {
      ...policy,
      endpoints: [{ ...policy.endpoints[0], verified_at: 'yesterday' }],
    },
    { ...policy, endpoints: [policy.endpoints[0], policy.endpoints[0]] },
    { ...policy, loss_policy: 'anything' },
  ])('rejects invalid policy %#', (invalid) => {
    expect(protocolModelPolicySchema.safeParse(invalid).success).toBe(false)
  })

  test('requires seconds in RFC3339 verification timestamps', () => {
    expect(
      protocolModelPolicySchema.safeParse({
        ...policy,
        endpoints: [
          { ...policy.endpoints[0], verified_at: '2026-09-08T12:00Z' },
        ],
      }).success
    ).toBe(false)
  })

  test('bounds account identifiers by UTF-8 byte length', () => {
    expect(
      channelFormSchema.safeParse({
        ...form,
        protocol_routing_account_resource: '界'.repeat(43),
      }).success
    ).toBe(false)
    expect(
      channelFormSchema.safeParse({
        ...form,
        protocol_routing_account_resource: '界'.repeat(42),
      }).success
    ).toBe(true)
  })

  test('preserves complete model overrides, verification timestamps and explicit false flags', () => {
    const modelPolicy = {
      ...policy,
      loss_policy: 'strict',
      endpoints: [
        { ...policy.endpoints[0], verified_at: '2026-09-08T12:00:00+08:00' },
      ],
    }
    const values = {
      ...form,
      protocol_routing_enabled: true,
      protocol_routing_defaults: JSON.stringify(policy),
      protocol_routing_models: JSON.stringify({ 'mapped-model': modelPolicy }),
    }
    expect(channelFormSchema.safeParse(values).success).toBe(true)
    expect(
      JSON.parse(buildSettingJSON(values)).protocol_routing.models[
        'mapped-model'
      ]
    ).toEqual(modelPolicy)
  })
})

test('accepts and preserves explicitly advertised native Claude context editing', () => {
  const nativePolicy = {
    entry_formats: ['claude'],
    endpoints: [
      {
        format: 'claude',
        path: '/v1/messages',
        features: ['context_editing'],
        verified: true,
      },
    ],
    loss_policy: 'safe',
  }
  const values = {
    ...form,
    protocol_routing_enabled: true,
    protocol_routing_defaults: JSON.stringify(nativePolicy),
    protocol_routing_models: '{}',
  }
  expect(channelFormSchema.safeParse(values).success).toBe(true)
  expect(
    JSON.parse(buildSettingJSON(values)).protocol_routing.defaults
  ).toEqual(nativePolicy)
})

test('profile inheritance preserves empty local policies and explicit feature removal', () => {
  const values = {
    ...form,
    protocol_routing_enabled: true,
    protocol_routing_profile: 'shared',
    protocol_routing_defaults: '{}',
    protocol_routing_models: JSON.stringify({
      model: {
        endpoint_overrides: [
          {
            format: 'openai',
            features: { tools: false, stream: true },
            verified: false,
          },
        ],
      },
    }),
  }
  expect(channelFormSchema.safeParse(values).success).toBe(true)
  const saved = JSON.parse(buildSettingJSON(values)).protocol_routing
  expect(saved.profile).toBe('shared')
  expect(saved.defaults).toEqual({})
  expect(saved.models.model.endpoint_overrides[0]).toEqual({
    format: 'openai',
    features: { tools: false, stream: true },
    verified: false,
  })
  expect(
    channelFormSchema.safeParse({
      ...values,
      protocol_routing_profile: 'bad profile',
    }).success
  ).toBe(false)
})

test('inline defaults require a complete base and can include endpoint differences', () => {
  const values = {
    ...form,
    protocol_routing_enabled: true,
    protocol_routing_defaults: JSON.stringify({
      ...policy,
      endpoint_overrides: [{ format: 'openai', features: { tools: false } }],
    }),
  }
  expect(channelFormSchema.safeParse(values).success).toBe(true)
  expect(
    channelFormSchema.safeParse({ ...values, protocol_routing_defaults: '{}' })
      .success
  ).toBe(false)
})
