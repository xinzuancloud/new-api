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

import { api } from '@/lib/api'

import {
  protocolModelPolicySchema,
  protocolModelOverridesSchema,
  protocolProfileIdSchema,
  type ProtocolRoutingSettings,
  type PROTOCOL_FORMATS,
} from './protocol-routing'

export const protocolCatalogSchema = z.record(
  protocolProfileIdSchema,
  z
    .object({
      name: z.string().trim().min(1),
      description: z.string().optional(),
      channel_types: z.array(z.number().int().positive()).optional(),
      base_urls: z.array(z.string()).optional(),
      defaults: protocolModelPolicySchema,
      models: protocolModelOverridesSchema.optional(),
    })
    .strict()
)
export type ProtocolCatalog = z.infer<typeof protocolCatalogSchema>
export type ProtocolFormat = (typeof PROTOCOL_FORMATS)[number]
export interface ProfileChannel {
  id: number
  name: string
  type: number
  base_url: string
  models: string[]
  mapped_models: string[]
  profile: string
  enabled: boolean
  account_resource: string
  diagnostic?: 'invalid_channel_settings' | 'invalid_model_mapping'
}
export interface ProfilesResponse {
  revision: string
  profiles: ProtocolCatalog
  channels: ProfileChannel[]
}
export interface BindingPreview {
  revision: string
  fingerprint: string
  changes: {
    id: number
    name: string
    before: ProtocolRoutingSettings
    after: ProtocolRoutingSettings
  }[]
}
export interface ApplyPreview {
  revision: string
  fingerprint: string
  before: ProtocolCatalog[string]
  after: ProtocolCatalog[string]
}
export const PROTOCOL_CHECKS = [
  'text',
  'stream',
  'tools',
  'namespaces',
  'images',
  'web_search',
] as const
export type ProtocolCheck = (typeof PROTOCOL_CHECKS)[number]
export interface ProbeRequest {
  profile: string
  channel_ids?: number[]
  models?: string[]
  formats?: ProtocolFormat[]
  checks: ProtocolCheck[]
  max_output_tokens: number
  allow_hosted: boolean
}
export interface ProbeCase {
  id: number
  channel_id: number
  model: string
  endpoint: { format: ProtocolFormat; path: string; verified: boolean }
  check: ProtocolCheck
  result: {
    outcome:
      | 'pending'
      | 'running'
      | 'passed'
      | 'rejected'
      | 'unknown'
      | 'cancelled'
    reason: string
    http_status?: number
    terminal: boolean
    elapsed_ms?: number
    input_tokens?: number
    output_tokens?: number
    features?: string[]
  }
}
export interface ProbeReport {
  id: string
  profile: string
  profile_revision: string
  fingerprints: Record<number, string>
  created_at: number
  updated_at: number
  max_output_tokens: number
  allow_hosted: boolean
  status: 'pending' | 'running' | 'completed' | 'cancelled' | 'applied'
  cases: ProbeCase[]
  applied_revision?: string
}
interface Envelope<T> {
  success: boolean
  data: T
  message?: string
}
async function request<T>(
  method: 'get' | 'put' | 'post',
  path: string,
  data?: unknown,
  signal?: AbortSignal
): Promise<T> {
  const response = await api.request<Envelope<T>>({
    method,
    url: `/api/channel/protocol${path}`,
    data,
    signal,
    timeout: 65000,
  })
  if (!response.data.success) {
    throw new Error(response.data.message || 'invalid_protocol_request')
  }
  return response.data.data
}
export const protocolProfilesAPI = {
  catalog: (signal?: AbortSignal) =>
    request<ProfilesResponse>('get', '/profiles', undefined, signal),
  save: (revision: string, profiles: ProtocolCatalog) =>
    request<{ revision: string }>('put', '/profiles', { revision, profiles }),
  bind: (body: {
    revision: string
    profile: string
    channel_ids: number[]
    preview: boolean
    fingerprint?: string
  }) => request<BindingPreview>('post', '/bind', body),
  effective: (id: number, signal?: AbortSignal) =>
    request<{
      revision: string
      profile: string
      settings: ProtocolRoutingSettings
    }>('get', `/effective/${id}`, undefined, signal),
  reports: (signal?: AbortSignal) =>
    request<ProbeReport[]>('get', '/probes', undefined, signal),
  report: (id: string, signal?: AbortSignal) =>
    request<ProbeReport>(
      'get',
      `/probes/${encodeURIComponent(id)}`,
      undefined,
      signal
    ),
  create: (body: ProbeRequest) => request<ProbeReport>('post', '/probes', body),
  run: (id: string, signal: AbortSignal) =>
    request<ProbeReport>(
      'post',
      `/probes/${encodeURIComponent(id)}/run`,
      undefined,
      signal
    ),
  cancel: (id: string) =>
    request<ProbeReport>('post', `/probes/${encodeURIComponent(id)}/cancel`),
  apply: (
    id: string,
    body: { revision: string; preview: boolean; fingerprint?: string }
  ) =>
    request<ApplyPreview>(
      'post',
      `/probes/${encodeURIComponent(id)}/apply`,
      body
    ),
}
