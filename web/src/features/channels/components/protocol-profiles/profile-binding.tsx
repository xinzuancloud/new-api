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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable } from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { JsonCodeEditor } from '@/components/json-code-editor'
import { Button } from '@/components/ui/button'
import { handleServerError } from '@/lib/handle-server-error'
import { getServerErrorMessageKey } from '@/lib/server-error-message'

import {
  protocolProfilesAPI,
  type ProfilesResponse,
  type BindingPreview,
} from '../../lib/protocol-profiles-api'
import { ProfileSelect, ProtocolCheckbox } from './profile-controls'

export function ProfileBinding(props: {
  data: ProfilesResponse
  canWrite: boolean
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [profile, setProfile] = useState('')
  const [ids, setIds] = useState<number[]>([])
  const [preview, setPreview] = useState<BindingPreview | null>(null)
  const [error, setError] = useState<unknown>(null)
  const binding = useMutation({
    mutationFn: (apply: boolean) =>
      protocolProfilesAPI.bind({
        revision: preview?.revision || props.data.revision,
        profile,
        channel_ids: ids,
        preview: !apply,
        ...(apply && preview ? { fingerprint: preview.fingerprint } : {}),
      }),
    onSuccess: (data, apply) => {
      if (!apply) {
        setPreview(data)
        return
      }
      setPreview(null)
      void queryClient.invalidateQueries({ queryKey: ['protocol-profiles'] })
      void queryClient.invalidateQueries({ queryKey: ['channels'] })
    },
    onError: (error) => {
      setPreview(null)
      setError(error)
      handleServerError(error)
    },
  })
  return (
    <div className='space-y-4'>
      <ProfileSelect
        catalog={props.data.profiles}
        value={profile}
        onChange={(value) => {
          setProfile(value)
          setPreview(null)
        }}
        disabled={!props.canWrite || binding.isPending}
      />
      <p className='text-muted-foreground text-sm'>
        {t(
          'Binding clears local policy copies and inherits the selected profile. Detaching preserves the effective policy as local configuration.'
        )}
      </p>
      <Button
        variant='outline'
        onClick={() =>
          setIds(
            props.data.channels
              .filter((channel) => !channel.diagnostic)
              .map((channel) => channel.id)
          )
        }
        disabled={!props.canWrite || binding.isPending}
      >
        {t('Select all')}
      </Button>
      <Button
        variant='ghost'
        onClick={() => setIds([])}
        disabled={binding.isPending}
      >
        {t('Clear')}
      </Button>
      <StaticDataTable
        data={props.data.channels}
        getRowKey={(row) => row.id}
        emptyContent={<EmptyState />}
        columns={[
          {
            id: 'select',
            header: t('Select'),
            cell: (channel) => (
              <ProtocolCheckbox
                label={`${channel.id} · ${channel.name}`}
                checked={ids.includes(channel.id)}
                disabled={
                  !props.canWrite ||
                  binding.isPending ||
                  Boolean(channel.diagnostic)
                }
                onChange={(checked) =>
                  setIds((current) =>
                    checked
                      ? [...current, channel.id]
                      : current.filter((id) => id !== channel.id)
                  )
                }
              />
            ),
          },
          {
            id: 'profile',
            header: t('Shared protocol profile'),
            cell: (channel) => channel.profile || t('Local configuration'),
          },
          {
            id: 'models',
            header: t('Models'),
            cell: (channel) => channel.mapped_models.join(', '),
          },
          {
            id: 'diagnostic',
            header: t('Status'),
            cell: (channel) =>
              channel.diagnostic
                ? t(
                    'Invalid channel configuration; repair before binding or verification.'
                  )
                : '—',
          },
        ]}
      />
      {error !== null && (
        <ErrorState
          description={t(
            getServerErrorMessageKey(error) || 'Something went wrong!'
          )}
        />
      )}
      <Button
        disabled={!props.canWrite || !ids.length || binding.isPending}
        onClick={() => {
          setError(null)
          binding.mutate(false)
        }}
      >
        {t('Preview binding')}
      </Button>
      <ConfirmDialog
        open={Boolean(preview)}
        onOpenChange={(open) => {
          if (!open) setPreview(null)
        }}
        title={t('Apply profile binding?')}
        desc={t('Review every channel change before applying.')}
        confirmText={t('Apply')}
        isLoading={binding.isPending}
        disabled={!props.canWrite}
        handleConfirm={() => binding.mutate(true)}
        className='sm:max-w-4xl'
      >
        <div className='max-h-[55vh] space-y-4 overflow-y-auto'>
          {preview?.changes.map((change) => (
            <section key={change.id} className='space-y-2'>
              <h3>
                {change.id} · {change.name}
              </h3>
              <div className='grid gap-3 md:grid-cols-2'>
                <div>
                  <p>{t('Before')}</p>
                  <JsonCodeEditor
                    value={JSON.stringify(change.before, null, 2)}
                    onChange={() => {}}
                    disabled
                    ariaLabel={`${t('Before')} ${change.id}`}
                  />
                </div>
                <div>
                  <p>{t('After')}</p>
                  <JsonCodeEditor
                    value={JSON.stringify(change.after, null, 2)}
                    onChange={() => {}}
                    disabled
                    ariaLabel={`${t('After')} ${change.id}`}
                  />
                </div>
              </div>
            </section>
          ))}
        </div>
      </ConfirmDialog>
    </div>
  )
}
