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
import type { TFunction } from 'i18next'
import { describe, expect, it } from 'vitest'

import { formatDuration, formatResetPeriod } from '../format'

const t = ((key: string) => key) as TFunction

describe('subscription duration formatting', () => {
  it('preserves minutes when a custom duration is 90 minutes', () => {
    expect(
      formatDuration({ duration_unit: 'custom', custom_seconds: 90 * 60 }, t)
    ).toBe('1 hour 30 minutes')
  })

  it('preserves hours when a custom duration is 25 hours', () => {
    expect(
      formatDuration({ duration_unit: 'custom', custom_seconds: 25 * 3600 }, t)
    ).toBe('1 day 1 hour')
  })

  it.each([
    [90 * 60, '1 hour 30 minutes'],
    [25 * 3600, '1 day 1 hour'],
    [61, '1 minute 1 second'],
    [60, '1 minute'],
    [1, '1 second'],
  ])(
    'preserves custom reset remainders for %i seconds',
    (seconds, expected) => {
      expect(
        formatResetPeriod(
          { quota_reset_period: 'custom', quota_reset_custom_seconds: seconds },
          t
        )
      ).toBe(expected)
    }
  )

  it('keeps fixed subscription duration and reset labels unchanged', () => {
    expect(
      formatDuration({ duration_unit: 'month', duration_value: 2 }, t)
    ).toBe('2 months')
    expect(formatResetPeriod({ quota_reset_period: 'monthly' }, t)).toBe(
      'Monthly'
    )
  })
})
