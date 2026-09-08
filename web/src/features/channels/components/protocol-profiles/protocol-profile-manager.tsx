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
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  ADMIN_PERMISSION_ACTIONS,
  ADMIN_PERMISSION_RESOURCES,
  hasPermission,
} from '@/lib/admin-permissions'
import { getServerErrorMessageKey } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { protocolProfilesAPI } from '../../lib/protocol-profiles-api'
import { ProfileBinding } from './profile-binding'
import { ProfileCatalog } from './profile-catalog'
import { ProfileProbes } from './profile-probes'

export default function ProtocolProfileManager(props: { onClose: () => void }) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const canRead = hasPermission(
    user,
    ADMIN_PERMISSION_RESOURCES.CHANNEL,
    ADMIN_PERMISSION_ACTIONS.READ
  )
  const canWrite = hasPermission(
    user,
    ADMIN_PERMISSION_RESOURCES.CHANNEL,
    ADMIN_PERMISSION_ACTIONS.WRITE
  )
  const canOperate = hasPermission(
    user,
    ADMIN_PERMISSION_RESOURCES.CHANNEL,
    ADMIN_PERMISSION_ACTIONS.OPERATE
  )
  const catalog = useQuery({
    queryKey: ['protocol-profiles', 'catalog'],
    queryFn: ({ signal }) => protocolProfilesAPI.catalog(signal),
    enabled: canRead,
    refetchOnWindowFocus: false,
  })
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('Shared protocol profiles')}
      contentClassName='sm:max-w-6xl'
      footer={
        <Button variant='outline' onClick={props.onClose}>
          {t('Close')}
        </Button>
      }
    >
      {!canRead && (
        <ErrorState description={t('No permission to perform this action')} />
      )}
      {canRead && catalog.isPending && <LoadingState />}
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
      {catalog.data && (
        <div className='space-y-4'>
          <Button
            variant='outline'
            onClick={() => {
              void catalog.refetch()
            }}
          >
            {t('Refresh profiles')}
          </Button>
          <Tabs defaultValue='binding'>
            <TabsList>
              <TabsTrigger value='binding'>{t('Bind channels')}</TabsTrigger>
              <TabsTrigger value='catalog'>{t('Profile catalog')}</TabsTrigger>
              <TabsTrigger value='probes'>
                {t('Batch verification')}
              </TabsTrigger>
            </TabsList>
            <TabsContent value='binding'>
              <ProfileBinding data={catalog.data} canWrite={canWrite} />
            </TabsContent>
            <TabsContent value='catalog'>
              <ProfileCatalog
                key={catalog.data.revision}
                data={catalog.data}
                canWrite={canWrite}
              />
            </TabsContent>
            <TabsContent value='probes'>
              <ProfileProbes
                data={catalog.data}
                canOperate={canOperate}
                canWrite={canWrite}
              />
            </TabsContent>
          </Tabs>
        </div>
      )}
    </Dialog>
  )
}
