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
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useWatch, type UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { ErrorState } from '@/components/error-state'
import { JsonCodeEditor } from '@/components/json-code-editor'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { getServerErrorMessageKey } from '@/lib/server-error-message'

import type { ChannelFormValues } from '../../../lib/channel-form'
import { protocolProfilesAPI } from '../../../lib/protocol-profiles-api'
import { ProfileSelect } from '../../protocol-profiles/profile-controls'

export function ChannelProfileField(props: {
  form: UseFormReturn<ChannelFormValues>
  channelId?: number
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const profile =
    useWatch({
      control: props.form.control,
      name: 'protocol_routing_profile',
    }) || ''
  const [next, setNext] = useState<string | null>(null)
  const [error, setError] = useState('')
  const catalog = useQuery({
    queryKey: ['protocol-profiles', 'catalog'],
    queryFn: ({ signal }) => protocolProfilesAPI.catalog(signal),
    refetchOnWindowFocus: false,
  })
  const effective = useQuery({
    queryKey: ['protocol-profiles', 'effective', props.channelId],
    queryFn: ({ signal }) =>
      protocolProfilesAPI.effective(props.channelId ?? 0, signal),
    enabled: Boolean(props.channelId),
    refetchOnWindowFocus: false,
  })
  const change = (value: string) => {
    setError('')
    if (value === profile) return
    if (
      !value &&
      profile &&
      (!effective.data || effective.data.profile !== profile)
    ) {
      setError(t('Save the channel before detaching a newly selected profile.'))
      return
    }
    setNext(value)
  }
  return (
    <div className='space-y-3'>
      {catalog.isPending && <LoadingState inline />}
      {catalog.error && (
        <ErrorState
          description={t(
            getServerErrorMessageKey(catalog.error) || 'Something went wrong!'
          )}
          onRetry={() => {
            void catalog.refetch()
          }}
        />
      )}
      <ProfileSelect
        catalog={catalog.data?.profiles || {}}
        value={profile}
        onChange={change}
        disabled={props.disabled || !catalog.data}
      />
      <p className='text-muted-foreground text-xs'>
        {profile
          ? t(
              'Inherited configuration comes from the shared profile. Local JSON contains optional differences; use {} to inherit everything.'
            )
          : t('Local configuration is stored on this channel.')}
      </p>
      {error && (
        <p role='alert' className='text-destructive text-sm'>
          {error}
        </p>
      )}
      <ConfirmDialog
        open={next !== null}
        onOpenChange={(open) => {
          if (!open) setNext(null)
        }}
        title={next ? t('Use shared profile?') : t('Detach shared profile?')}
        desc={
          next
            ? t(
                'This clears local default and model policies so old copies cannot override the selected profile. Account and quota settings are preserved.'
              )
            : t(
                'The saved effective policy will be copied into local configuration. Save the channel to finish detaching.'
              )
        }
        confirmText={t('Continue')}
        handleConfirm={() => {
          if (next === null) return
          if (next) {
            props.form.setValue('protocol_routing_defaults', '{}', {
              shouldDirty: true,
            })
            props.form.setValue('protocol_routing_models', '{}', {
              shouldDirty: true,
            })
          } else if (effective.data) {
            props.form.setValue(
              'protocol_routing_defaults',
              JSON.stringify(effective.data.settings.defaults, null, 2),
              { shouldDirty: true }
            )
            props.form.setValue(
              'protocol_routing_models',
              JSON.stringify(effective.data.settings.models || {}, null, 2),
              { shouldDirty: true }
            )
          }
          props.form.setValue('protocol_routing_profile', next, {
            shouldDirty: true,
            shouldValidate: true,
          })
          setNext(null)
        }}
      />
      {props.channelId && (
        <details>
          <summary className='cursor-pointer text-sm'>
            {t('Saved effective policy')}
          </summary>
          <p className='text-muted-foreground text-xs'>
            {t(
              'This preview reflects the saved channel. Save edits and refresh to see the new effective policy.'
            )}
          </p>
          <Button
            type='button'
            variant='outline'
            onClick={() => {
              void effective.refetch()
            }}
          >
            {t('Refresh')}
          </Button>
          {effective.isPending && <LoadingState inline />}
          {effective.error && (
            <ErrorState
              description={t(
                getServerErrorMessageKey(effective.error) ||
                  'Something went wrong!'
              )}
              onRetry={() => {
                void effective.refetch()
              }}
            />
          )}
          {effective.data && (
            <JsonCodeEditor
              value={JSON.stringify(effective.data.settings, null, 2)}
              disabled
              onChange={() => {}}
              ariaLabel={t('Saved effective policy')}
            />
          )}
        </details>
      )}
    </div>
  )
}
