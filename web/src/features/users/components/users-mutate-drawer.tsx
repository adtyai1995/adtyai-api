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
import { useQuery } from '@tanstack/react-query'
import { KeyRound, Pencil } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  SideDrawerSection,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { PasswordInput } from '@/components/password-input'
import { PasswordStrength } from '@/components/password-strength'
import { PermissionMatrix } from '@/components/permission-matrix'
import { Button } from '@/components/ui/button'
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
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Textarea } from '@/components/ui/textarea'
import { PermissionPresets } from '@/features/adty-admin-permissions/permission-presets'
import { UserLedgerDialog } from '@/features/adty-ledger/user-ledger-dialog'
import { UserLimitsFields } from '@/features/adty-ledger/user-limits-fields'
import {
  ADMIN_PERMISSION_ACTIONS,
  ADMIN_PERMISSION_RESOURCES,
  EMPTY_PERMISSION_CATALOG,
  hasPermission,
  normalizeAdminPermissions,
  type AdminPermissionMatrix,
} from '@/lib/admin-permissions'
import { ADTY_PERMISSIONS, canAdmin } from '@/lib/adty-permissions'
import { getCurrencyDisplay, getCurrencyLabel } from '@/lib/currency'
import { formatQuota, parseQuotaFromDollars } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { accountPasswordSchema } from '@/lib/password-policy'
import { ROLE } from '@/lib/roles'
import { AuthOperationError } from '@/lib/secure-verification'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { createUser, updateUser, getUser, getPermissionCatalog } from '../api'
import { BINDING_FIELDS, ERROR_MESSAGES, SUCCESS_MESSAGES } from '../constants'
import {
  userFormSchema,
  type UserFormValues,
  USER_FORM_DEFAULT_VALUES,
  transformFormDataToPayload,
  transformUserToFormDefaults,
} from '../lib'
import type { User } from '../types'
import { UserQuotaDialog } from './user-quota-dialog'
import { useUsers } from './users-provider'
import { UsersResetPasswordDialog } from './users-reset-password-dialog'

type UsersMutateDrawerProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentRow?: User
}

