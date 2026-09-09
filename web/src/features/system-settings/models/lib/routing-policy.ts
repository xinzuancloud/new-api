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

const routingPolicySchema = z.object({
  enabled: z.boolean().default(false),
  group_tag_order: z
    .record(z.string().trim().min(1), z.array(z.string().trim().min(1)).min(1))
    .default({}),
  tag_capacity: z
    .record(
      z.string().trim().min(1),
      z.object({
        window_seconds: z.number().int().min(10).max(3600),
        max_requests: z.number().int().min(0).max(1_000_000).default(0),
        max_input_tokens: z
          .number()
          .int()
          .min(0)
          .max(1_000_000_000_000)
          .default(0),
      })
    )
    .default({}),
  max_attempts_per_tag: z.number().int().min(0).max(256).default(3),
  max_total_attempts: z.number().int().min(0).max(1024).default(0),
  rate_limit_cooldown_seconds: z.number().int().min(0).max(3600).default(60),
  rate_limit_max_cooldown_seconds: z
    .number()
    .int()
    .min(0)
    .max(86400)
    .default(900),
  quota_cooldown_seconds: z.number().int().min(0).max(604800).default(3600),
  quota_error_keywords: z.array(z.string()).default([]),
  request_timeout_seconds: z.number().int().min(1).max(1800).default(300),
})

export function parseRoutingPolicy(value: string) {
  return routingPolicySchema.parse(value.trim() ? JSON.parse(value) : {})
}

export function createRoutingPolicyFormSchema(t: (key: string) => string) {
  const numberMessage = t('Enter a whole number within the allowed range')
  return z
    .object({
      enabled: z.boolean(),
      groups: z
        .array(
          z.object({
            group: z.string().trim().min(1, t('Group is required')),
            tags: z
              .string()
              .refine(
                (value) => value.trim().length > 0,
                t('Enter at least one channel tag')
              ),
          })
        )
        .superRefine((groups, ctx) => {
          const seen = new Set<string>()
          groups.forEach((group, index) => {
            if (seen.has(group.group)) {
              ctx.addIssue({
                code: 'custom',
                path: [index, 'group'],
                message: t('Each group can only have one routing rule'),
              })
            }
            seen.add(group.group)
            const tags = group.tags
              .split(/\r?\n/)
              .map((tag) => tag.trim())
              .filter(Boolean)
            if (new Set(tags).size !== tags.length) {
              ctx.addIssue({
                code: 'custom',
                path: [index, 'tags'],
                message: t('Channel tags must be unique within a group'),
              })
            }
          })
        }),
      capacities: z
        .array(
          z
            .object({
              tag: z.string().trim().min(1, t('Channel tag is required')),
              window_seconds: z.number().int().min(10).max(3600),
              max_requests: z.number().int().min(0).max(1_000_000),
              max_input_tokens: z.number().int().min(0).max(1_000_000_000_000),
            })
            .refine(
              (capacity) =>
                capacity.max_requests > 0 || capacity.max_input_tokens > 0,
              t('Set at least one capacity limit')
            )
        )
        .max(32),
      max_attempts_per_tag: z
        .number({ error: numberMessage })
        .int(numberMessage)
        .min(0, numberMessage)
        .max(256, numberMessage),
      max_total_attempts: z
        .number({ error: numberMessage })
        .int(numberMessage)
        .min(0, numberMessage)
        .max(1024, numberMessage),
      rate_limit_cooldown_seconds: z
        .number({ error: numberMessage })
        .int(numberMessage)
        .min(0, numberMessage)
        .max(3600, numberMessage),
      rate_limit_max_cooldown_seconds: z
        .number({ error: numberMessage })
        .int(numberMessage)
        .min(0, numberMessage)
        .max(86400, numberMessage),
      quota_cooldown_seconds: z
        .number({ error: numberMessage })
        .int(numberMessage)
        .min(0, numberMessage)
        .max(604800, numberMessage),
      quota_error_keywords: z.string(),
      request_timeout_seconds: z
        .number({ error: numberMessage })
        .int(numberMessage)
        .min(1, numberMessage)
        .max(1800, numberMessage),
    })
    .superRefine((policy, ctx) => {
      if (
        policy.rate_limit_max_cooldown_seconds <
        policy.rate_limit_cooldown_seconds
      ) {
        ctx.addIssue({
          code: 'custom',
          path: ['rate_limit_max_cooldown_seconds'],
          message: t('Maximum cooldown must be at least the base cooldown'),
        })
      }
      const tags = new Set<string>()
      policy.capacities.forEach((capacity, index) => {
        if (tags.has(capacity.tag)) {
          ctx.addIssue({
            code: 'custom',
            path: ['capacities', index, 'tag'],
            message: t('Each channel tag can only have one capacity rule'),
          })
        }
        tags.add(capacity.tag)
      })
    })
}

export type RoutingPolicyFormValues = z.infer<
  ReturnType<typeof createRoutingPolicyFormSchema>
>

export function routingPolicyFormDefaults(
  value: string
): RoutingPolicyFormValues {
  const policy = parseRoutingPolicy(value)
  return {
    enabled: policy.enabled,
    groups: Object.entries(policy.group_tag_order).map(([group, tags]) => ({
      group,
      tags: tags.join('\n'),
    })),
    capacities: Object.entries(policy.tag_capacity).map(([tag, capacity]) => ({
      tag,
      window_seconds: capacity.window_seconds,
      max_requests: capacity.max_requests,
      max_input_tokens: capacity.max_input_tokens,
    })),
    max_attempts_per_tag: policy.max_attempts_per_tag,
    max_total_attempts: policy.max_total_attempts,
    rate_limit_cooldown_seconds: policy.rate_limit_cooldown_seconds,
    rate_limit_max_cooldown_seconds: policy.rate_limit_max_cooldown_seconds,
    quota_cooldown_seconds: policy.quota_cooldown_seconds,
    quota_error_keywords: policy.quota_error_keywords.join('\n'),
    request_timeout_seconds: policy.request_timeout_seconds,
  }
}

export function serializeRoutingPolicy(
  values: RoutingPolicyFormValues
): string {
  return JSON.stringify({
    enabled: values.enabled,
    group_tag_order: Object.fromEntries(
      values.groups.map((row) => [
        row.group,
        row.tags
          .split(/\r?\n/)
          .map((tag) => tag.trim())
          .filter(Boolean),
      ])
    ),
    tag_capacity: Object.fromEntries(
      values.capacities.map((capacity) => [
        capacity.tag.trim(),
        {
          window_seconds: capacity.window_seconds,
          max_requests: capacity.max_requests,
          max_input_tokens: capacity.max_input_tokens,
        },
      ])
    ),
    max_attempts_per_tag: values.max_attempts_per_tag,
    max_total_attempts: values.max_total_attempts,
    rate_limit_cooldown_seconds: values.rate_limit_cooldown_seconds,
    rate_limit_max_cooldown_seconds: values.rate_limit_max_cooldown_seconds,
    quota_cooldown_seconds: values.quota_cooldown_seconds,
    quota_error_keywords: values.quota_error_keywords
      .split(/\r?\n/)
      .map((keyword) => keyword.trim())
      .filter(Boolean),
    request_timeout_seconds: values.request_timeout_seconds,
  })
}
