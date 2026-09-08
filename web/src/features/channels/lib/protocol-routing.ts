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
import { z } from 'zod'

export const PROTOCOL_FORMATS = [
  'openai',
  'claude',
  'openai_responses',
] as const
export const PROTOCOL_FEATURES = [
  'stream',
  'tools',
  'parallel_tools',
  'images',
  'files',
  'audio',
  'video',
  'structured_output',
  'reasoning',
  'hosted_tools',
  'context_editing',
  'stateful',
  'background',
] as const
const formatSchema = z.enum(PROTOCOL_FORMATS)
const unique = (values: readonly unknown[]) =>
  new Set(values).size === values.length
const byteLength = (value: string) => new TextEncoder().encode(value).length

export const protocolModelPolicySchema = z
  .object({
    entry_formats: z.array(formatSchema).min(1).max(3).refine(unique),
    endpoints: z
      .array(
        z
          .object({
            format: formatSchema,
            path: z
              .string()
              .refine(
                (value) =>
                  value.startsWith('/') &&
                  !value.includes('//') &&
                  (value === '/' || !value.endsWith('/')) &&
                  byteLength(value) <= 1024 &&
                  !/[\\?#%\s\p{Cc}]/u.test(value) &&
                  !value
                    .split('/')
                    .some((segment) => segment === '.' || segment === '..')
              ),
            features: z
              .array(z.enum(PROTOCOL_FEATURES))
              .max(PROTOCOL_FEATURES.length)
              .refine(unique)
              .optional(),
            verified: z.boolean(),
            verified_at: z.iso
              .datetime({ offset: true })
              .refine((value) => /T\d{2}:\d{2}:\d{2}/.test(value))
              .optional(),
          })
          .strict()
      )
      .min(1)
      .max(16)
      .refine((endpoints) =>
        unique(
          endpoints.map((endpoint) => `${endpoint.format}:${endpoint.path}`)
        )
      ),
    loss_policy: z.enum(['safe', 'allow', 'strict']).optional(),
  })
  .strict()

export const protocolModelOverridesSchema = z
  .record(
    z
      .string()
      .refine(
        (value) =>
          value.length > 0 &&
          value === value.trim() &&
          byteLength(value) <= 256 &&
          !/[\p{Cc}]/u.test(value)
      ),
    protocolModelPolicySchema
  )
  .refine((models) => Object.keys(models).length <= 256)

export type ProtocolModelPolicy = z.infer<typeof protocolModelPolicySchema>
export interface ProtocolRoutingSettings {
  enabled: boolean
  account_resource?: string
  quota_scope?: 'model' | 'account'
  defaults: ProtocolModelPolicy
  models?: Record<string, ProtocolModelPolicy>
}

// An editable example, never an automatically verified upstream capability.
export const PROTOCOL_DEFAULT_POLICY_JSON = JSON.stringify(
  {
    entry_formats: PROTOCOL_FORMATS,
    endpoints: [
      {
        format: 'openai',
        path: '/v1/chat/completions',
        features: [],
        verified: false,
      },
    ],
    loss_policy: 'safe',
  },
  null,
  2
)

const emptyProtocolPolicySchema = z
  .object({
    entry_formats: z.array(formatSchema).length(0).nullish(),
    endpoints: z.array(z.unknown()).length(0).nullish(),
    loss_policy: z.literal('').optional(),
  })
  .strict()

export function validateProtocolPolicyJSON(
  value: string | undefined,
  models = false,
  allowEmpty = false
): boolean {
  try {
    const parsed: unknown = JSON.parse(value || '')
    if (
      allowEmpty &&
      !models &&
      emptyProtocolPolicySchema.safeParse(parsed).success
    ) {
      return true
    }
    return (
      models ? protocolModelOverridesSchema : protocolModelPolicySchema
    ).safeParse(parsed).success
  } catch {
    return false
  }
}
