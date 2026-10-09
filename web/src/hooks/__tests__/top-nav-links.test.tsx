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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it } from 'vitest'

import { STATUS_QUERY_KEY } from '@/lib/status-query'
import { useAuthStore } from '@/stores/auth-store'

import { useTopNavLinks } from '../use-top-nav-links'

/**
 * The header nav is driven by the backend `HeaderNavModules` option, and the
 * site hides FAQ by storing `faq: false` there instead of shipping a source
 * change. Losing that key silently restores the tab, so pin the mapping.
 */

function navFor(headerNavModules: string) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client.setQueryData(STATUS_QUERY_KEY, {
    HeaderNavModules: headerNavModules,
    docs_link: '',
  })
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'nav-user',
    role: 1,
    quota: 1000000,
    used_quota: 0,
    request_count: 0,
  })

  function Wrapper(props: { children: ReactNode }) {
    return (
      <QueryClientProvider client={client}>
        {props.children}
      </QueryClientProvider>
    )
  }

  return renderHook(() => useTopNavLinks(), { wrapper: Wrapper }).result.current
}

afterEach(() => {
  cleanup()
  useAuthStore.getState().auth.reset()
})

describe('top navigation FAQ entry', () => {
  it('hides the FAQ entry when the backend option sets faq to false', () => {
    const links = navFor(
      JSON.stringify({
        home: true,
        console: true,
        docs: true,
        about: false,
        faq: false,
      })
    )

    expect(links.some((link) => link.href === '/faq')).toBe(false)
    expect(links.some((link) => link.title === 'FAQ')).toBe(false)
  })

  it('keeps the FAQ entry when the backend option omits or enables faq', () => {
    for (const raw of [
      JSON.stringify({ home: true, console: true, docs: true }),
      JSON.stringify({ home: true, console: true, docs: true, faq: true }),
    ]) {
      expect(navFor(raw).some((link) => link.href === '/faq')).toBe(true)
    }
  })
})