export function UsersMutateDrawer({
  open,
  onOpenChange,
  currentRow,
}: UsersMutateDrawerProps) {
  const { t } = useTranslation()

  const isUpdate = !!currentRow
  const targetUserId = currentRow?.id
  const { triggerRefresh, requestVerification } = useUsers()
  const currentUser = useAuthStore((s) => s.auth.user)
  const canSubmit = canAdmin(
    currentUser,
    isUpdate ? ADTY_PERMISSIONS.USER_WRITE : ADTY_PERMISSIONS.USER_CREATE
  )
  // adty: balances need "adjust balances"
  const canAdjustQuota = canAdmin(currentUser, ADTY_PERMISSIONS.USER_QUOTA)
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [quotaDialogOpen, setQuotaDialogOpen] = useState(false)
  // Adjustments use exact quota units, separate from the form display.
  const [currentQuotaRaw, setCurrentQuotaRaw] = useState(0)
  const [currentLiveQuota, setCurrentLiveQuota] = useState(0)
  const [ledgerOpen, setLedgerOpen] = useState(false)
  const [resetPasswordOpen, setResetPasswordOpen] = useState(false)
  const [loadedUser, setLoadedUser] = useState<User>()
  const loadedTargetId = loadedUser?.id
  const isTargetLoaded =
    !isUpdate || (targetUserId !== undefined && loadedTargetId === targetUserId)
  const siteUser = isTargetLoaded ? loadedUser : undefined
  // Matrix as loaded from the server; an unchanged matrix is not resubmitted so
  // routine edits of an administrator do not require step-up verification.
  const loadedPermissions = useRef<AdminPermissionMatrix | undefined>(undefined)

  // Permission catalog is owned by the backend; fetched once and reused.
  const { data: permissionCatalog = EMPTY_PERMISSION_CATALOG } = useQuery({
    queryKey: ['admin-permission-catalog'],
    queryFn: async () => requireServerSuccess(await getPermissionCatalog()),
    staleTime: 5 * 60 * 1000,
  })

  const form = useForm<UserFormValues>({
    resolver: zodResolver(userFormSchema),
    defaultValues: USER_FORM_DEFAULT_VALUES,
  })

  // Load existing data when updating
  useEffect(() => {
    let cancelled = false
    setLoadedUser(undefined)
    loadedPermissions.current = undefined
    setCurrentQuotaRaw(0)
    setCurrentLiveQuota(0)
    setQuotaDialogOpen(false)
    setLedgerOpen(false)
    setResetPasswordOpen(false)
    form.reset(USER_FORM_DEFAULT_VALUES)

    if (open && isUpdate && targetUserId !== undefined) {
      // For update, fetch fresh data
      getUser(targetUserId)
        .then((result) => {
          if (cancelled) return
          if (result.success && result.data) {
            setLoadedUser(result.data)
            loadedPermissions.current = result.data.admin_permissions
            setCurrentQuotaRaw(result.data.quota)
            setCurrentLiveQuota(result.data.live_quota ?? result.data.quota)
            form.reset(transformUserToFormDefaults(result.data))
          } else {
            handleServerError(result, t('Failed to load'))
          }
        })
        .catch((error) => {
          if (!cancelled) handleServerError(error, t('Failed to load'))
        })
    }
    return () => {
      cancelled = true
    }
  }, [open, isUpdate, targetUserId, form, t])

  const { meta: currencyMeta } = getCurrencyDisplay()
  const currencyLabel = getCurrencyLabel()
  const tokensOnly = currencyMeta.kind === 'tokens'

  const selectedRole = form.watch('role')
  const canEditAdminPermissions = currentUser?.role === ROLE.SUPER_ADMIN
  const targetIsAdmin =
    (selectedRole ?? (isTargetLoaded ? loadedUser?.role : 0) ?? 0) >= ROLE.ADMIN
  const targetForReset = loadedUser ?? currentRow
  const canResetRandomPassword =
    isUpdate &&
    Boolean(targetForReset) &&
    canSubmit &&
    isTargetLoaded &&
    (targetForReset?.role ?? 0) !== ROLE.SUPER_ADMIN &&
    (currentUser?.role === ROLE.SUPER_ADMIN ||
      (currentUser?.role ?? 0) > (targetForReset?.role ?? 0))

  const onSubmit = async (data: UserFormValues) => {
    if (isUpdate && !isTargetLoaded) return
    const action = isUpdate
      ? ADTY_PERMISSIONS.USER_WRITE
      : ADTY_PERMISSIONS.USER_CREATE
    if (!canAdmin(useAuthStore.getState().auth.user, action)) return
    if (!isUpdate || data.password) {
      if (!accountPasswordSchema.safeParse(data.password ?? '').success) {
        form.setError('password', {
          type: 'manual',
          message: t('Password must contain between 8 and 128 characters.'),
        })
        return
      }
    }

    setIsSubmitting(true)
    try {
      const payload = transformFormDataToPayload(
        data,
        currentRow?.id,
        permissionCatalog
      )
      if (
        isUpdate &&
        payload.admin_permissions &&
        JSON.stringify(payload.admin_permissions) ===
          JSON.stringify(
            normalizeAdminPermissions(
              loadedPermissions.current,
              permissionCatalog
            )
          )
      ) {
        delete payload.admin_permissions
      }
      // Resetting a password, rewriting admin permissions, or creating an
      // administrator changes who can sign in or what they may do.
      let proofToken: string | undefined
      if (isUpdate && currentRow) {
        if (payload.password || payload.admin_permissions) {
          const proof = await requestVerification({
            scope: 'admin.user.update',
            context: { user_id: currentRow.id },
            title: t('Verify to update user credentials'),
            description: t(
              'Confirm your identity before changing the account {{username}}.',
              { username: currentRow.username }
            ),
          })
          if (!proof) return
          proofToken = proof.proof_token
        }
      } else if ((payload.role ?? 0) >= ROLE.ADMIN) {
        const proof = await requestVerification({
          scope: 'admin.user.create',
          context: { role: payload.role ?? ROLE.ADMIN },
          title: t('Verify to create administrator'),
          description: t(
            'Confirm your identity before creating an administrator account.'
          ),
        })
        if (!proof) return
        proofToken = proof.proof_token
      }
      if (!canAdmin(useAuthStore.getState().auth.user, action)) return
      const result = isUpdate
        ? await updateUser(
            payload as typeof payload & { id: number },
            proofToken
          )
        : await createUser(payload, proofToken)

      if (result.success) {
        toast.success(
          isUpdate
            ? t(SUCCESS_MESSAGES.USER_UPDATED)
            : t(SUCCESS_MESSAGES.USER_CREATED)
        )
        onOpenChange(false)
        triggerRefresh()
      } else {
        handleServerError(result, t(ERROR_MESSAGES.CREATE_FAILED))
      }
    } catch (error) {
      handleServerError(
        AuthOperationError.from(error),
        t(ERROR_MESSAGES.UNEXPECTED)
      )
    } finally {
      setIsSubmitting(false)
    }
  }

  const refreshUserData = async () => {
    if (!currentRow) return
    try {
      const result = requireServerSuccess(await getUser(currentRow.id))
      if (result.success && result.data) {
        setLoadedUser(result.data)
        loadedPermissions.current = result.data.admin_permissions
        setCurrentQuotaRaw(result.data.quota)
        setCurrentLiveQuota(result.data.live_quota ?? result.data.quota)
        form.reset(transformUserToFormDefaults(result.data))
      }
      triggerRefresh()
    } catch (error) {
      handleServerError(error, t('Failed to load'))
    }
  }

  return (
    <>
      <Sheet
        open={open}
        onOpenChange={(v) => {
          onOpenChange(v)
          if (!v) {
            form.reset()
          }
        }}
      >
        <SheetContent
          className={sideDrawerContentClassName('sm:max-w-[600px]')}
        >
          <SheetHeader className={sideDrawerHeaderClassName()}>
            <SheetTitle>
              {isUpdate ? t('Update') : t('Create')} {t('User')}
            </SheetTitle>
            <SheetDescription>
              {isUpdate
                ? t('Update the user by providing necessary info.')
                : t('Add a new user by providing necessary info.')}
            </SheetDescription>
          </SheetHeader>
          <Form {...form}>
            <form
              id='user-form'
              onSubmit={form.handleSubmit(onSubmit)}
              className={sideDrawerFormClassName()}
            >
              {/* Basic Information */}
              <SideDrawerSection>
                <h3 className='text-sm font-medium'>
                  {t('Basic Information')}
                </h3>

                {isUpdate && (
                  <dl className='grid grid-cols-2 gap-3 text-sm'>
                    <div className='min-w-0'>
                      <dt
                        id='user-site-label'
                        className='text-muted-foreground'
                      >
                        {t('Site')}
                      </dt>
                      <dd
                        aria-labelledby='user-site-label'
                        className='break-words'
                      >
                        {(siteUser?.site_id ?? 0) === 0
                          ? t('Main site')
                          : siteUser?.site_name ||
                            `${t('Site')} #${siteUser?.site_id}`}
                      </dd>
                    </div>
                    <div>
                      <dt
                        id='user-site-id-label'
                        className='text-muted-foreground'
                      >
                        {t('Site ID')}
                      </dt>
                      <dd aria-labelledby='user-site-id-label'>
                        {siteUser?.site_id ?? 0}
                      </dd>
                    </div>
                  </dl>
                )}

                <FormField
                  control={form.control}
                  name='username'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Username')}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          placeholder={t('Enter username')}
                          disabled={isUpdate}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                {!isUpdate && (
                  <FormField
                    control={form.control}
                    name='role'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Role')}</FormLabel>
                        <Select
                          items={[
                            { value: '1', label: t('Common User') },
                            { value: '10', label: t('Admin') },
                          ]}
                          onValueChange={(value) =>
                            value !== null &&
                            field.onChange(Number.parseInt(value))
                          }
                          value={String(field.value)}
                        >
                          <FormControl>
                            <SelectTrigger>
                              <SelectValue placeholder={t('Select a role')} />
                            </SelectTrigger>
                          </FormControl>
                          <SelectContent alignItemWithTrigger={false}>
                            <SelectGroup>
                              <SelectItem value='1'>
                                {t('Common User')}
                              </SelectItem>
                              <SelectItem value='10'>{t('Admin')}</SelectItem>
                            </SelectGroup>
                          </SelectContent>
                        </Select>
                        <FormDescription>
                          {t("Set the user's role (cannot be Root)")}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                )}

                <FormField
                  control={form.control}
                  name='display_name'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Display Name')}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          placeholder={t('Enter display name')}
                        />
                      </FormControl>
                      <FormDescription>
                        {t('Leave empty to use username')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='password'
                  render={({ field }) => (
                    <FormItem>
                      <div className='flex items-center justify-between gap-2'>
                        <FormLabel>{t('Password')}</FormLabel>
                        {isUpdate && (
                          <Button
                            type='button'
                            size='sm'
                            variant='outline'
                            disabled={!canResetRandomPassword || isSubmitting}
                            onClick={() => setResetPasswordOpen(true)}
                          >
                            <KeyRound className='mr-1 h-3.5 w-3.5' aria-hidden />
                            {t('Reset random password')}
                          </Button>
                        )}
                      </div>
                      <FormControl
                        describedBy={
                          !isUpdate || field.value
                            ? 'user-password-strength'
                            : undefined
                        }
                      >
                        <PasswordInput
                          {...field}
                          value={field.value ?? ''}
                          autoComplete='new-password'
                          placeholder={
                            isUpdate
                              ? t('Leave empty to keep unchanged')
                              : t('Enter password (8–128 characters)')
                          }
                        />
                      </FormControl>
                      <PasswordStrength
                        id='user-password-strength'
                        value={field.value ?? ''}
                        quietWhenEmpty={isUpdate}
                      />
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </SideDrawerSection>

              {/* Quota Settings (Update only) */}
              {isUpdate && (
                <SideDrawerSection>
                  <h3 className='text-sm font-medium'>{t('Quota Settings')}</h3>

                  <FormField
                    control={form.control}
                    name='quota_dollars'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>
                          {t('Remaining Quota ({{currency}})', {
                            currency: currencyLabel,
                          })}
                        </FormLabel>
                        <div className='flex gap-2'>
                          <FormControl>
                            <Input
                              value={
                                tokensOnly
                                  ? String(field.value || 0)
                                  : (field.value || 0).toFixed(6)
                              }
                              readOnly
                              className='flex-1'
                            />
                          </FormControl>
                          <Button
                            type='button'
                            variant='outline'
                            onClick={() => setQuotaDialogOpen(true)}
                            disabled={!canAdjustQuota || !isTargetLoaded}
                          >
                            <Pencil className='mr-1 h-4 w-4' />
                            {t('Adjust Quota')}
                          </Button>
                          {currentRow && (
                            <Button
                              type='button'
                              variant='outline'
                              onClick={() => setLedgerOpen(true)}
                              disabled={!isTargetLoaded}
                            >
                              {t('Balance ledger')}
                            </Button>
                          )}
                        </div>
                        <FormDescription>
                          {formatQuota(parseQuotaFromDollars(field.value || 0))}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name='remark'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Remark')}</FormLabel>
                        <FormControl>
                          <Textarea
                            {...field}
                            placeholder={t(
                              'Admin notes (only visible to admins)'
                            )}
                            rows={3}
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  {currentRow && (
                    <UserLimitsFields
                      userId={currentRow.id}
                      canEdit={canAdmin(
                        currentUser,
                        ADTY_PERMISSIONS.USER_WRITE
                      )}
                    />
                  )}
                </SideDrawerSection>
              )}

              {canEditAdminPermissions &&
                targetIsAdmin &&
                permissionCatalog.resources.length > 0 && (
                  <SideDrawerSection>
                    <h3 className='text-sm font-medium'>
                      {t('Admin Permissions')}
                    </h3>
                    <p className='text-muted-foreground text-xs'>
                      {t(
                        'A new administrator has no admin permissions. Turn on what this person needs, or apply a preset.'
                      )}
                    </p>
                    <FormField
                      control={form.control}
                      name='admin_permissions'
                      render={({ field }) => {
                        const selected = normalizeAdminPermissions(
                          field.value,
                          permissionCatalog
                        )
                        return (
                          <FormItem>
                            <PermissionPresets
                              catalog={permissionCatalog}
                              onApply={field.onChange}
                            />
                            <PermissionMatrix
                              resources={permissionCatalog.resources}
                              value={selected}
                              onChange={field.onChange}
                            />
                            <FormMessage />
                          </FormItem>
                        )
                      }}
                    />
                    {currentUser && (
                      <p className='text-muted-foreground text-xs'>
                        {hasPermission(
                          currentUser,
                          ADMIN_PERMISSION_RESOURCES.CHANNEL,
                          ADMIN_PERMISSION_ACTIONS.SENSITIVE_WRITE
                        )
                          ? t(
                              'Your account can edit sensitive channel settings.'
                            )
                          : t(
                              'Your account cannot edit sensitive channel settings.'
                            )}
                      </p>
                    )}
                  </SideDrawerSection>
                )}

              {/* Binding Information (Read-only) */}
              {isUpdate && (
                <SideDrawerSection>
                  <h3 className='text-sm font-medium'>
                    {t('Binding Information')}
                  </h3>
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'Account bindings (read-only, managed by user in Security & Access)'
                    )}
                  </p>

                  <div className='flex flex-col gap-3'>
                    {BINDING_FIELDS.map(({ key, label }) => (
                      <div key={key}>
                        <Label className='text-muted-foreground text-xs'>
                          {t(label)}
                        </Label>
                        <Input
                          value={
                            (isTargetLoaded
                              ? (loadedUser?.[key as keyof User] as string)
                              : '') || '-'
                          }
                          disabled
                          className='mt-1'
                        />
                      </div>
                    ))}
                  </div>
                </SideDrawerSection>
              )}
            </form>
          </Form>
          <SheetFooter className={sideDrawerFooterClassName()}>
            <SheetClose render={<Button variant='outline' />}>
              {t('Close')}
            </SheetClose>
            <Button
              variant='brand'
              form='user-form'
              type='submit'
              disabled={isSubmitting || !canSubmit || !isTargetLoaded}
            >
              {isSubmitting ? t('Saving...') : t('Save changes')}
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>

      {/* Adjust Quota Dialog */}
      {currentRow && isTargetLoaded && loadedUser && (
        <UserQuotaDialog
          open={quotaDialogOpen && isTargetLoaded}
          onOpenChange={setQuotaDialogOpen}
          userId={loadedUser.id}
          currentQuota={currentQuotaRaw}
          currentLiveQuota={currentLiveQuota}
          onSuccess={refreshUserData}
        />
      )}
      {currentRow && isTargetLoaded && loadedUser && (
        <UserLedgerDialog
          open={ledgerOpen && isTargetLoaded}
          onOpenChange={setLedgerOpen}
          userId={loadedUser.id}
        />
      )}
      {currentRow && isTargetLoaded && loadedUser && (
        <UsersResetPasswordDialog
          open={resetPasswordOpen && isTargetLoaded}
          onOpenChange={setResetPasswordOpen}
          user={loadedUser}
        />
      )}
    </>
  )
}
