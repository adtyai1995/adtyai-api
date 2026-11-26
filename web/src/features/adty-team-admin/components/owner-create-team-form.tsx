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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  getOwnerCustomers,
  lookupOwnerCustomer,
  OWNER_PAGE_SIZE,
} from '@/features/adty-site-owner/api'
import type { OwnerCustomer } from '@/features/adty-site-owner/types'
import { validTeamDiscount } from '@/features/adty-team-registration/discount'
import { PageSwitcher } from '@/features/referral/components/page-switcher'
import { SettingsSection } from '@/features/system-settings/components/settings-section'
import { api } from '@/lib/api'
import { formatUserIdentity } from '@/lib/format-user-identity'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

export function OwnerCreateTeamForm(props: { closing?: boolean }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const closing = Boolean(props.closing)
  const [leader, setLeader] = useState('')
  const [name, setName] = useState('')
  const [discount, setDiscount] = useState('')
  const [lookupInput, setLookupInput] = useState('')
  const [lookedUp, setLookedUp] = useState<OwnerCustomer | null>(null)
  const [page, setPage] = useState(1)
  const attempt = useRef({ body: '', key: '' })

  const nameTooLong = [...name.trim()].length > 64
  const discountTrimmed = discount.trim()
  const discountInvalid =
    discountTrimmed !== '' && !validTeamDiscount(discountTrimmed, true)

  const candidates = useQuery({
    queryKey: ['owner-team-leader-candidates', page],
    queryFn: () => getOwnerCustomers(page),
  })

  const lookup = useMutation({
    mutationFn: (q: string) => lookupOwnerCustomer(q),
    onSuccess: (customer) => {
      setLookedUp(customer)
      setLeader(String(customer.id))
    },
    onError: (error) => handleServerError(error),
  })

  const create = useMutation({
    mutationFn: async () => {
      const params = {
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
      }>('/api/adty/site/teams', params, {
        headers: { 'Idempotency-Key': attempt.current.key },
      })
      return requireServerSuccess(data).data.Team
    },
    onSuccess: () => {
      setName('')
      setLeader('')
      setDiscount('')
      setLookedUp(null)
      setLookupInput('')
      attempt.current = { body: '', key: '' }
      void client.invalidateQueries({ queryKey: ['adty-team-admin'] })
    },
    onError: (error) => handleServerError(error),
  })

  const pageItems = candidates.data?.items ?? []
  const options: OwnerCustomer[] =
    lookedUp && !pageItems.some((item) => item.id === lookedUp.id)
      ? [lookedUp, ...pageItems]
      : pageItems

  return (
    <SettingsSection
      title={t('Create team')}
      description={t('Teams created here belong to this subsite.')}
    >
      <div className='flex max-w-2xl flex-col gap-5'>
        <div className='grid gap-4 sm:grid-cols-2'>
          <div className='flex flex-col gap-1.5'>
            <Label htmlFor='owner-new-team-leader-lookup'>
              {t('Exact lookup (username or email)')}
            </Label>
            <div className='flex gap-2'>
              <Input
                id='owner-new-team-leader-lookup'
                className='max-w-sm'
                disabled={closing || lookup.isPending}
                value={lookupInput}
                onChange={(event) => setLookupInput(event.target.value)}
              />
              <Button
                type='button'
                variant='outline'
                disabled={closing || lookup.isPending || !lookupInput.trim()}
                onClick={() => lookup.mutate(lookupInput.trim())}
              >
                {t('Lookup')}
              </Button>
            </div>
          </div>
          <div className='flex flex-col gap-1.5'>
            <Label htmlFor='owner-new-team-leader'>{t('Leader')}</Label>
            <NativeSelect
              id='owner-new-team-leader'
              className='w-full max-w-sm'
              disabled={closing}
              required
              value={leader}
              onChange={(event) => setLeader(event.target.value)}
            >
              <NativeSelectOption value=''>
                {t('Select user')}
              </NativeSelectOption>
              {options
                .filter((user) => user.status === 1)
                .map((user) => (
                  <NativeSelectOption key={user.id} value={user.id}>
                    {formatUserIdentity(user.id, user.username)}
                  </NativeSelectOption>
                ))}
            </NativeSelect>
            {candidates.isError && (
              <ErrorState onRetry={() => void candidates.refetch()} />
            )}
            <div className='pt-0.5'>
              <PageSwitcher
                page={page}
                pageSize={OWNER_PAGE_SIZE}
                total={candidates.data?.total ?? 0}
                onPageChange={(next) => {
                  setLeader('')
                  setPage(next)
                }}
              />
            </div>
          </div>
        </div>
        <div className='grid gap-4 sm:grid-cols-2'>
          <div className='flex flex-col gap-1.5'>
            <Label htmlFor='owner-new-team-name'>{t('Team name')}</Label>
            <Input
              id='owner-new-team-name'
              className='max-w-sm'
              disabled={closing}
              aria-invalid={nameTooLong}
              aria-describedby={
                nameTooLong ? 'owner-new-team-name-error' : undefined
              }
              required
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
            {nameTooLong && (
              <p
                id='owner-new-team-name-error'
                role='alert'
                className='text-destructive text-xs'
              >
                {t('Team name must contain 1 to 64 characters')}
              </p>
            )}
          </div>
          <div className='flex flex-col gap-1.5'>
            <Label htmlFor='owner-new-team-discount'>
              {t('Supply Price Multiplier')}
            </Label>
            <Input
              id='owner-new-team-discount'
              className='sm:max-w-44'
              inputMode='decimal'
              placeholder='1'
              disabled={closing}
              aria-invalid={discountInvalid}
              aria-describedby={
                discountInvalid
                  ? 'owner-new-team-discount-error'
                  : 'owner-new-team-discount-hint'
              }
              value={discount}
              onChange={(event) => setDiscount(event.target.value)}
            />
            {discountInvalid ? (
              <p
                id='owner-new-team-discount-error'
                role='alert'
                className='text-destructive text-xs'
              >
                {t(
                  'Team supply price multiplier must be between 1.0 and 10.0 (up to 4 decimal places).'
                )}
              </p>
            ) : (
              <p
                id='owner-new-team-discount-hint'
                className='text-dim text-[12.5px] leading-relaxed'
              >
                {t(
                  'Range [1, 10], up to 4 decimals. Leave blank for 1 (supply price).'
                )}
              </p>
            )}
          </div>
        </div>
        <div>
          <Button
            type='button'
            variant='brand'
            onClick={() => create.mutate()}
            disabled={
              closing ||
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
      </div>
    </SettingsSection>
  )
}
