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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useLocation, useNavigate } from '@tanstack/react-router'
import { Code2, Eye, ShieldAlert } from 'lucide-react'
import * as React from 'react'
import { useForm, type Resolver } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { ErrorState } from '@/components/error-state'
import { JsonCodeEditor } from '@/components/json-code-editor'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { PaymentChannels } from '@/features/adty-payment-channels'
import { PaymentSceneManager } from '@/features/adty-payment-scenes'
import { api } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import { SettingsForm } from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import { safeNumberFieldProps } from '../utils/numeric-field'
import {
  AdtyEpaySettingsSection,
  type AdtyEpaySettingsDefaults,
} from './adty-epay-settings-section'
import { AmountOptionsVisualEditor } from './amount-options-visual-editor'
import {
  BEpusdtSettingsSection,
  type BEpusdtSettingsDefaults,
} from './bepusdt-settings-section'
import {
  formatJsonForEditor,
  getJsonError,
  normalizeJsonForComparison,
  removeTrailingSlash,
} from './utils'

function isHttpOriginUrl(value: string) {
  const trimmed = value.trim()
  if (!trimmed) return true

  try {
    const url = new URL(trimmed)
    const isHttpProtocol = url.protocol === 'http:' || url.protocol === 'https:'
    const hasNoPath = url.pathname === '' || url.pathname === '/'
    return isHttpProtocol && hasNoPath && !url.search && !url.hash
  } catch {
    return false
  }
}

const paymentSchema = z.object({
  Price: z.coerce.number().min(0),
  MinTopUp: z.coerce.number().min(0),
  CustomCallbackAddress: z
    .string()
    .refine(
      isHttpOriginUrl,
      'Enter only a top-level callback domain, for example https://api.example.com, without any path.'
    ),
  AmountOptions: z.string().superRefine((value, ctx) => {
    const error = getJsonError(value, (parsed) => Array.isArray(parsed))
    if (error) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: error,
      })
    }
  }),
})

type PaymentFormValues = z.infer<typeof paymentSchema>
const providerSchemas = {
  general: paymentSchema.pick({
    Price: true,
    MinTopUp: true,
    AmountOptions: true,
  }),
  epay: paymentSchema.pick({
    CustomCallbackAddress: true,
  }),
}

type MainBonusMethodRule = {
  enabled: boolean
  rate: string
}

type MainBonusMethodsMap = Record<string, MainBonusMethodRule>

const MAIN_BONUS_METHODS = [
  { key: 'alipay', labelKey: 'Alipay' },
  { key: 'wxpay', labelKey: 'WeChat Pay' },
  { key: 'usdt', labelKey: 'USDT' },
] as const

function isValidBonusRateDecimal(raw: string): boolean {
  const trimmed = raw.trim()
  if (!/^(?:0(?:\.\d+)?|1(?:\.0+)?)$/.test(trimmed)) {
    return false
  }
  const num = Number(trimmed)
  return Number.isFinite(num) && num >= 0 && num <= 1
}

function formatBonusRatePercent(raw: string): string {
  const trimmed = raw.trim()
  if (!isValidBonusRateDecimal(trimmed)) return ''
  const pct = Number((Number(trimmed) * 100).toFixed(4))
  return `= ${pct}%`
}

