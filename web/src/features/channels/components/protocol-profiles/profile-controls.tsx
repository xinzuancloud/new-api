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

import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

import type { ProtocolCatalog } from '../../lib/protocol-profiles-api'

export function ProfileSelect(props: {
  catalog: ProtocolCatalog
  value: string
  onChange: (value: string) => void
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const items = [
    { value: '', label: t('Local configuration') },
    ...Object.entries(props.catalog).map(([value, profile]) => ({
      value,
      label: `${profile.name} (${value})`,
    })),
  ]
  if (props.value && !props.catalog[props.value]) {
    items.push({ value: props.value, label: props.value })
  }
  return (
    <div className='space-y-2'>
      <Label>{t('Shared protocol profile')}</Label>
      <Select
        value={props.value}
        onValueChange={(value) => {
          if (value !== null) props.onChange(value)
        }}
        items={items}
        disabled={props.disabled}
      >
        <SelectTrigger aria-label={t('Shared protocol profile')}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {items.map((item) => (
            <SelectItem key={item.value} value={item.value}>
              {item.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  )
}

export function ProtocolCheckbox(props: {
  label: string
  checked: boolean
  onChange: (checked: boolean) => void
  disabled?: boolean
}) {
  return (
    <Label className='flex items-center gap-2 py-1'>
      <Checkbox
        checked={props.checked}
        onCheckedChange={props.onChange}
        disabled={props.disabled}
      />
      {props.label}
    </Label>
  )
}
