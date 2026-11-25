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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { Dialog } from '@/components/dialog'
import { sideDrawerContentClassName } from '@/components/drawer-layout'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { PasswordInput } from '@/components/password-input'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import type { SiteOwnerAction } from '@/features/auth/secure-verification'
import { DraftDiscardDialog } from '@/features/system-settings/components/draft-discard-dialog'
import { useDraftNavigation } from '@/features/system-settings/hooks/use-draft-navigation'
import { formatBonusRateFraction } from '@/features/wallet/adty/api'
import { ORDER_STATUS_LABELS } from '@/features/wallet/adty/lib/methods'
import { formatUsd } from '@/features/wallet/adty/lib/money'
import type { AdtyOrderStatus } from '@/features/wallet/adty/types'
import { WalletLotProjectionLines } from '@/features/wallet/components/wallet-lot-projection'
import { toIntlLocale } from '@/i18n/languages'
import { formatQuota, formatTimestamp } from '@/lib/format'
import { formatUserIdentity } from '@/lib/format-user-identity'
import { handleServerError } from '@/lib/handle-server-error'
import { accountPasswordSchema } from '@/lib/password-policy'

import {
  getOwnerCustomer,
  getOwnerCustomerLogs,
  getOwnerCustomerTopUps,
  reportCustomer,
  resetOwnerCustomerPassword,
  siteOwnerKeys,
  type OwnerCustomerPasswordResult,
} from '../api'
import { useOwnerVerification } from './owner-verification'

const RANDOM_PASSWORD_LENGTHS = [12, 16, 20, 24] as const
const CUSTOMER_PASSWORD_ACTION: SiteOwnerAction = 'customer.password'

