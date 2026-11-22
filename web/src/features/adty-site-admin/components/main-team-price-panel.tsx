import { useMutation, useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
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
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { api } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

/** Root sets a main-site team discount. The form does not suggest a floor. */
export function MainTeamPricePanel() {
  const { t } = useTranslation()
  const isRoot =
    useAuthStore((state) => state.auth.user?.role) === ROLE.SUPER_ADMIN
  const [teamId, setTeamId] = useState('')
  const [discount, setDiscount] = useState('')
  const [page, setPage] = useState(1)
  const teams = useQuery({
    queryKey: ['main-site-teams', page],
    enabled: isRoot,
    queryFn: async () => {
      type Team = {
        id: number
        name: string
        leader_user_id: number
        discount: string
        status: string
      }
      const result = await api.get<{
        success: boolean
        data: { items: Team[]; total: number }
      }>('/api/adty/admin/teams', { params: { p: page, page_size: 20 } })
      return requireServerSuccess(result.data).data
    },
  })
  const selected = teams.data?.items.find((team) => String(team.id) === teamId)
  const save = useMutation({
    mutationFn: async () => {
      const res = await api.put(
        `/api/adty/admin/teams/${teamId.trim()}/agreement-price`,
        {
          discount: discount.trim(),
        }
      )
      requireServerSuccess(res.data)
    },
    onSuccess: () => {
      void teams.refetch()
      setTeamId('')
      setDiscount('')
    },
    onError: (error) => handleServerError(error),
  })
  if (!isRoot) return null
  return (
    <section className='flex flex-col gap-3'>
      <h2 className='text-sm font-medium'>{t('Agreement price')}</h2>
      <p className='text-muted-foreground text-sm'>
        {t(
          'A main-site team discount has no supply floor. A subsite team is refused.'
        )}
      </p>
      <div className='grid gap-3 sm:grid-cols-2'>
        <div className='flex flex-col gap-1'>
          <Label htmlFor='main-team-id'>{t('Team')}</Label>
          <select
            id='main-team-id'
            className='rounded-md border p-2'
            value={teamId}
            onChange={(event) => {
              setTeamId(event.target.value)
              setDiscount(
                teams.data?.items.find(
                  (team) => String(team.id) === event.target.value
                )?.discount ?? ''
              )
            }}
          >
            <option value=''>{t('Select team')}</option>
            {teams.data?.items.map((team) => (
              <option
                key={team.id}
                value={String(team.id)}
                disabled={team.status !== 'active'}
              >
                {team.name} (#{team.id}) · {t('Main site')} · {t('Leader')} #
                {team.leader_user_id} ·{' '}
                {team.status === 'active' ? t('Active') : t('Closed')}
              </option>
            ))}
          </select>
          {teams.isError && <ErrorState onRetry={() => void teams.refetch()} />}
          <PageSwitcher
            page={page}
            total={teams.data?.total ?? 0}
            pageSize={20}
            onPageChange={(next) => {
              setPage(next)
              setTeamId('')
              setDiscount('')
            }}
          />
        </div>
        <div className='flex flex-col gap-1'>
          <Label htmlFor='main-team-discount'>
            {t('Post-discount payment ratio')}
          </Label>
          <Input
            id='main-team-discount'
            value={discount}
            inputMode='decimal'
            onChange={(event) => setDiscount(event.target.value)}
          />
          <p className='text-muted-foreground text-sm'>
            {t(
              '0.8 means pay 80% of the ordinary price; 1 means pay the ordinary (full) price.'
            )}
          </p>
        </div>
      </div>
      <Button
        type='button'
        variant='outline'
        disabled={save.isPending || !selected || discount.trim() === ''}
        onClick={() => save.mutate()}
      >
        {t('Save')}
      </Button>
    </section>
  )
}
