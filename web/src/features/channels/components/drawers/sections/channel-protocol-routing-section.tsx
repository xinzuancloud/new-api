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
import { useWatch, type UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import {
  SideDrawerSection,
  SideDrawerSectionHeader,
} from '@/components/drawer-layout'
import { JsonCodeEditor } from '@/components/json-code-editor'
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'

import type { ChannelFormValues } from '../../../lib/channel-form'
import {
  PROTOCOL_DEFAULT_POLICY_JSON,
  PROTOCOL_FEATURES,
  PROTOCOL_FORMATS,
} from '../../../lib/protocol-routing'
import { ChannelProfileField } from './channel-profile-field'

export function ChannelProtocolRoutingSection(props: {
  form: UseFormReturn<ChannelFormValues>
  disabled?: boolean
  id?: string
  channelId?: number
}) {
  const { t } = useTranslation()
  const enabled = useWatch({
    control: props.form.control,
    name: 'protocol_routing_enabled',
  })
  const profile = useWatch({
    control: props.form.control,
    name: 'protocol_routing_profile',
  })
  const defaults = useWatch({
    control: props.form.control,
    name: 'protocol_routing_defaults',
  })
  return (
    <div id={props.id} className='scroll-mt-4'>
      <SideDrawerSection>
        <SideDrawerSectionHeader
          title={t('Protocol routing')}
          description={t(
            'Route each model through explicitly verified upstream endpoints.'
          )}
        />
        <p className='text-muted-foreground text-xs'>
          {t(
            'Currently supported channel types: OpenAI, Anthropic, Moonshot, and VolcEngine. Other channel adapters require additional signing or URL handling and are not yet supported.'
          )}
        </p>
        <FormField
          control={props.form.control}
          name='protocol_routing_enabled'
          render={({ field }) => (
            <FormItem className='flex items-center justify-between gap-4'>
              <div className='space-y-1'>
                <FormLabel>{t('Enable protocol routing')}</FormLabel>
                <FormDescription>
                  {t('Disabled channels keep their existing routing behavior.')}
                </FormDescription>
              </div>
              <FormControl>
                <Switch
                  checked={field.value === true}
                  disabled={props.disabled}
                  onCheckedChange={(checked) => {
                    if (checked && !defaults?.trim()) {
                      props.form.setValue(
                        'protocol_routing_defaults',
                        profile ? '{}' : PROTOCOL_DEFAULT_POLICY_JSON,
                        { shouldDirty: true }
                      )
                      props.form.setValue('protocol_routing_models', '{}', {
                        shouldDirty: true,
                      })
                    }
                    field.onChange(checked)
                  }}
                />
              </FormControl>
            </FormItem>
          )}
        />
        {(enabled || profile || defaults?.trim()) && (
          <fieldset
            disabled={props.disabled}
            className='space-y-4 disabled:opacity-60'
          >
            <ChannelProfileField
              form={props.form}
              channelId={props.channelId}
              disabled={props.disabled}
            />
            <FormField
              control={props.form.control}
              name='protocol_routing_account_resource'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Account resource')}</FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      value={field.value || ''}
                      disabled={props.disabled}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Use the same nonsecret identifier for channels sharing an upstream account. Leave blank to use this channel identity.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={props.form.control}
              name='protocol_routing_quota_scope'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Quota scope')}</FormLabel>
                  <Select
                    disabled={props.disabled}
                    value={field.value || 'model'}
                    onValueChange={field.onChange}
                    items={[
                      { value: 'model', label: t('Per model') },
                      { value: 'account', label: t('Whole account') },
                    ]}
                  >
                    <FormControl>
                      <SelectTrigger>
                        <SelectValue />
                      </SelectTrigger>
                    </FormControl>
                    <SelectContent>
                      <SelectItem value='model'>{t('Per model')}</SelectItem>
                      <SelectItem value='account'>
                        {t('Whole account')}
                      </SelectItem>
                    </SelectContent>
                  </Select>
                  <FormDescription>
                    {t(
                      'Choose whether quota cooldown applies to one model or the whole account.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={props.form.control}
              name='protocol_routing_defaults'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Default protocol policy')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Declare entry_formats, endpoints, and loss_policy (safe, allow, strict). Only verified endpoints are eligible; verify capabilities with your provider before setting verified to true.'
                    )}
                  </FormDescription>
                  <FormControl>
                    <JsonCodeEditor
                      value={field.value || ''}
                      onChange={field.onChange}
                      name={field.name}
                      onBlur={field.onBlur}
                      textareaRef={field.ref}
                      disabled={props.disabled}
                      ariaLabel={t('Default protocol policy')}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <p className='text-muted-foreground text-xs break-words'>
              {t(
                'Supported formats: {{formats}}. Endpoint paths must start with / and contain no host, query, fragment, escapes, or relative segments.',
                { formats: PROTOCOL_FORMATS.join(', ') }
              )}
            </p>
            <p className='text-muted-foreground text-xs break-words'>
              {t(
                'Declare only verified features: {{features}}. Stateful and background requests are currently unsupported.',
                { features: PROTOCOL_FEATURES.join(', ') }
              )}{' '}
              {t(
                'The stateful and background feature fields are reserved for future support; declaring them does not enable these requests.'
              )}{' '}
              {t(
                'Declare context_editing only for verified native Claude context management. This capability does not enable server-side conversation state.'
              )}
            </p>
            <FormField
              control={props.form.control}
              name='protocol_routing_models'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Model protocol overrides')}</FormLabel>
                  <FormDescription>
                    {t(
                      'JSON object keyed by mapped upstream model name. Partial policies inherit defaults; use {} for no differences.'
                    )}
                  </FormDescription>
                  <FormControl>
                    <JsonCodeEditor
                      value={field.value || '{}'}
                      onChange={field.onChange}
                      name={field.name}
                      onBlur={field.onBlur}
                      textareaRef={field.ref}
                      disabled={props.disabled}
                      ariaLabel={t('Model protocol overrides')}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          </fieldset>
        )}
      </SideDrawerSection>
    </div>
  )
}