/** Customer drawer: profile, requests, top-ups, keys, password management and report. */
export function CustomerSheet(props: {
  customerId: number | null
  onOpenChange: (open: boolean) => void
}) {
  const { t, i18n } = useTranslation()
  const queryClient = useQueryClient()
  const ownerVerification = useOwnerVerification()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const id = props.customerId ?? 0
  const [newPassword, setNewPassword] = useState('')
  const [randomLength, setRandomLength] = useState<number>(16)
  const [generatedResult, setGeneratedResult] =
    useState<OwnerCustomerPasswordResult | null>(null)
  const [reason, setReason] = useState('')
  const [discarding, setDiscarding] = useState(false)
  useEffect(() => {
    setNewPassword('')
    setRandomLength(16)
    setGeneratedResult(null)
    setReason('')
  }, [props.customerId])
  const dirty = newPassword !== '' || reason !== ''
  useDraftNavigation(dirty)
  const passwordCheck = accountPasswordSchema.safeParse(newPassword)
  const passwordInvalid = newPassword !== '' && !passwordCheck.success
  const detail = useQuery({
    queryKey: siteOwnerKeys.list('customer', { id }),
    queryFn: () => getOwnerCustomer(id),
    enabled: id > 0,
  })
  const setManualPassword = useMutation({
    mutationFn: async (password: string) => {
      const proof = await ownerVerification.verify(CUSTOMER_PASSWORD_ACTION, id)
      if (!proof) return null
      return resetOwnerCustomerPassword(id, { password }, proof)
    },
    onSuccess: (res) => {
      if (!res) return
      setNewPassword('')
      toast.success(
        t(
          'Password updated. All active customer sessions have been signed out.'
        )
      )
      void queryClient.invalidateQueries({ queryKey: siteOwnerKeys.all })
    },
    onError: (err) => handleServerError(err),
  })
  const resetRandomPassword = useMutation({
    mutationFn: async (length: number) => {
      const proof = await ownerVerification.verify(CUSTOMER_PASSWORD_ACTION, id)
      if (!proof) return null
      return resetOwnerCustomerPassword(id, { random: true, length }, proof)
    },
    onSuccess: (res) => {
      if (!res) return
      setGeneratedResult(res)
      toast.success(
        t(
          'Random password generated. All active customer sessions have been signed out.'
        )
      )
      void queryClient.invalidateQueries({ queryKey: siteOwnerKeys.all })
    },
    onError: (err) => handleServerError(err),
  })
  const isPasswordPending =
    setManualPassword.isPending || resetRandomPassword.isPending
  const logs = useQuery({
    queryKey: siteOwnerKeys.list('customer-logs', { id }),
    queryFn: () => getOwnerCustomerLogs(id, 1),
    enabled: id > 0,
    meta: { errorToast: false },
  })
  const topups = useQuery({
    queryKey: siteOwnerKeys.list('customer-topups', { id }),
    queryFn: () => getOwnerCustomerTopUps(id, 1),
    enabled: id > 0,
    meta: { errorToast: false },
  })
  const report = useMutation({
    mutationFn: () => reportCustomer(id, reason.trim()),
    onSuccess: () => {
      toast.success(t('Report sent to the platform'))
      setReason('')
    },
    onError: (err) => handleServerError(err),
  })
  const customer = detail.data?.customer

  return (
    <Sheet
      open={props.customerId !== null}
      onOpenChange={(open) => {
        if (!open && dirty) setDiscarding(true)
        else {
          if (!open) setGeneratedResult(null)
          props.onOpenChange(open)
        }
      }}
    >
      <SheetContent className={sideDrawerContentClassName('sm:max-w-2xl')}>
        <SheetHeader>
          <SheetTitle className='tabular-nums'>
            {customer
              ? formatUserIdentity(customer.id, customer.username)
              : t('Customer')}
          </SheetTitle>
          <SheetDescription>{customer?.email}</SheetDescription>
        </SheetHeader>
        {detail.isError && <ErrorState onRetry={() => void detail.refetch()} />}
        {detail.isPending && <LoadingState />}
        {customer && (
          <div className='flex flex-col gap-4 px-4 pb-6'>
            <dl className='grid grid-cols-2 gap-2 text-sm'>
              <dt className='text-muted-foreground'>{t('Balance')}</dt>
              <dd>
                {customer.balance === undefined
                  ? t('Hidden by the platform')
                  : formatQuota(customer.balance)}
                {customer.balance !== undefined && (
                  <WalletLotProjectionLines
                    projection={customer.wallet_lot_projection}
                  />
                )}
              </dd>
              <dt className='text-muted-foreground'>{t('Used')}</dt>
              <dd>
                {customer.used_quota === undefined
                  ? t('Hidden by the platform')
                  : formatQuota(customer.used_quota)}
              </dd>
              <dt className='text-muted-foreground'>{t('Signed up')}</dt>
              <dd>{formatTimestamp(customer.created_at)}</dd>
            </dl>
            <Tabs defaultValue='logs'>
              <TabsList>
                <TabsTrigger value='logs'>{t('Requests')}</TabsTrigger>
                <TabsTrigger value='topups'>{t('Top-ups')}</TabsTrigger>
                <TabsTrigger value='tokens'>{t('API keys')}</TabsTrigger>
              </TabsList>
              <TabsContent value='logs'>
                {logs.isError ? (
                  <ErrorState
                    className='min-h-24'
                    onRetry={() => void logs.refetch()}
                  />
                ) : (
                  <StaticDataTable
                    tableProps={{ 'aria-label': t('Requests') }}
                    data={logs.data?.items ?? []}
                    getRowKey={(row) => String(row.id)}
                    emptyContent={
                      <EmptyState
                        className='min-h-24'
                        title={t('No requests yet')}
                      />
                    }
                    columns={[
                      {
                        id: 'time',
                        header: t('Time'),
                        cell: (row) => formatTimestamp(row.created_at),
                      },
                      {
                        id: 'model',
                        header: t('Model'),
                        cell: (row) => row.model_name,
                      },
                      {
                        id: 'tokens',
                        header: t('Tokens'),
                        cell: (row) =>
                          `${row.prompt_tokens} / ${row.completion_tokens}`,
                      },
                      {
                        id: 'quota',
                        header: t('Charged'),
                        cell: (row) => formatQuota(row.quota),
                      },
                    ]}
                  />
                )}
              </TabsContent>
              <TabsContent value='topups'>
                {topups.isError ? (
                  <ErrorState
                    className='min-h-24'
                    onRetry={() => void topups.refetch()}
                  />
                ) : (
                  <StaticDataTable
                    tableProps={{ 'aria-label': t('Top-ups') }}
                    data={topups.data?.items ?? []}
                    getRowKey={(row) => row.trade_no}
                    emptyContent={
                      <EmptyState
                        className='min-h-24'
                        title={t('No top-ups yet')}
                      />
                    }
                    columns={[
                      {
                        id: 'time',
                        header: t('Time'),
                        cell: (row) => formatTimestamp(row.created_at),
                      },
                      {
                        id: 'face',
                        header: t('Amount'),
                        cell: (row) => formatUsd(Number(row.face_usd), locale),
                      },
                      {
                        id: 'bonus-rate',
                        header: t('Bonus rate'),
                        cell: (row) => {
                          const percent = formatBonusRateFraction(
                            row.bonus_rate
                          )
                          return (
                            <span className='tabular-nums'>
                              {percent ? `${percent}%` : '0%'}
                            </span>
                          )
                        },
                      },
                      {
                        id: 'bonus',
                        header: t('Bonus'),
                        cell: (row) => formatUsd(Number(row.bonus_usd), locale),
                      },
                      {
                        id: 'status',
                        header: t('Status'),
                        cell: (row) =>
                          t(
                            ORDER_STATUS_LABELS[
                              row.status as AdtyOrderStatus
                            ] ?? row.status
                          ),
                      },
                    ]}
                  />
                )}
              </TabsContent>
              <TabsContent value='tokens'>
                {detail.data?.tokens === undefined ? (
                  <EmptyState
                    className='min-h-24'
                    title={t('The platform does not show this information')}
                  />
                ) : (
                  <StaticDataTable
                    tableProps={{ 'aria-label': t('API keys') }}
                    data={detail.data.tokens}
                    getRowKey={(row) => String(row.id)}
                    emptyContent={
                      <EmptyState
                        className='min-h-24'
                        title={t('No API keys yet')}
                      />
                    }
                    columns={[
                      {
                        id: 'name',
                        header: t('Name'),
                        cell: (row) => row.name,
                      },
                      {
                        id: 'used',
                        header: t('Last used'),
                        cell: (row) =>
                          row.accessed_time > 0
                            ? formatTimestamp(row.accessed_time)
                            : '—',
                      },
                    ]}
                  />
                )}
              </TabsContent>
            </Tabs>
            <section
              className='space-y-4 rounded-lg border p-4'
              aria-labelledby='customer-password-title'
            >
              <div className='space-y-1'>
                <h3 id='customer-password-title' className='text-sm font-medium'>
                  {t('Password Management')}
                </h3>
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Set a new password manually or generate a one-time random password. Changing the password immediately signs the customer out of all active sessions.'
                  )}
                </p>
              </div>
              <form
                className='space-y-2'
                onSubmit={(event) => {
                  event.preventDefault()
                  if (!passwordCheck.success || isPasswordPending) return
                  setManualPassword.mutate(newPassword)
                }}
              >
                <div className='space-y-1'>
                  <Label htmlFor='owner-customer-password'>
                    {t('New password')}
                  </Label>
                  <div className='flex flex-wrap items-start gap-2'>
                    <div className='min-w-56 flex-1'>
                      <PasswordInput
                        id='owner-customer-password'
                        autoComplete='new-password'
                        value={newPassword}
                        placeholder={t('Enter new password (8-128 characters)')}
                        aria-invalid={passwordInvalid}
                        disabled={isPasswordPending}
                        onChange={(event) => setNewPassword(event.target.value)}
                      />
                    </div>
                    <Button
                      type='submit'
                      variant='brand'
                      disabled={
                        isPasswordPending ||
                        newPassword === '' ||
                        !passwordCheck.success
                      }
                    >
                      {t('Save Password')}
                    </Button>
                  </div>
                  {passwordInvalid && (
                    <p role='alert' className='text-destructive text-xs'>
                      {t('Password must contain between 8 and 128 characters.')}
                    </p>
                  )}
                </div>
              </form>
              <div className='space-y-2 border-t pt-3'>
                <div className='flex flex-wrap items-end gap-2'>
                  <div className='space-y-1'>
                    <Label htmlFor='owner-customer-random-length'>
                      {t('Password length')}
                    </Label>
                    <NativeSelect
                      id='owner-customer-random-length'
                      value={String(randomLength)}
                      disabled={isPasswordPending}
                      onChange={(event) =>
                        setRandomLength(Number(event.target.value))
                      }
                    >
                      {RANDOM_PASSWORD_LENGTHS.map((len) => (
                        <NativeSelectOption key={len} value={String(len)}>
                          {t('{{count}} characters', { count: len })}
                        </NativeSelectOption>
                      ))}
                    </NativeSelect>
                  </div>
                  <Button
                    type='button'
                    variant='outline'
                    disabled={isPasswordPending}
                    onClick={() => resetRandomPassword.mutate(randomLength)}
                  >
                    {t('Reset Random Password')}
                  </Button>
                </div>
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Includes uppercase letters, lowercase letters, and digits.'
                  )}
                </p>
              </div>
            </section>
            <section className='space-y-2' aria-labelledby='report-title'>
              <h3 id='report-title' className='text-sm font-medium'>
                {t('Report this customer')}
              </h3>
              <Textarea
                aria-labelledby='report-title'
                maxLength={1000}
                value={reason}
                onChange={(event) => setReason(event.target.value)}
              />
              <Button
                type='button'
                variant='outline'
                disabled={!reason.trim() || report.isPending}
                onClick={() => report.mutate()}
              >
                {t('Send report')}
              </Button>
            </section>
          </div>
        )}
      </SheetContent>
      <Dialog
        open={generatedResult !== null}
        onOpenChange={(open) => {
          if (!open) setGeneratedResult(null)
        }}
        title={t('New Random Password')}
        description={
          generatedResult
            ? formatUserIdentity(
                generatedResult.user_id,
                generatedResult.username
              )
            : undefined
        }
        footer={
          <Button
            type='button'
            variant='brand'
            onClick={() => setGeneratedResult(null)}
          >
            {t('I have copied it')}
          </Button>
        }
      >
        <div className='flex flex-col gap-3'>
          <Alert>
            <AlertDescription>
              {t(
                'This password is shown only once and cannot be retrieved after closing this dialog. Copy it now and share it securely with the customer. All existing sessions for this customer have been signed out.'
              )}
            </AlertDescription>
          </Alert>
          {generatedResult?.generated_password && (
            <div className='bg-muted flex items-center justify-between gap-2 rounded-md border px-3 py-2'>
              <code className='font-mono text-sm break-all select-all'>
                {generatedResult.generated_password}
              </code>
              <CopyButton
                value={generatedResult.generated_password}
                variant='outline'
                size='sm'
                aria-label={t('Copy password')}
              >
                {t('Copy')}
              </CopyButton>
            </div>
          )}
        </div>
      </Dialog>
      <DraftDiscardDialog
        open={discarding}
        onOpenChange={setDiscarding}
        onDiscard={() => {
          setNewPassword('')
          setGeneratedResult(null)
          setReason('')
          props.onOpenChange(false)
        }}
      />
    </Sheet>
  )
}
