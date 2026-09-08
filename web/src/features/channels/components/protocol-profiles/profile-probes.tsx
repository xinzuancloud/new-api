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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { JsonCodeEditor } from '@/components/json-code-editor'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { handleServerError } from '@/lib/handle-server-error'
import { getServerErrorMessageKey } from '@/lib/server-error-message'

import { useProtocolProbes } from '../../hooks/use-protocol-probes'
import {
  protocolProfilesAPI,
  type ProfilesResponse,
  type ApplyPreview,
} from '../../lib/protocol-profiles-api'
import { ProbeResults } from './probe-results'
import { ProbeSetup } from './probe-setup'

export function ProfileProbes(props: {
  data: ProfilesResponse
  canOperate: boolean
  canWrite: boolean
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const probes = useProtocolProbes()
  const [preview, setPreview] = useState<ApplyPreview | null>(null)
  const reports = useQuery({
    queryKey: ['protocol-profiles', 'reports'],
    queryFn: ({ signal }) => protocolProfilesAPI.reports(signal),
    refetchOnWindowFocus: false,
  })
  const select = useMutation({
    mutationFn: (id: string) => protocolProfilesAPI.report(id),
    onSuccess: (report) => {
      setPreview(null)
      probes.setReport(report)
    },
    onError: handleServerError,
  })
  const apply = useMutation({
    mutationFn: (confirm: boolean) => {
      if (!probes.report) throw new Error('invalid_protocol_request')
      return protocolProfilesAPI.apply(probes.report.id, {
        revision: preview?.revision || props.data.revision,
        preview: !confirm,
        ...(confirm && preview ? { fingerprint: preview.fingerprint } : {}),
      })
    },
    onSuccess: (data, confirm) => {
      if (!confirm) {
        setPreview(data)
        return
      }
      setPreview(null)
      void queryClient.invalidateQueries({ queryKey: ['protocol-profiles'] })
      if (probes.report) select.mutate(probes.report.id)
    },
    onError: (error) => {
      setPreview(null)
      handleServerError(error)
    },
  })
  const error = probes.error || select.error || apply.error || reports.error
  const report = probes.report
  const busy =
    probes.running ||
    probes.create.isPending ||
    probes.cancel.isPending ||
    select.isPending ||
    apply.isPending
  const canApply =
    report &&
    ['completed', 'cancelled'].includes(report.status) &&
    report.cases.some((item) => item.result.outcome === 'passed')
  const items = (reports.data || []).map((report) => ({
    value: report.id,
    label: `${report.profile} · ${new Date(report.created_at * 1000).toLocaleString()} · ${report.id}`,
  }))
  return (
    <div className='space-y-6'>
      <ProbeSetup
        data={props.data}
        disabled={!props.canOperate || busy}
        onCreate={(body) => probes.create.mutate(body)}
      />
      <div className='space-y-2'>
        <h3 className='font-medium'>{t('Recent verification reports')}</h3>
        {reports.isPending && <LoadingState inline />}
        {reports.data?.length === 0 && <EmptyState />}
        <Select
          value={report?.id || ''}
          onValueChange={(value) => {
            if (value) select.mutate(value)
          }}
          items={items}
          disabled={busy || !items.length}
        >
          <SelectTrigger aria-label={t('Recent verification reports')}>
            <SelectValue placeholder={t('Select report')} />
          </SelectTrigger>
          <SelectContent>
            {items.map((item) => (
              <SelectItem key={item.value} value={item.value}>
                {item.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button
          variant='outline'
          disabled={busy}
          onClick={() => {
            void reports.refetch()
            if (report) select.mutate(report.id)
          }}
        >
          {t('Refresh')}
        </Button>
      </div>
      {error && (
        <ErrorState
          description={t(
            getServerErrorMessageKey(error) || 'Something went wrong!'
          )}
        />
      )}
      {report && (
        <>
          <div className='flex flex-wrap gap-2'>
            <Button
              disabled={
                !props.canOperate ||
                busy ||
                !['pending', 'running'].includes(report.status)
              }
              onClick={() => probes.run.mutate(report)}
            >
              {t('Run / resume verification')}
            </Button>
            <Button
              variant='outline'
              disabled={
                !props.canOperate ||
                probes.cancel.isPending ||
                !['pending', 'running'].includes(report.status)
              }
              onClick={() => probes.cancel.mutate()}
            >
              {t('Cancel verification')}
            </Button>
            <Button
              variant='outline'
              disabled={!props.canWrite || busy || !canApply}
              onClick={() => apply.mutate(false)}
            >
              {t('Preview passed suggestions')}
            </Button>
          </div>
          {probes.running && (
            <LoadingState inline message={t('Verification in progress')} />
          )}
          {report.status === 'running' && !probes.running && (
            <p className='text-muted-foreground text-sm'>
              {t(
                'A batch is already running. Wait 60 seconds before manually resuming an interrupted run.'
              )}
            </p>
          )}
          <p className='text-muted-foreground text-xs'>
            {t(
              'Closing this manager stops requests. Reopen a saved report to resume. Only passed suggestions can update the profile.'
            )}
          </p>
          <ProbeResults report={report} />
        </>
      )}
      <ConfirmDialog
        open={Boolean(preview)}
        onOpenChange={(open) => {
          if (!open) setPreview(null)
        }}
        title={t('Apply passed suggestions?')}
        desc={t(
          'Only verified model differences will be added. Failed and unknown checks never remove capabilities.'
        )}
        confirmText={t('Apply')}
        isLoading={apply.isPending}
        disabled={!props.canWrite}
        handleConfirm={() => apply.mutate(true)}
        className='sm:max-w-4xl'
      >
        <div className='grid max-h-[55vh] gap-3 overflow-y-auto md:grid-cols-2'>
          <div>
            <p>{t('Before')}</p>
            <JsonCodeEditor
              value={JSON.stringify(preview?.before || {}, null, 2)}
              disabled
              onChange={() => {}}
              ariaLabel={t('Before')}
            />
          </div>
          <div>
            <p>{t('After')}</p>
            <JsonCodeEditor
              value={JSON.stringify(preview?.after || {}, null, 2)}
              disabled
              onChange={() => {}}
              ariaLabel={t('After')}
            />
          </div>
        </div>
      </ConfirmDialog>
    </div>
  )
}