function MainBonusMethodsPanel() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [drafts, setDrafts] = React.useState<MainBonusMethodsMap>({})
  const savedRef = React.useRef<MainBonusMethodsMap | null>(null)
  const [savingMethod, setSavingMethod] = React.useState<string | null>(null)

  const query = useQuery({
    queryKey: ['adty-admin-bonus-methods'],
    queryFn: async () => {
      const res = await api.get<{
        success: boolean
        message?: string
        data?: MainBonusMethodsMap
      }>('/api/adty/admin/bonus-methods')
      return requireServerSuccess(res.data).data ?? {}
    },
  })

  React.useEffect(() => {
    if (!query.data) return
    const incoming: MainBonusMethodsMap = {}
    for (const item of MAIN_BONUS_METHODS) {
      const remote = query.data?.[item.key]
      incoming[item.key] = {
        enabled: Boolean(remote?.enabled),
        rate: remote?.rate ?? '',
      }
    }
    const prevSaved = savedRef.current
    if (!prevSaved) {
      savedRef.current = incoming
      setDrafts(incoming)
      return
    }
    setDrafts((prev) => {
      const next: MainBonusMethodsMap = { ...prev }
      for (const item of MAIN_BONUS_METHODS) {
        const cur = prev[item.key]
        const base = prevSaved[item.key]
        const isClean =
          !cur ||
          !base ||
          (cur.enabled === base.enabled && cur.rate === base.rate)
        if (isClean) {
          next[item.key] = incoming[item.key]
        }
      }
      return next
    })
    savedRef.current = incoming
  }, [query.data])

  const mutation = useMutation({
    mutationFn: async (payload: {
      method: string
      enabled: boolean
      rate: string
    }) => {
      const res = await api.put<{
        success: boolean
        message?: string
        data?: MainBonusMethodsMap
      }>('/api/adty/admin/bonus-methods', payload)
      return requireServerSuccess(res.data).data ?? {}
    },
    onSuccess: (updated, variables) => {
      const remote = updated[variables.method]
      const savedEntry = {
        enabled: Boolean(remote?.enabled),
        rate: remote?.rate ?? '',
      }
      if (savedRef.current) {
        savedRef.current = {
          ...savedRef.current,
          [variables.method]: savedEntry,
        }
      }
      setDrafts((prev) => ({
        ...prev,
        [variables.method]: savedEntry,
      }))
      queryClient.setQueryData(['adty-admin-bonus-methods'], updated)
      toast.success(t('Saved'))
    },
    onError: (error) => handleServerError(error),
    onSettled: () => setSavingMethod(null),
  })

  const handleSave = (methodKey: string) => {
    const draft = drafts[methodKey] ?? { enabled: false, rate: '' }
    const trimmed = draft.rate.trim()
    if (draft.enabled) {
      if (!isValidBonusRateDecimal(trimmed)) {
        toast.error(t('Enter a decimal rate between 0 and 1 (e.g. 0.025)'))
        return
      }
    } else if (trimmed !== '' && !isValidBonusRateDecimal(trimmed)) {
      toast.error(t('Enter a decimal rate between 0 and 1 (e.g. 0.025)'))
      return
    }
    setSavingMethod(methodKey)
    mutation.mutate({
      method: methodKey,
      enabled: draft.enabled,
      rate: trimmed || '0',
    })
  }

  let bodyContent: React.ReactNode
  if (query.isPending) {
    bodyContent = (
      <div className='space-y-2'>
        <Skeleton className='h-10 w-full' />
        <Skeleton className='h-10 w-full' />
        <Skeleton className='h-10 w-full' />
      </div>
    )
  } else if (query.isError) {
    bodyContent = (
      <ErrorState
        className='min-h-28'
        title={t('Failed to load')}
        onRetry={() => void query.refetch()}
      />
    )
  } else {
    bodyContent = MAIN_BONUS_METHODS.map((item) => {
      const draft = drafts[item.key] ?? { enabled: false, rate: '' }
      const percentHint = formatBonusRatePercent(draft.rate)
      const isSavingThis = savingMethod === item.key && mutation.isPending
      return (
        <div
          key={item.key}
          className='flex flex-col gap-2 rounded-md border p-3 sm:flex-row sm:items-center sm:justify-between'
        >
          <div className='flex items-center gap-3'>
            <Switch
              id={`main-bonus-switch-${item.key}`}
              checked={draft.enabled}
              onCheckedChange={(checked) =>
                setDrafts((prev) => ({
                  ...prev,
                  [item.key]: {
                    enabled: checked,
                    rate: prev[item.key]?.rate ?? '',
                  },
                }))
              }
            />
            <Label
              htmlFor={`main-bonus-switch-${item.key}`}
              className='min-w-20 font-medium'
            >
              {t(item.labelKey)}
            </Label>
          </div>
          <div className='flex flex-wrap items-center gap-2'>
            <div className='flex items-center gap-2'>
              <Input
                type='text'
                inputMode='decimal'
                aria-label={`${t(item.labelKey)} ${t('Bonus rate')}`}
                placeholder='0.025'
                className='w-32 font-mono text-xs'
                value={draft.rate}
                onChange={(event) =>
                  setDrafts((prev) => ({
                    ...prev,
                    [item.key]: {
                      enabled: prev[item.key]?.enabled ?? false,
                      rate: event.target.value,
                    },
                  }))
                }
              />
              {percentHint && (
                <span className='text-muted-foreground min-w-16 font-mono text-xs'>
                  {percentHint}
                </span>
              )}
            </div>
            <Button
              type='button'
              size='sm'
              variant='outline'
              disabled={isSavingThis}
              onClick={() => handleSave(item.key)}
            >
              {t('Save')}
            </Button>
          </div>
        </div>
      )
    })
  }

  return (
    <Card className='shadow-none'>
      <CardHeader className='pb-3'>
        <CardTitle className='text-base'>
          {t('Payment method top-up bonus')}
        </CardTitle>
        <CardDescription>
          {t(
            'Configure bonus rate (0 to 1, e.g. 0.025 for 2.5%) credited per payment method on the main site. Bonus earns no commission.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-3'>{bodyContent}</CardContent>
    </Card>
  )
}
type PaymentTab = keyof typeof providerSchemas | 'usdt'
const PAYMENT_TABS = ['general', 'epay', 'usdt'] as const

function isPaymentTab(tab: string): tab is PaymentTab {
  return (PAYMENT_TABS as readonly string[]).includes(tab)
}

function paymentTabSchema(tab: PaymentTab) {
  return providerSchemas[tab === 'usdt' ? 'general' : tab]
}

const paymentTabContentClassName = 'mt-6 min-w-0'

type PaymentSettingsSectionProps = {
  defaultValues: PaymentFormValues
  epayDefaultValues: AdtyEpaySettingsDefaults
  usdtDefaultValues: BEpusdtSettingsDefaults
}

export function PaymentSettingsSection({
  defaultValues,
  epayDefaultValues,
  usdtDefaultValues,
}: PaymentSettingsSectionProps) {
  const { t } = useTranslation()
  const location = useLocation()
  const navigate = useNavigate({ from: '/system-settings/billing/$section' })
  const initialTab = React.useMemo(() => {
    const tab = new URLSearchParams(location.searchStr).get('tab')
    return tab && isPaymentTab(tab) ? tab : 'general'
  }, [location.searchStr])
  const [activeTab, setActiveTab] = React.useState<PaymentTab>(initialTab)
  const updateOption = useUpdateOption()
  const initialRef = React.useRef(defaultValues)
  const defaultsSignature = React.useMemo(
    () => JSON.stringify(defaultValues),
    [defaultValues]
  )

  const [amountOptionsVisualMode, setAmountOptionsVisualMode] =
    React.useState(true)

  React.useEffect(() => {
    setActiveTab(initialTab)
  }, [initialTab])

  const form = useForm<PaymentFormValues>({
    resolver: zodResolver(
      paymentTabSchema(activeTab).passthrough()
    ) as unknown as Resolver<PaymentFormValues>,
    mode: 'onChange', // Enable real-time validation
    defaultValues: {
      ...defaultValues,
      AmountOptions: formatJsonForEditor(defaultValues.AmountOptions),
    },
  })

  const { isSubmitting } = form.formState

  React.useEffect(() => {
    const parsedDefaults = JSON.parse(defaultsSignature) as PaymentFormValues
    initialRef.current = parsedDefaults
    form.reset(
      {
        ...parsedDefaults,
        AmountOptions: formatJsonForEditor(parsedDefaults.AmountOptions),
      },
      { keepDirtyValues: true }
    )
  }, [defaultsSignature, form])

  const onSubmit = async (values: PaymentFormValues) => {
    const sanitized = {
      Price: values.Price,
      MinTopUp: values.MinTopUp,
      CustomCallbackAddress: removeTrailingSlash(values.CustomCallbackAddress),
      AmountOptions: values.AmountOptions.trim(),
    }

    const initial = {
      Price: initialRef.current.Price,
      MinTopUp: initialRef.current.MinTopUp,
      CustomCallbackAddress: removeTrailingSlash(
        initialRef.current.CustomCallbackAddress
      ),
      AmountOptions: initialRef.current.AmountOptions.trim(),
    }

    const updates: Array<{ key: string; value: string | number | boolean }> = []

    if (sanitized.Price !== initial.Price) {
      updates.push({ key: 'Price', value: sanitized.Price })
    }

    if (sanitized.MinTopUp !== initial.MinTopUp) {
      updates.push({ key: 'MinTopUp', value: sanitized.MinTopUp })
    }

    if (sanitized.CustomCallbackAddress !== initial.CustomCallbackAddress) {
      updates.push({
        key: 'CustomCallbackAddress',
        value: sanitized.CustomCallbackAddress,
      })
    }

    if (
      normalizeJsonForComparison(sanitized.AmountOptions) !==
      normalizeJsonForComparison(initial.AmountOptions)
    ) {
      updates.push({
        key: 'payment_setting.amount_options',
        value: sanitized.AmountOptions,
      })
    }

    const fields = paymentTabSchema(activeTab).shape
    const tabUpdates = updates.filter((update) => {
      if (update.key === 'payment_setting.amount_options') {
        return activeTab === 'general'
      }
      return update.key in fields
    })

    if (tabUpdates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }

    for (const update of tabUpdates) {
      await updateOption.mutateAsync(update)
      let field = update.key
      if (update.key === 'payment_setting.amount_options') {
        field = 'AmountOptions'
      }
      if (field in initialRef.current) {
        initialRef.current[field as keyof PaymentFormValues] =
          update.value as never
      }
    }
  }

  return (
    <SettingsSection title={t('Payment Gateway')}>
      <Form {...form}>
        <SettingsForm
          onSubmit={form.handleSubmit(onSubmit)}
          className='gap-y-8'
          data-no-autosubmit='true'
        >
          {activeTab !== 'usdt' && (
            <SettingsPageFormActions
              onSave={form.handleSubmit(onSubmit)}
              isSaving={updateOption.isPending || isSubmitting}
            />
          )}
          <Tabs
            value={activeTab}
            onValueChange={(value) => {
              setActiveTab(value as PaymentTab)
              void navigate({
                search: (previous) => ({
                  ...previous,
                  tab: value === 'general' ? undefined : value,
                }),
              })
            }}
            className='min-w-0'
          >
            <div className='overflow-x-auto pb-1'>
              <TabsList className='grid min-w-[24rem] grid-cols-3'>
                <TabsTrigger value='general'>{t('General')}</TabsTrigger>
                <TabsTrigger value='epay'>Epay</TabsTrigger>
                <TabsTrigger value='usdt'>USDT</TabsTrigger>
              </TabsList>
            </div>

            <TabsContent value='general' className={paymentTabContentClassName}>
              <div className='space-y-4'>
                <PaymentSceneManager />
                <div>
                  <h3 className='text-lg font-medium'>
                    {t('General Settings')}
                  </h3>
                  <p className='text-muted-foreground text-sm'>
                    {t('Shared configuration for all payment gateways')}
                  </p>
                </div>

                <div className='grid gap-6 md:grid-cols-2'>
                  <FormField
                    control={form.control}
                    name='Price'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>
                          {t('Price (local currency / USD)')}
                        </FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            step='0.01'
                            min={0}
                            {...safeNumberFieldProps(field)}
                          />
                        </FormControl>
                        <FormDescription>
                          {t(
                            'How much to charge for each US dollar of balance (Epay)'
                          )}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name='MinTopUp'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Minimum top-up (USD)')}</FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            step='0.01'
                            min={0}
                            {...safeNumberFieldProps(field)}
                          />
                        </FormControl>
                        <FormDescription>
                          {t('Smallest USD amount users can recharge (Epay)')}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </div>

                <div className='grid gap-6 md:grid-cols-2 md:items-start'>
                  <FormField
                    control={form.control}
                    name='AmountOptions'
                    render={({ field }) => (
                      <FormItem>
                        <div className='mb-2 flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between'>
                          <FormLabel>{t('Top-up amount options')}</FormLabel>
                          <Button
                            type='button'
                            variant='outline'
                            size='sm'
                            onClick={() =>
                              setAmountOptionsVisualMode(
                                !amountOptionsVisualMode
                              )
                            }
                            className='w-full sm:w-auto'
                          >
                            {amountOptionsVisualMode ? (
                              <>
                                <Code2 className='mr-2 h-3 w-3' />
                                {t('JSON Editor')}
                              </>
                            ) : (
                              <>
                                <Eye className='mr-2 h-3 w-3' />
                                {t('Visual Editor')}
                              </>
                            )}
                          </Button>
                        </div>
                        <FormControl>
                          {amountOptionsVisualMode ? (
                            <AmountOptionsVisualEditor
                              value={field.value}
                              onChange={field.onChange}
                            />
                          ) : (
                            <JsonCodeEditor
                              value={field.value}
                              onChange={field.onChange}
                              name={field.name}
                              onBlur={field.onBlur}
                              textareaRef={field.ref}
                              placeholder='[10, 20, 50, 100]'
                              heightClassName='h-40 min-h-40 max-h-40'
                              aria-invalid={Boolean(
                                form.formState.errors.AmountOptions
                              )}
                            />
                          )}
                        </FormControl>
                        <FormDescription>
                          {t('Preset recharge amounts (JSON array)')}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <MainBonusMethodsPanel />
                </div>
              </div>
            </TabsContent>

            <TabsContent
              value='usdt'
              keepMounted
              className={paymentTabContentClassName}
            >
              <BEpusdtSettingsSection
                defaultValues={usdtDefaultValues}
                inlineActions
              />
            </TabsContent>

            <TabsContent
              value='epay'
              keepMounted
              className={paymentTabContentClassName}
            >
              <div className='space-y-4'>
                <div>
                  <h3 className='text-lg font-medium'>{t('Epay Gateway')}</h3>
                  <p className='text-muted-foreground text-sm'>
                    {t('Configuration for Epay payment integration')}
                  </p>
                </div>

                <Alert>
                  <ShieldAlert className='h-4 w-4' />
                  <AlertTitle>{t('Epay safety reminder')}</AlertTitle>
                  <AlertDescription>
                    {t(
                      'Epay is a payment protocol, not a specific official website. Verify the provider yourself and do not trust random third-party Epay deployments.'
                    )}
                  </AlertDescription>
                </Alert>

                <div className='grid gap-6 md:grid-cols-2'>
                  <FormField
                    control={form.control}
                    name='CustomCallbackAddress'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Callback address')}</FormLabel>
                        <FormControl>
                          <Input
                            placeholder={t('https://gateway.example.com')}
                            {...field}
                            onChange={(event) =>
                              field.onChange(event.target.value)
                            }
                          />
                        </FormControl>
                        <FormDescription>
                          {t(
                            'Only enter the site origin, for example https://api.example.com. Do not include any path such as /api/user/epay/notify. Leave blank to use the server address.'
                          )}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </div>

                <div className='space-y-6'>
                  <PaymentChannels embedded />
                  <AdtyEpaySettingsSection
                    defaultValues={epayDefaultValues}
                    inlineActions
                  />
                </div>
              </div>
            </TabsContent>
          </Tabs>
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
