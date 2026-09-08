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
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { StaticDataTable } from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'

import type { ProbeReport } from '../../lib/protocol-profiles-api'

export function ProbeResults(props: { report: ProbeReport }) {
  const { t } = useTranslation()
  const outcomes: Record<string, string> = {
    pending: t('Pending'),
    running: t('Running'),
    passed: t('Passed'),
    rejected: t('Rejected'),
    unknown: t('Unknown'),
    cancelled: t('Cancelled'),
    completed: t('Completed'),
    applied: t('Applied'),
  }
  const reasons: Record<string, string> = {
    not_run: t('Not run'),
    in_progress: t('In progress'),
    verified_fixture: t('Verification fixture passed'),
    fixture_rejected: t('Verification fixture rejected'),
    upstream_status: t('Upstream HTTP status'),
    insufficient_evidence: t('Insufficient evidence'),
    incomplete_response: t('Incomplete response'),
    invalid_response: t('Invalid response'),
    response_too_large: t('Response too large'),
    configuration_unavailable: t('Configuration unavailable'),
    no_eligible_key: t('No eligible key'),
    transport_error: t('Transport error'),
    cancelled: t('Cancelled'),
    timeout: t('Timeout'),
    response_interrupted: t('Response interrupted'),
    interrupted_batch: t('Interrupted batch'),
  }
  const checks: Record<string, string> = {
    text: t('Text'),
    stream: t('Streaming'),
    tools: t('Tools'),
    namespaces: t('Tool namespaces'),
    images: t('Images'),
    web_search: t('Web search'),
  }
  return (
    <div className='space-y-3'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <p>
          {props.report.id} · {outcomes[props.report.status]} ·{' '}
          {
            props.report.cases.filter(
              (item) => item.result.outcome === 'passed'
            ).length
          }
          /{props.report.cases.length} {t('Passed')}
        </p>
        <CopyButton
          value={JSON.stringify(props.report, null, 2)}
          size='sm'
          variant='outline'
          aria-label={t('Export report JSON')}
        >
          {t('Export report JSON')}
        </CopyButton>
      </div>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Rejected means the fixture was rejected. Rate limits, server errors, timeouts, and incomplete responses are unknown. HTTP 200 alone does not verify a capability.'
        )}
      </p>
      <StaticDataTable
        data={props.report.cases}
        getRowKey={(row) => row.id}
        emptyContent={<EmptyState />}
        columns={[
          {
            id: 'channel',
            header: t('Channel'),
            cell: (row) => row.channel_id,
          },
          { id: 'model', header: t('Model'), cell: (row) => row.model },
          {
            id: 'endpoint',
            header: t('Endpoint'),
            cell: (row) => `${row.endpoint.format} ${row.endpoint.path}`,
          },
          { id: 'check', header: t('Check'), cell: (row) => checks[row.check] },
          {
            id: 'outcome',
            header: t('Result'),
            cell: (row) => outcomes[row.result.outcome],
          },
          {
            id: 'reason',
            header: t('Reason'),
            cell: (row) => reasons[row.result.reason] || t('Unknown'),
          },
          {
            id: 'http',
            header: t('HTTP Status'),
            cell: (row) => row.result.http_status ?? '—',
          },
          {
            id: 'terminal',
            header: t('Terminal response'),
            cell: (row) => (row.result.terminal ? t('Yes') : t('No')),
          },
          {
            id: 'usage',
            header: t('Input / Output tokens'),
            cell: (row) =>
              `${row.result.input_tokens ?? '—'} / ${row.result.output_tokens ?? '—'}`,
          },
        ]}
      />
    </div>
  )
}
