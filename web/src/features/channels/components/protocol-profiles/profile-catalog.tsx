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

import { ErrorState } from '@/components/error-state'
import { JsonCodeEditor } from '@/components/json-code-editor'
import { Button } from '@/components/ui/button'
import { handleServerError } from '@/lib/handle-server-error'
import { getServerErrorMessageKey } from '@/lib/server-error-message'

import {
  protocolCatalogSchema,
  protocolProfilesAPI,
  type ProfilesResponse,
} from '../../lib/protocol-profiles-api'
import { PROTOCOL_DEFAULT_POLICY_JSON } from '../../lib/protocol-routing'

export function ProfileCatalog(props: {
  data: ProfilesResponse
  canWrite: boolean
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [draft, setDraft] = useState(
    JSON.stringify(props.data.profiles, null, 2)
  )
  const save = useMutation({
    mutationFn: () =>
      protocolProfilesAPI.save(
        props.data.revision,
        protocolCatalogSchema.parse(JSON.parse(draft))
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['protocol-profiles'] })
    },
    onError: handleServerError,
  })
  let valid = false
  try {
    valid = protocolCatalogSchema.safeParse(JSON.parse(draft)).success
  } catch {
    /* Editor shows JSON syntax errors. */
  }
  return (
    <div className='space-y-4'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Edit reusable profiles by ID. Use defaults for shared policy and models for mapped model differences. Selectors constrain compatible channels; never include secrets.'
        )}
      </p>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Partial policies inherit unchanged fields. endpoint_overrides features use true to add and false to remove; full entry_formats and endpoints replace the policy.'
        )}
      </p>
      <Button
        variant='outline'
        disabled={!props.canWrite || save.isPending}
        onClick={() => {
          const parsed = valid ? JSON.parse(draft) : {}
          setDraft(
            JSON.stringify(
              {
                ...parsed,
                example: {
                  name: 'Example',
                  defaults: JSON.parse(PROTOCOL_DEFAULT_POLICY_JSON),
                },
              },
              null,
              2
            )
          )
        }}
      >
        {t('Add example profile')}
      </Button>
      <JsonCodeEditor
        value={draft}
        onChange={setDraft}
        disabled={!props.canWrite || save.isPending}
        ariaLabel={t('Profile catalog')}
        heightClassName='h-80 min-h-80 max-h-80'
      />
      {!valid && (
        <p role='alert' className='text-destructive text-sm'>
          {t('Invalid profile catalog')}
        </p>
      )}
      {Boolean(save.error) && (
        <ErrorState
          description={t(
            getServerErrorMessageKey(save.error) || 'Something went wrong!'
          )}
        />
      )}
      <Button
        disabled={!props.canWrite || !valid || save.isPending}
        onClick={() => save.mutate()}
      >
        {t('Save')}
      </Button>
    </div>
  )
}
