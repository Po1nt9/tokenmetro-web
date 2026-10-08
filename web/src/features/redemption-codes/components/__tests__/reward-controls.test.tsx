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
import {
  flexRender,
  getCoreRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import type { Redemption } from '../../types'
import { useRedemptionsColumns } from '../redemptions-columns'
import { RedemptionsDialogs } from '../redemptions-dialogs'
import { RedemptionsProvider } from '../redemptions-provider'

const i18n = (await import('i18next')).default
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { Toaster } = await import('sonner')

await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

function redemption(
  id: number,
  overrides: Partial<Redemption> = {}
): Redemption {
  return {
    id,
    user_id: 1,
    name: `code-${id}`,
    key: `key-${id}`,
    status: 1,
    quota: 500000,
    outcome_type: 'balance',
    subscription_plan_id: 0,
    created_time: 1,
    redeemed_time: 0,
    expired_time: 0,
    used_user_id: 0,
    reward_eligible: true,
    ...overrides,
  }
}

// Renders the real column definitions and the real dialogs on top of a minimal
// table, so the badge and the row action are asserted where an operator sees
// them instead of through the components' internals.
function RedemptionsHarness(props: { redemptions: Redemption[] }) {
  const columns = useRedemptionsColumns()
  const table = useReactTable({
    data: props.redemptions,
    columns,
    getCoreRowModel: getCoreRowModel(),
  })
  return (
    <RedemptionsProvider>
      <table>
        <tbody>
          {table.getRowModel().rows.map((row) => (
            <tr key={row.id}>
              {row.getVisibleCells().map((cell) => (
                <td key={cell.id}>
                  {flexRender(cell.column.columnDef.cell, cell.getContext())}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      <RedemptionsDialogs />
    </RedemptionsProvider>
  )
}

function renderHarness(redemptions: Redemption[]) {
  return render(
    <I18nextProvider i18n={i18n}>
      <RedemptionsHarness redemptions={redemptions} />
      <Toaster duration={60_000} />
    </I18nextProvider>
  )
}

async function openRowMenu() {
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Open menu' }))
  return user
}

async function openVoidDialog() {
  const user = await openRowMenu()
  await user.click(
    await screen.findByRole('menuitem', { name: 'Void invitation reward' })
  )
  return user
}

beforeEach(() => {
  useSystemConfigStore.getState().setConfig({
    currency: { ...DEFAULT_CURRENCY_CONFIG },
  })
})

afterEach(() => {
  vi.restoreAllMocks()
  localStorage.clear()
})

test('marks not-reward-eligible codes in the list without touching the others', () => {
  renderHarness([
    redemption(1, { name: 'sold-code', reward_eligible: true }),
    redemption(2, { name: 'granted-code', reward_eligible: false }),
  ])

  const grantedRow = screen.getByText('granted-code').closest('tr')
  expect(grantedRow).not.toBeNull()
  expect(
    within(grantedRow as HTMLElement).getByText('Not reward-eligible')
  ).toBeInTheDocument()

  const soldRow = screen.getByText('sold-code').closest('tr')
  expect(
    within(soldRow as HTMLElement).queryByText('Not reward-eligible')
  ).toBeNull()
})

test('hides the void reward entry while the code is still unused', async () => {
  renderHarness([redemption(3, { status: 1 })])

  await openRowMenu()
  // The menu is open once the always-present delete entry shows up.
  expect(
    await screen.findByRole('menuitem', { name: 'Delete' })
  ).toBeInTheDocument()
  expect(
    screen.queryByRole('menuitem', { name: 'Void invitation reward' })
  ).toBeNull()
})

test('hides the void reward entry for redeemed codes that earn no reward', async () => {
  renderHarness([redemption(4, { status: 3, reward_eligible: false })])

  await openRowMenu()
  expect(
    await screen.findByRole('menuitem', { name: 'Delete' })
  ).toBeInTheDocument()
  expect(
    screen.queryByRole('menuitem', { name: 'Void invitation reward' })
  ).toBeNull()
})

test('shows the void reward entry for redeemed reward-eligible codes', async () => {
  renderHarness([redemption(5, { status: 3, reward_eligible: true })])

  await openRowMenu()
  expect(
    await screen.findByRole('menuitem', { name: 'Void invitation reward' })
  ).toBeInTheDocument()
})

test('voids the pending invitation reward of the selected code', async () => {
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true } })
  renderHarness([redemption(7, { status: 3 })])

  const user = await openVoidDialog()
  const dialog = await screen.findByRole('alertdialog', {
    name: 'Void invitation reward',
  })
  await user.click(
    within(dialog).getByRole('button', { name: 'Void invitation reward' })
  )

  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  expect(post).toHaveBeenCalledWith('/api/redemption/reward/void', {
    redemption_id: 7,
  })
  expect(
    await screen.findByText('Invitation reward voided successfully')
  ).toBeInTheDocument()
})

test('reports the backend reason verbatim when the reward cannot be voided', async () => {
  vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: false,
      message: 'the invitation reward has been settled and cannot be reversed',
    },
  })
  renderHarness([redemption(8, { status: 3 })])

  const user = await openVoidDialog()
  const dialog = await screen.findByRole('alertdialog', {
    name: 'Void invitation reward',
  })
  await user.click(
    within(dialog).getByRole('button', { name: 'Void invitation reward' })
  )

  expect(
    await screen.findByText(
      'the invitation reward has been settled and cannot be reversed'
    )
  ).toBeInTheDocument()
  // The dialog stays open so the operator can see the record did not change.
  expect(
    screen.getByRole('alertdialog', { name: 'Void invitation reward' })
  ).toBeInTheDocument()
})
