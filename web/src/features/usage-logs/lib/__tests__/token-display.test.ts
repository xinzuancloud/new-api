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
import { describe, expect, test } from 'vitest'

import type { LogOtherData } from '../../types'
import { getNewInputTokens, isInputNormalized } from '../format'

function makeOther(fields: Partial<LogOtherData>): LogOtherData {
  return { ...fields } as LogOtherData
}

describe('getNewInputTokens', () => {
  test('returns prompt_tokens unchanged for anthropic semantics even with cache hit', () => {
    const logOther = makeOther({
      usage_semantic: 'anthropic',
      cache_tokens: 124160,
    })
    expect(getNewInputTokens(671, logOther)).toBe(671)
  })

  test('returns prompt_tokens unchanged when other is missing', () => {
    expect(getNewInputTokens(500, null)).toBe(500)
  })

  test('returns prompt_tokens unchanged for openai semantics without cache hit', () => {
    const logOther = makeOther({ usage_semantic: undefined, cache_tokens: 0 })
    expect(getNewInputTokens(500, logOther)).toBe(500)
  })

  test('subtracts cached tokens for openai semantics (cache_tokens is a subset of prompt_tokens)', () => {
    const logOther = makeOther({ cache_tokens: 206080 })
    expect(getNewInputTokens(206767, logOther)).toBe(687)
  })

  test('clamps to zero when cache_tokens exceeds prompt_tokens in openai semantics', () => {
    const logOther = makeOther({ cache_tokens: 500 })
    expect(getNewInputTokens(300, logOther)).toBe(0)
  })
})

describe('isInputNormalized', () => {
  test('is false for anthropic semantics regardless of cache', () => {
    const logOther = makeOther({
      usage_semantic: 'anthropic',
      cache_tokens: 100,
    })
    expect(isInputNormalized(671, logOther)).toBe(false)
  })

  test('is true only when openai semantics cache subtraction changed the figure', () => {
    expect(isInputNormalized(206767, makeOther({ cache_tokens: 206080 }))).toBe(
      true
    )
    expect(isInputNormalized(206767, makeOther({ cache_tokens: 0 }))).toBe(false)
    expect(isInputNormalized(206767, null)).toBe(false)
  })
})
