import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  NativeSelect,
  NativeSelectOption,
} from '@/components/ui/native-select'
import { validTeamDiscount } from '@/features/adty-team-registration/discount'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { searchUsers } from '@/features/users/api'
import { api } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

export function CreateTeamForm() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [leader, setLeader] = useState('')
  const [name, setName] = useState('')
  const [discount, setDiscount] = useState('')
  const nameTooLong = [...name.trim()].length > 64
  const discountTrimmed = discount.trim()
  const discountInvalid =
    discountTrimmed !== '' && !validTeamDiscount(discountTrimmed)
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(1)
  const attempt = useRef({ body: '', key: '' })
  const candidates = useQuery({
    queryKey: ['team-leader-candidates', 0, search, page],
    queryFn: async () =>
      requireServerSuccess(
        await searchUsers({
          site_id: 0,
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
        site_id: 0,
        leader_id: Number(leader),
        name: name.trim(),
        discount: discountTrimmed,
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
      setDiscount('')
      attempt.current = { body: '', key: '' }
      void client.invalidateQueries({ queryKey: ['adty-team-admin'] })
    },
    onError: (error) => handleServerError(error),
  })
  return (
    <section className='flex flex-col gap-3'>
      <div className='flex flex-col gap-1'>
        <h2 className='text-sm font-medium'>{t('Create team')}</h2>
        <p className='text-muted-foreground text-xs'>
          {t('Teams created here belong to the main site.')}
        </p>
      </div>
      <div className='flex flex-col gap-1.5'>
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
      </div>
      <div className='flex flex-col gap-1.5'>
        <Label htmlFor='new-team-leader'>{t('Leader')}</Label>
        <NativeSelect
          id='new-team-leader'
          required
          value={leader}
          onChange={(event) => setLeader(event.target.value)}
        >
          <NativeSelectOption value=''>{t('Select user')}</NativeSelectOption>
          {candidates.data?.items.map((user) => (
            <NativeSelectOption key={user.id} value={user.id}>
              {user.username} (#{user.id})
            </NativeSelectOption>
          ))}
        </NativeSelect>
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
      </div>
      <div className='flex flex-col gap-1.5'>
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
          <p
            id='new-team-name-error'
            role='alert'
            className='text-destructive text-xs'
          >
            {t('Team name must contain 1 to 64 characters')}
          </p>
        )}
      </div>
      <div className='flex flex-col gap-1.5'>
        <Label htmlFor='new-team-discount'>{t('Team supply discount')}</Label>
        <Input
          id='new-team-discount'
          inputMode='decimal'
          placeholder='1'
          aria-invalid={discountInvalid}
          aria-describedby={
            discountInvalid
              ? 'new-team-discount-error'
              : 'new-team-discount-hint'
          }
          value={discount}
          onChange={(event) => setDiscount(event.target.value)}
        />
        {discountInvalid ? (
          <p
            id='new-team-discount-error'
            role='alert'
            className='text-destructive text-xs'
          >
            {t(
              'Supply discount must be between 0 and 1 with up to 4 decimal places.'
            )}
          </p>
        ) : (
          <p
            id='new-team-discount-hint'
            className='text-muted-foreground text-xs'
          >
            {t('Range (0, 1], up to 4 decimals. Leave blank for 1 (no discount).')}
          </p>
        )}
      </div>
      <div>
        <Button
          type='button'
          onClick={() => create.mutate()}
          disabled={
            create.isPending ||
            !leader ||
            !name.trim() ||
            nameTooLong ||
            discountInvalid
          }
        >
          {t('Create team')}
        </Button>
      </div>
      {create.data && (
        <p role='status' className='text-sm'>
          {t('Team created')} · {create.data.name} (#{create.data.id})
        </p>
      )}
    </section>
  )
}
