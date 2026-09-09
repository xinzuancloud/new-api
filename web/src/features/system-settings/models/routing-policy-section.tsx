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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMemo } from 'react'
import { Controller, useFieldArray, useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

import { SettingsForm } from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { useUpdateOption } from '../hooks/use-update-option'
import {
  createRoutingPolicyFormSchema,
  routingPolicyFormDefaults,
  serializeRoutingPolicy,
  type RoutingPolicyFormValues,
} from './lib/routing-policy'

export function RoutingPolicySection(props: { defaultValue: string }) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const loaded = useMemo(() => {
    try {
      return {
        values: routingPolicyFormDefaults(props.defaultValue),
        invalid: false,
      }
    } catch {
      return { values: routingPolicyFormDefaults(''), invalid: true }
    }
  }, [props.defaultValue])
  const form = useForm<RoutingPolicyFormValues>({
    resolver: zodResolver(createRoutingPolicyFormSchema(t)),
    defaultValues: loaded.values,
  })
  useResetForm(form, loaded.values)
  const groups = useFieldArray({ control: form.control, name: 'groups' })
  const capacities = useFieldArray({
    control: form.control,
    name: 'capacities',
  })
  const errors = form.formState.errors
  const disabled = loaded.invalid || updateOption.isPending

  const onSubmit = async (values: RoutingPolicyFormValues) => {
    if (disabled) return
    form.clearErrors('root')
    try {
      const result = await updateOption.mutateAsync({
        key: 'RoutingPolicy',
        value: serializeRoutingPolicy(values),
      })
      if (!result.success) {
        form.setError('root', {
          message: result.message || t('Failed to update setting'),
        })
        return
      }
      form.reset(values)
    } catch {
      form.setError('root', { message: t('Failed to update setting') })
    }
  }

  const numericFields = [
    {
      name: 'max_attempts_per_tag',
      label: t('Attempts per tag (0 = all available)'),
      min: 0,
      max: 256,
      description: t(
        'Zero tries every currently available, untried channel with the current tag before fallback. A positive value limits attempts for each tag.'
      ),
    },
    {
      name: 'max_total_attempts',
      label: t('Total attempts per request'),
      min: 0,
      max: 1024,
      description: t(
        'Includes the initial attempt and all retries across tags. Zero uses Retry Times plus the initial attempt.'
      ),
    },
    {
      name: 'rate_limit_cooldown_seconds',
      label: t('Rate-limit cooldown (seconds)'),
      min: 0,
      max: 3600,
    },
    {
      name: 'rate_limit_max_cooldown_seconds',
      label: t('Maximum rate-limit cooldown (seconds)'),
      min: 0,
      max: 86400,
      description: t(
        'Repeated 429 responses double the cooldown up to this value. A successful request resets the backoff.'
      ),
    },
    {
      name: 'quota_cooldown_seconds',
      label: t('Quota cooldown (seconds)'),
      min: 0,
      max: 604800,
    },
    {
      name: 'request_timeout_seconds',
      label: t('Whole-request timeout (seconds)'),
      min: 1,
      max: 1800,
    },
  ] as const

  return (
    <SettingsSection title={t('Routing Policy')}>
      <SettingsForm onSubmit={form.handleSubmit(onSubmit)} noValidate>
        <SettingsPageFormActions
          onSave={form.handleSubmit(onSubmit)}
          isSaving={updateOption.isPending}
          isSaveDisabled={loaded.invalid}
        />
        {loaded.invalid && (
          <FieldError>{t('Unable to load routing policy')}</FieldError>
        )}
        <FieldError errors={[errors.root]} />
        <FieldSet disabled={disabled}>
          <FieldGroup>
            <Controller
              control={form.control}
              name='enabled'
              render={({ field }) => (
                <Field orientation='horizontal' data-disabled={disabled}>
                  <FieldLabel htmlFor='routing-policy-enabled'>
                    {t('Enable routing policy')}
                  </FieldLabel>
                  <Switch
                    id='routing-policy-enabled'
                    checked={field.value}
                    onCheckedChange={field.onChange}
                    disabled={disabled}
                  />
                </Field>
              )}
            />
            <FieldDescription>
              {t(
                'Existing group and model permissions still apply. For configured groups, only listed channel tags are eligible. Unconfigured groups keep native routing.'
              )}
            </FieldDescription>
            <FieldDescription>
              {t(
                'Tags are tried from top to bottom. Channel affinity only applies within the highest healthy tier.'
              )}
            </FieldDescription>
            <FieldSet>
              <FieldLegend>{t('Group routing order')}</FieldLegend>
              <FieldDescription>
                {t(
                  'Use existing group names and channel tags. Enter one tag per line, highest priority first.'
                )}
              </FieldDescription>
              {groups.fields.length === 0 && (
                <FieldDescription>
                  {t('No group routing rules')}
                </FieldDescription>
              )}
              {groups.fields.map((row, index) => (
                <FieldGroup key={row.id} className='rounded-lg border p-4'>
                  <Field data-invalid={!!errors.groups?.[index]?.group}>
                    <FieldLabel htmlFor={`routing-group-${row.id}`}>
                      {t('Group')}
                    </FieldLabel>
                    <Input
                      id={`routing-group-${row.id}`}
                      {...form.register(`groups.${index}.group`)}
                      aria-invalid={!!errors.groups?.[index]?.group}
                      aria-describedby={`routing-group-error-${row.id}`}
                    />
                    <FieldError
                      id={`routing-group-error-${row.id}`}
                      errors={[errors.groups?.[index]?.group]}
                    />
                  </Field>
                  <Field data-invalid={!!errors.groups?.[index]?.tags}>
                    <FieldLabel htmlFor={`routing-tags-${row.id}`}>
                      {t('Channel tags in priority order')}
                    </FieldLabel>
                    <Textarea
                      id={`routing-tags-${row.id}`}
                      rows={3}
                      {...form.register(`groups.${index}.tags`)}
                      aria-invalid={!!errors.groups?.[index]?.tags}
                      aria-describedby={`routing-tags-error-${row.id}`}
                    />
                    <FieldError
                      id={`routing-tags-error-${row.id}`}
                      errors={[errors.groups?.[index]?.tags]}
                    />
                  </Field>
                  <Button
                    type='button'
                    variant='outline'
                    className='self-start'
                    onClick={() => groups.remove(index)}
                    disabled={disabled}
                  >
                    {t('Remove group rule')}
                  </Button>
                </FieldGroup>
              ))}
              <Button
                type='button'
                variant='outline'
                className='self-start'
                onClick={() => groups.append({ group: '', tags: '' })}
                disabled={disabled}
              >
                {t('Add group rule')}
              </Button>
            </FieldSet>
            <FieldSet>
              <FieldLegend>{t('Account capacity by channel tag')}</FieldLegend>
              <FieldDescription>
                {t(
                  'The gateway prefers the lowest-load account in a tag and skips accounts whose projected rolling load exceeds these limits. Zero disables one limit.'
                )}
              </FieldDescription>
              {capacities.fields.length === 0 && (
                <FieldDescription>{t('No capacity rules')}</FieldDescription>
              )}
              {capacities.fields.map((row, index) => (
                <FieldGroup
                  key={row.id}
                  className='grid rounded-lg border p-4 md:grid-cols-2'
                >
                  <Field data-invalid={!!errors.capacities?.[index]?.tag}>
                    <FieldLabel htmlFor={`routing-capacity-tag-${row.id}`}>
                      {t('Channel tag')}
                    </FieldLabel>
                    <Input
                      id={`routing-capacity-tag-${row.id}`}
                      {...form.register(`capacities.${index}.tag`)}
                      aria-invalid={!!errors.capacities?.[index]?.tag}
                    />
                    <FieldError errors={[errors.capacities?.[index]?.tag]} />
                  </Field>
                  {(
                    [
                      [
                        'window_seconds',
                        t('Rolling window (seconds)'),
                        10,
                        3600,
                      ],
                      ['max_requests', t('Maximum requests'), 0, 1_000_000],
                      [
                        'max_input_tokens',
                        t('Maximum input tokens'),
                        0,
                        1_000_000_000_000,
                      ],
                    ] as const
                  ).map(([name, label, min, max]) => (
                    <Field
                      key={name}
                      data-invalid={!!errors.capacities?.[index]?.[name]}
                    >
                      <FieldLabel
                        htmlFor={`routing-capacity-${name}-${row.id}`}
                      >
                        {label}
                      </FieldLabel>
                      <Input
                        id={`routing-capacity-${name}-${row.id}`}
                        type='number'
                        min={min}
                        max={max}
                        step={1}
                        {...form.register(`capacities.${index}.${name}`, {
                          valueAsNumber: true,
                        })}
                        aria-invalid={!!errors.capacities?.[index]?.[name]}
                      />
                      <FieldError
                        errors={[errors.capacities?.[index]?.[name]]}
                      />
                    </Field>
                  ))}
                  <Button
                    type='button'
                    variant='outline'
                    className='self-start'
                    onClick={() => capacities.remove(index)}
                    disabled={disabled}
                  >
                    {t('Remove capacity rule')}
                  </Button>
                </FieldGroup>
              ))}
              <Button
                type='button'
                variant='outline'
                className='self-start'
                onClick={() =>
                  capacities.append({
                    tag: '',
                    window_seconds: 60,
                    max_requests: 0,
                    max_input_tokens: 0,
                  })
                }
                disabled={disabled}
              >
                {t('Add capacity rule')}
              </Button>
            </FieldSet>
            <FieldGroup className='grid gap-5 md:grid-cols-2'>
              {numericFields.map((field) => (
                <Field key={field.name} data-invalid={!!errors[field.name]}>
                  <FieldLabel htmlFor={`routing-${field.name}`}>
                    {field.label}
                  </FieldLabel>
                  <Input
                    id={`routing-${field.name}`}
                    type='number'
                    min={field.min}
                    max={field.max}
                    step={1}
                    {...form.register(field.name, { valueAsNumber: true })}
                    aria-invalid={!!errors[field.name]}
                    aria-describedby={`routing-${field.name}-description routing-${field.name}-error`}
                  />
                  <FieldDescription id={`routing-${field.name}-description`}>
                    {t('Allowed range: {{min}}–{{max}}', {
                      min: field.min,
                      max: field.max,
                    })}{' '}
                    {'description' in field && field.description}
                  </FieldDescription>
                  <FieldError
                    id={`routing-${field.name}-error`}
                    errors={[errors[field.name]]}
                  />
                </Field>
              ))}
            </FieldGroup>
            <FieldDescription>
              {t(
                'Cooldowns temporarily skip a channel for the requested model. Zero disables that cooldown. Channels become eligible after expiry without an automatic recovery test.'
              )}
            </FieldDescription>
            <FieldDescription>
              {t(
                'When trying all channels, reaching the total attempt limit or timeout ends the request; it does not skip to another supplier.'
              )}
            </FieldDescription>
            <FieldDescription>
              {t(
                'The whole-request timeout includes all attempts and streaming.'
              )}
            </FieldDescription>
            <Field>
              <FieldLabel htmlFor='routing-quota-keywords'>
                {t('Quota error keywords')}
              </FieldLabel>
              <Textarea
                id='routing-quota-keywords'
                rows={4}
                {...form.register('quota_error_keywords')}
                aria-describedby='routing-quota-keywords-description'
              />
              <FieldDescription id='routing-quota-keywords-description'>
                {t(
                  'Enter one keyword per line. Upstream errors matching any keyword, ignoring case, trigger the quota cooldown.'
                )}
              </FieldDescription>
            </Field>
          </FieldGroup>
        </FieldSet>
      </SettingsForm>
    </SettingsSection>
  )
}
