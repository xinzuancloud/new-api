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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

import {
  PROTOCOL_CHECKS,
  type ProfilesResponse,
  type ProbeRequest,
  type ProtocolCheck,
  type ProtocolFormat,
} from '../../lib/protocol-profiles-api'
import { PROTOCOL_FORMATS } from '../../lib/protocol-routing'
import { ProfileSelect, ProtocolCheckbox } from './profile-controls'

export function ProbeSetup(props: {
  data: ProfilesResponse
  disabled: boolean
  onCreate: (request: ProbeRequest) => void
}) {
  const { t } = useTranslation()
  const [profile, setProfile] = useState('')
  const [ids, setIds] = useState<number[]>([])
  const [models, setModels] = useState<string[]>([])
  const [checks, setChecks] = useState<ProtocolCheck[]>([
    'text',
    'stream',
    'tools',
  ])
  const [formats, setFormats] = useState<ProtocolFormat[]>([
    ...PROTOCOL_FORMATS,
  ])
  const [allowHosted, setAllowHosted] = useState(false)
  const [maxTokens, setMaxTokens] = useState(256)
  const channels = props.data.channels.filter(
    (channel) => channel.profile === profile
  )
  const availableModels = [
    ...new Set(channels.flatMap((channel) => channel.mapped_models)),
  ].sort()
  const checkLabels = {
    text: t('Text'),
    stream: t('Streaming'),
    tools: t('Tools'),
    namespaces: t('Tool namespaces'),
    images: t('Images'),
    web_search: t('Web search'),
  }
  return (
    <fieldset
      disabled={props.disabled}
      className='space-y-4 disabled:opacity-60'
    >
      <p className='rounded-md border p-3 text-sm'>
        {t(
          'Verification sends real upstream requests and consumes provider quota. Hosted web search may incur extra charges and requires explicit opt-in.'
        )}
      </p>
      <ProfileSelect
        catalog={props.data.profiles}
        value={profile}
        onChange={(value) => {
          setProfile(value)
          setIds([])
          setModels([])
        }}
        disabled={props.disabled}
      />
      <fieldset>
        <legend className='text-sm font-medium'>{t('Checks')}</legend>
        <div className='flex flex-wrap gap-x-5'>
          {PROTOCOL_CHECKS.map((check) => (
            <ProtocolCheckbox
              key={check}
              label={checkLabels[check]}
              checked={checks.includes(check)}
              disabled={
                props.disabled || (check === 'web_search' && !allowHosted)
              }
              onChange={(checked) =>
                setChecks((current) =>
                  checked
                    ? [...current, check]
                    : current.filter((value) => value !== check)
                )
              }
            />
          ))}
        </div>
      </fieldset>
      <ProtocolCheckbox
        label={t('Allow hosted web search charges')}
        checked={allowHosted}
        disabled={props.disabled}
        onChange={(checked) => {
          setAllowHosted(checked)
          if (!checked) {
            setChecks((current) =>
              current.filter((check) => check !== 'web_search')
            )
          }
        }}
      />
      <fieldset>
        <legend className='text-sm font-medium'>{t('Formats')}</legend>
        <div className='flex flex-wrap gap-x-5'>
          {PROTOCOL_FORMATS.map((format) => (
            <ProtocolCheckbox
              key={format}
              label={format}
              checked={formats.includes(format)}
              disabled={props.disabled}
              onChange={(checked) =>
                setFormats((current) =>
                  checked
                    ? [...current, format]
                    : current.filter((value) => value !== format)
                )
              }
            />
          ))}
        </div>
      </fieldset>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Tool namespaces use Responses only. Tool and web search checks include streaming.'
        )}
      </p>
      <fieldset>
        <legend className='text-sm font-medium'>{t('Models')}</legend>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Leave models and channels unselected to test one representative bound channel per mapped model.'
          )}
        </p>
        <div className='flex max-h-40 flex-wrap gap-x-5 overflow-y-auto'>
          {availableModels.map((model) => (
            <ProtocolCheckbox
              key={model}
              label={model}
              checked={models.includes(model)}
              disabled={props.disabled}
              onChange={(checked) =>
                setModels((current) =>
                  checked
                    ? [...current, model]
                    : current.filter((value) => value !== model)
                )
              }
            />
          ))}
        </div>
      </fieldset>
      <fieldset>
        <legend className='text-sm font-medium'>
          {t('Explicit channels')}
        </legend>
        <div className='flex gap-2'>
          <Button
            type='button'
            variant='outline'
            disabled={props.disabled}
            onClick={() =>
              setIds(
                channels
                  .filter((channel) => !channel.diagnostic)
                  .map((channel) => channel.id)
              )
            }
          >
            {t('Select all')}
          </Button>
          <Button
            type='button'
            variant='ghost'
            disabled={props.disabled}
            onClick={() => setIds([])}
          >
            {t('Clear')}
          </Button>
        </div>
        <div className='max-h-48 overflow-y-auto'>
          {channels.map((channel) => (
            <ProtocolCheckbox
              key={channel.id}
              label={`${channel.id} · ${channel.name}${channel.diagnostic ? ` · ${t('Invalid channel configuration; repair before binding or verification.')}` : ''}`}
              checked={ids.includes(channel.id)}
              disabled={props.disabled || Boolean(channel.diagnostic)}
              onChange={(checked) =>
                setIds((current) =>
                  checked
                    ? [...current, channel.id]
                    : current.filter((value) => value !== channel.id)
                )
              }
            />
          ))}
        </div>
      </fieldset>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Explicit channel selection verifies each account. A multi-key channel samples its first enabled key. Disabled channels cannot be tested.'
        )}
      </p>
      <div className='space-y-2'>
        <Label htmlFor='protocol-max-tokens'>{t('Max output tokens')}</Label>
        <Input
          id='protocol-max-tokens'
          type='number'
          min={1}
          max={1024}
          value={maxTokens}
          onChange={(event) => setMaxTokens(Number(event.target.value))}
        />
      </div>
      <Button
        type='button'
        disabled={
          props.disabled ||
          !profile ||
          !checks.length ||
          !formats.length ||
          !Number.isInteger(maxTokens) ||
          maxTokens < 1 ||
          maxTokens > 1024
        }
        onClick={() =>
          props.onCreate({
            profile,
            checks,
            formats,
            max_output_tokens: maxTokens,
            allow_hosted: allowHosted,
            ...(ids.length ? { channel_ids: ids } : {}),
            ...(models.length ? { models } : {}),
          })
        }
      >
        {t('Create verification report')}
      </Button>
    </fieldset>
  )
}
