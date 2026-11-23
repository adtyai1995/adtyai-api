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
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

/** Root sees every site; a site owner sees teams currently on its own site. */
export type TeamAdminScope = 'root' | 'owner'

export type TeamStatusFilter = '' | 'active' | 'closed'

export type TeamAdminRow = {
  id: number
  site_id: number
  source_site_id: number
  leader_user_id: number
  name: string
  discount: string
  status: string
  shared_quota: number
  created_at: number
  closed_at: number
  site_name: string
  source_site_name?: string
  leader_username: string
  active_members: number
  active_keys: number
}

export type TeamAdminMember = {
  user_id: number
  username: string
  role: string
  status: string
  independent_employee?: boolean
}

export type TeamAdminKey = {
  token_id: number
  name: string
  user_id: number
  status: string
  binding_status?: string
}

export type TeamAdminDetail = TeamAdminRow & {
  members: TeamAdminMember[]
  keys: TeamAdminKey[]
}

export type TeamLedgerRow = {
  id: number
  team_id: number
  delta_quota: number
  balance_before: number
  balance_after: number
  type: string
  ref_id: string
  actor_user_id: number
  created_at: number
}

type ApiResponse<T> = { success: boolean; message?: string; data: T }
type Page<T> = { items: T[]; total: number }

export const TEAM_ADMIN_PAGE_SIZE = 20

function teamsBase(scope: TeamAdminScope) {
  return scope === 'root' ? '/api/adty/admin/teams' : '/api/adty/site/teams'
}

export const teamAdminKeys = {
  all: ['adty-team-admin'] as const,
  list: (scope: TeamAdminScope, params: Record<string, unknown>) =>
    ['adty-team-admin', scope, 'list', params] as const,
  detail: (scope: TeamAdminScope, id: number) =>
    ['adty-team-admin', scope, 'detail', id] as const,
  ledger: (scope: TeamAdminScope, id: number, page: number) =>
    ['adty-team-admin', scope, 'ledger', id, page] as const,
}

export async function listAdminTeams(
  scope: TeamAdminScope,
  params: { p: number; status: TeamStatusFilter; q: string; siteId?: string }
): Promise<Page<TeamAdminRow>> {
  const query: Record<string, string | number> = {
    p: params.p,
    page_size: TEAM_ADMIN_PAGE_SIZE,
    q: params.q,
  }
  if (scope === 'owner') {
    // Without status the owner list returns active teams only.
    query.status = params.status === '' ? 'all' : params.status
  } else {
    if (params.status) query.status = params.status
    if (params.siteId !== undefined && params.siteId !== '') {
      query.site_id = params.siteId
    }
  }
  const res = await api.get<ApiResponse<Page<TeamAdminRow>>>(
    teamsBase(scope),
    { params: query }
  )
  return requireServerSuccess(res.data).data
}

export async function getAdminTeam(
  scope: TeamAdminScope,
  id: number
): Promise<TeamAdminDetail> {
  const res = await api.get<ApiResponse<TeamAdminDetail>>(
    `${teamsBase(scope)}/${id}`
  )
  return requireServerSuccess(res.data).data
}

export async function listAdminTeamLedger(
  scope: TeamAdminScope,
  id: number,
  page: number
): Promise<Page<TeamLedgerRow>> {
  const res = await api.get<ApiResponse<Page<TeamLedgerRow>>>(
    `${teamsBase(scope)}/${id}/ledgers`,
    { params: { p: page, page_size: TEAM_ADMIN_PAGE_SIZE } }
  )
  return requireServerSuccess(res.data).data
}

/** Root sets a main-site team discount; the owner sets a discount on its own site team. */
export async function saveTeamDiscount(
  scope: TeamAdminScope,
  id: number,
  discount: string
): Promise<void> {
  const url =
    scope === 'root'
      ? `/api/adty/admin/teams/${id}/agreement-price`
      : `/api/adty/site/teams/${id}/discount`
  const res = await api.put<ApiResponse<{ updated: boolean }>>(url, {
    discount: discount.trim(),
  })
  requireServerSuccess(res.data)
}
