import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { getUserSites, searchUsers } from '@/features/users/api'
import { api } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

export function CreateTeamForm() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [site, setSite] = useState(0)
  const [leader, setLeader] = useState('')
  const [name, setName] = useState('')
  const nameTooLong = [...name.trim()].length > 64
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(1)
  const attempt = useRef({ body: '', key: '' })
  const sites = useQuery({
    queryKey: ['team-leader-sites'],
    queryFn: async () => requireServerSuccess(await getUserSites()).data ?? [],
  })
  const candidates = useQuery({
    queryKey: ['team-leader-candidates', site, search, page],
    queryFn: async () =>
      requireServerSuccess(
        await searchUsers({
          site_id: site,
          keyword: search,
          role: '1',
          status: '1',
          p: page,
          page_size: 20,
        })
      ).data,
  })
  const create = useMutation({
    mutationFn: async () => {
      const params = {
        site_id: site,
        leader_id: Number(leader),
        name: name.trim(),
      }
      const body = JSON.stringify(params)
      if (attempt.current.body !== body) {
        attempt.current = { body, key: crypto.randomUUID() }
      }
      const { data } = await api.post<{
        success: boolean
        data: { Team: { id: number; name: string } }
      }>('/api/adty/admin/teams', params, {
        headers: { 'Idempotency-Key': attempt.current.key },
      })
      return requireServerSuccess(data).data.Team
    },
    onSuccess: () => {
      setName('')
      setLeader('')
      attempt.current = { body: '', key: '' }
      void client.invalidateQueries({ queryKey: ['adty-team-admin'] })
    },
    onError: (error) => handleServerError(error),
  })
  return (
    <section className='flex flex-col gap-2'>
      <h2 className='text-sm font-medium'>{t('Create team')}</h2>
      <Label htmlFor='new-team-site'>{t('Site')}</Label>
      <select
        id='new-team-site'
        className='rounded-md border p-2'
        value={site}
        onChange={(event) => {
          setSite(Number(event.target.value))
          setPage(1)
          setLeader('')
          setSearch('')
        }}
      >
        <option value={0}>{t('Main site')}</option>
        {sites.data
          ?.filter((item) => item.site_id > 0)
          .map((item) => (
            <option key={item.site_id} value={item.site_id}>
              {item.site_name} (#{item.site_id})
            </option>
          ))}
      </select>
      {sites.isError && <ErrorState onRetry={() => void sites.refetch()} />}
      <Label htmlFor='new-team-leader-search'>{t('Search users')}</Label>
      <Input
        id='new-team-leader-search'
        value={search}
        onChange={(event) => {
          setSearch(event.target.value)
          setPage(1)
          setLeader('')
        }}
      />
      <Label htmlFor='new-team-leader'>{t('Leader')}</Label>
      <select
        id='new-team-leader'
        required
        className='rounded-md border p-2'
        value={leader}
        onChange={(event) => setLeader(event.target.value)}
      >
        <option value=''>{t('Select user')}</option>
        {candidates.data?.items.map((user) => (
          <option key={user.id} value={user.id}>
            {user.username} (#{user.id})
          </option>
        ))}
      </select>
      {candidates.isError && (
        <ErrorState onRetry={() => void candidates.refetch()} />
      )}
      <PageSwitcher
        page={page}
        pageSize={20}
        total={candidates.data?.total ?? 0}
        onPageChange={(next) => {
          setLeader('')
          setPage(next)
        }}
      />
      <Label htmlFor='new-team-name'>{t('Team name')}</Label>
      <Input
        id='new-team-name'
        aria-invalid={nameTooLong}
        aria-describedby={nameTooLong ? 'new-team-name-error' : undefined}
        required
        value={name}
        onChange={(event) => setName(event.target.value)}
      />
      {nameTooLong && (
        <p id='new-team-name-error' role='alert'>
          {t('Team name must contain 1 to 64 characters')}
        </p>
      )}
      <Button
        type='button'
        onClick={() => create.mutate()}
        disabled={create.isPending || !leader || !name.trim() || nameTooLong}
      >
        {t('Create team')}
      </Button>
      {create.data && (
        <p role='status'>
          {t('Team created')} · {create.data.name} (#{create.data.id})
        </p>
      )}
    </section>
  )
}
