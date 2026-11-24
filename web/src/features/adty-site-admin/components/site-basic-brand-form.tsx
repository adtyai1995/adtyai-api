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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { useDraftNavigation } from '@/features/system-settings/hooks/use-draft-navigation'
import { ADTY_PERMISSIONS, canAdmin } from '@/lib/adty-permissions'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  adjustSite,
  clearSiteNotice,
  type getSite,
  resumeSiteSettlement,
  reviewSite,
  siteAdminKeys,
} from '../api'
import { VISIBILITY_KEYS } from '../lib/settings'
import {
  readSiteVisibility,
  serializeSiteVisibility,
  validSiteVisibility,
} from '../lib/site-visibility'
import { isValidOptionalHttpUrl, parseSiteBrand, siteNotice } from '../lib/sites'
import type { AdminSite, AdminSiteBrand } from '../types'
import { SiteVisibilityEditor } from './site-visibility-editor'

type EditableBrandKey =
  | 'name'
  | 'title'
  | 'tagline'
  | 'notice'
  | 'support_contact'
  | 'tawk_property_id'
  | 'tawk_widget_id'
  | 'wallet_notice'
  | 'voucher_purchase_url'

const EDITABLE_BRAND_KEYS: ReadonlyArray<EditableBrandKey> = [
  'name',
  'title',
  'tagline',
  'notice',
  'support_contact',
  'tawk_property_id',
  'tawk_widget_id',
  'wallet_notice',
  'voucher_purchase_url',
]

const BRAND_LIMITS: Record<EditableBrandKey, number> = {
  name: 40,
  title: 80,
  tagline: 80,
  notice: 500,
  support_contact: 200,
  tawk_property_id: 64,
  tawk_widget_id: 64,
  wallet_notice: 2000,
  voucher_purchase_url: 500,
}

function readAllowedGroups(raw?: string): string {
  if (!raw) return ''
  try {
    const parsed: unknown = JSON.parse(raw)
    if (Array.isArray(parsed)) {
      return parsed.filter((id): id is string => typeof id === 'string').join(', ')
    }
  } catch {
    return raw
  }
  return raw
}

function brandSnapshot(brand: AdminSiteBrand): Record<EditableBrandKey, string> {
  return {
    name: brand.name ?? '',
    title: brand.title ?? '',
    tagline: brand.tagline ?? '',
    notice: brand.notice ?? '',
    support_contact: brand.support_contact ?? '',
    tawk_property_id: brand.tawk_property_id ?? '',
    tawk_widget_id: brand.tawk_widget_id ?? '',
    wallet_notice: brand.wallet_notice ?? '',
    voucher_purchase_url: brand.voucher_purchase_url ?? '',
  }
}

/**
 * Editable Basic & Brand Information form on the dedicated site detail page.
 * Saves all changed fields via PUT /api/adty/admin/sites/:id.
 */
export function SiteBasicBrandForm(props: { site: AdminSite }) {
  const { t } = useTranslation()
  const fieldId = useId()
  const queryClient = useQueryClient()
  const user = useAuthStore((state) => state.auth.user)
  const isRoot = user?.role === ROLE.SUPER_ADMIN
  const canAdjust = canAdmin(user, ADTY_PERMISSIONS.SITE_ADJUST)
  const canDelete = canAdmin(user, ADTY_PERMISSIONS.SITE_DELETE)
  const site = props.site

  const savedBrand = brandSnapshot(parseSiteBrand(site.brand))
  const savedHomePage = site.home_page_content ?? ''
  const savedContact = site.contact_info ?? ''
  const savedSeo = Boolean(site.seo_index)
  const savedAllowedGroups = readAllowedGroups(site.allowed_groups)
  const savedVisibility = readSiteVisibility(site.visibility)

  const [brand, setBrand] = useState<Record<EditableBrandKey, string>>(
    () => savedBrand
  )
  const [homePageContent, setHomePageContent] = useState(savedHomePage)
  const [contactInfo, setContactInfo] = useState(savedContact)
  const [seoIndex, setSeoIndex] = useState(savedSeo)
  const [allowedGroups, setAllowedGroups] = useState(savedAllowedGroups)
  const [visibility, setVisibility] = useState(() => savedVisibility)
  const [reason, setReason] = useState('')
  const [takingDown, setTakingDown] = useState(false)

  const previousSite = useRef(site)
  useEffect(() => {
    const prev = previousSite.current
    if (prev === site) return
    const prevBrand = brandSnapshot(parseSiteBrand(prev.brand))
    const nextBrand = brandSnapshot(parseSiteBrand(site.brand))
    setBrand((current) => {
      const merged = { ...current }
      for (const key of EDITABLE_BRAND_KEYS) {
        if (current[key] === prevBrand[key]) {
          merged[key] = nextBrand[key]
        }
      }
      return merged
    })
    setHomePageContent((current) =>
      current === (prev.home_page_content ?? '')
        ? (site.home_page_content ?? '')
        : current
    )
    setContactInfo((current) =>
      current === (prev.contact_info ?? '') ? (site.contact_info ?? '') : current
    )
    setSeoIndex((current) =>
      current === Boolean(prev.seo_index) ? Boolean(site.seo_index) : current
    )
    setAllowedGroups((current) =>
      current === readAllowedGroups(prev.allowed_groups)
        ? readAllowedGroups(site.allowed_groups)
        : current
    )
    const prevVis = readSiteVisibility(prev.visibility)
    const nextVis = readSiteVisibility(site.visibility)
    setVisibility((current) => {
      const merged = { ...current }
      for (const key of VISIBILITY_KEYS) {
        if (current[key] === prevVis[key]) {
          merged[key] = nextVis[key]
        }
      }
      return merged
    })
    previousSite.current = site
  }, [site])

  const visibilityValid = validSiteVisibility(site.visibility)
  const changedBrandKeys = EDITABLE_BRAND_KEYS.filter(
    (key) => brand[key] !== savedBrand[key]
  )
  const homePageChanged = homePageContent !== savedHomePage
  const contactChanged = contactInfo !== savedContact
  const seoChanged = seoIndex !== savedSeo
  const allowedGroupsChanged = allowedGroups.trim() !== savedAllowedGroups
  const visibilityChanged = VISIBILITY_KEYS.some(
    (key) => visibility[key] !== savedVisibility[key]
  )

  const hasChanges =
    changedBrandKeys.length > 0 ||
    homePageChanged ||
    contactChanged ||
    seoChanged ||
    allowedGroupsChanged ||
    visibilityChanged

  const voucherUrlInvalid = !isValidOptionalHttpUrl(brand.voucher_purchase_url)

  const dirty = hasChanges || reason !== ''
  useDraftNavigation(dirty)

  const refresh = () => {
    toast.success(t('Saved successfully'))
    setReason('')
    void queryClient.invalidateQueries({ queryKey: siteAdminKeys.all })
  }

  const review = useMutation({
    mutationFn: (approve: boolean) => {
      if (useAuthStore.getState().auth.user?.role !== ROLE.SUPER_ADMIN) {
        throw new Error(t('No permission to perform this action'))
      }
      return reviewSite(site.id, {
        approve,
        note: reason.trim(),
      })
    },
    onSuccess: refresh,
    onError: (err) => handleServerError(err),
  })

  const save = useMutation({
    mutationFn: () => {
      if (
        !canAdmin(
          useAuthStore.getState().auth.user,
          ADTY_PERMISSIONS.SITE_ADJUST
        )
      ) {
        throw new Error(t('No permission to perform this action'))
      }
      const body: Parameters<typeof adjustSite>[1] = {
        reason: reason.trim(),
      }
      const clear: Array<'cost_factor' | 'allowed_groups'> = []
      if (changedBrandKeys.length > 0) {
        body.brand = Object.fromEntries(
          changedBrandKeys.map((key) => [key, brand[key]])
        )
      }
      if (homePageChanged) {
        body.home_page_content = homePageContent
      }
      if (contactChanged) {
        body.contact_info = contactInfo.trim()
      }
      if (seoChanged) {
        body.seo_index = seoIndex
      }
      if (visibilityChanged) {
        body.visibility = serializeSiteVisibility(visibility)
      }
      if (allowedGroupsChanged) {
        if (allowedGroups.trim() === '') {
          clear.push('allowed_groups')
        } else {
          body.allowed_groups = JSON.stringify(
            allowedGroups
              .split(',')
              .map((id) => id.trim())
              .filter(Boolean)
          )
        }
      }
      if (clear.length > 0) {
        body.clear = clear
      }
      return adjustSite(site.id, body)
    },
    onSuccess: (updated) => {
      const nextBrand = brandSnapshot(parseSiteBrand(updated.brand))
      setBrand(nextBrand)
      setHomePageContent(updated.home_page_content ?? '')
      setContactInfo(updated.contact_info ?? '')
      setSeoIndex(Boolean(updated.seo_index))
      setAllowedGroups(readAllowedGroups(updated.allowed_groups))
      setVisibility(readSiteVisibility(updated.visibility))
      queryClient.setQueryData<Awaited<ReturnType<typeof getSite>>>(
        siteAdminKeys.site(site.id),
        (cached) => (cached ? { ...cached, site: updated } : cached)
      )
      refresh()
    },
    onError: (err) => handleServerError(err),
  })

  const settlement = useMutation({
    mutationFn: () => resumeSiteSettlement(site.id),
    onSuccess: refresh,
    onError: (err) => handleServerError(err),
  })

  const takedown = useMutation({
    mutationFn: () => {
      if (
        !canAdmin(
          useAuthStore.getState().auth.user,
          ADTY_PERMISSIONS.SITE_DELETE
        )
      ) {
        throw new Error(t('No permission to perform this action'))
      }
      return clearSiteNotice(site.id, reason.trim())
    },
    onSuccess: () => {
      setTakingDown(false)
      refresh()
    },
    onError: (err) => handleServerError(err),
  })

  const editable =
    canAdjust &&
    site.status !== 'closed' &&
    site.status !== 'closing' &&
    site.status !== 'pending'
  const busy =
    save.isPending ||
    review.isPending ||
    settlement.isPending ||
    takedown.isPending
  const savedNoticeText = siteNotice(site.brand)

  const handleReset = () => {
    setBrand(savedBrand)
    setHomePageContent(savedHomePage)
    setContactInfo(savedContact)
    setSeoIndex(savedSeo)
    setAllowedGroups(savedAllowedGroups)
    setVisibility(savedVisibility)
    setReason('')
  }

  return (
    <form
      className='flex flex-col gap-5'
      onSubmit={(event) => {
        event.preventDefault()
        if (!editable || !hasChanges || voucherUrlInvalid) {
          return
        }
        save.mutate()
      }}
    >
      {(site.settle_hold_reason ?? '') !== '' && (
        <Alert className='border-warning/40 bg-warning/5'>
          <AlertDescription className='flex flex-wrap items-center justify-between gap-2 text-sm'>
            <span>
              {t('Settlement is on hold: {{reason}}', {
                reason: site.settle_hold_reason,
              })}
            </span>
            {canAdjust && (
              <Button
                type='button'
                size='sm'
                variant='outline'
                disabled={busy}
                onClick={() => settlement.mutate()}
              >
                {t('Resume settlement')}
              </Button>
            )}
          </AlertDescription>
        </Alert>
      )}

      {isRoot && site.status === 'pending' && (
        <Card>
          <CardHeader>
            <CardTitle>{t('Pending site application')}</CardTitle>
            <CardDescription>
              {t(
                'Approve or reject this pending site application. Optionally enter a review note.'
              )}
            </CardDescription>
          </CardHeader>
          <CardContent className='flex flex-wrap gap-2'>
            <Button
              type='button'
              variant='brand'
              disabled={busy}
              onClick={() => review.mutate(true)}
            >
              {t('Approve')}
            </Button>
            <Button
              type='button'
              variant='outline'
              disabled={busy}
              onClick={() => review.mutate(false)}
            >
              {t('Reject')}
            </Button>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle>{t('Brand & Customer Display')}</CardTitle>
          <CardDescription>
            {t(
              'Configure public brand identity, wallet notice, voucher purchase link, and live chat widget.'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent className='grid gap-4'>
          <div className='grid gap-4 sm:grid-cols-2'>
            <div className='space-y-1.5'>
              <Label htmlFor={`${fieldId}-name`}>{t('Site name')}</Label>
              <Input
                id={`${fieldId}-name`}
                maxLength={BRAND_LIMITS.name}
                disabled={!editable || busy}
                value={brand.name}
                onChange={(event) =>
                  setBrand((prev) => ({ ...prev, name: event.target.value }))
                }
              />
            </div>
            <div className='space-y-1.5'>
              <Label htmlFor={`${fieldId}-title`}>{t('Website title')}</Label>
              <Input
                id={`${fieldId}-title`}
                maxLength={BRAND_LIMITS.title}
                disabled={!editable || busy}
                value={brand.title}
                onChange={(event) =>
                  setBrand((prev) => ({ ...prev, title: event.target.value }))
                }
              />
            </div>
            <div className='space-y-1.5'>
              <Label htmlFor={`${fieldId}-tagline`}>{t('Tagline')}</Label>
              <Input
                id={`${fieldId}-tagline`}
                maxLength={BRAND_LIMITS.tagline}
                disabled={!editable || busy}
                value={brand.tagline}
                onChange={(event) =>
                  setBrand((prev) => ({ ...prev, tagline: event.target.value }))
                }
              />
            </div>
            <div className='space-y-1.5'>
              <Label htmlFor={`${fieldId}-support-contact`}>
                {t('Support contact')}
              </Label>
              <Input
                id={`${fieldId}-support-contact`}
                maxLength={BRAND_LIMITS.support_contact}
                disabled={!editable || busy}
                value={brand.support_contact}
                onChange={(event) =>
                  setBrand((prev) => ({
                    ...prev,
                    support_contact: event.target.value,
                  }))
                }
              />
            </div>
          </div>

          <div className='space-y-1.5'>
            <div className='flex items-center justify-between gap-2'>
              <Label htmlFor={`${fieldId}-notice`}>{t('Notice')}</Label>
              {canDelete && savedNoticeText && (
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  disabled={busy}
                  onClick={() => setTakingDown(true)}
                >
                  {t('Take down notice')}
                </Button>
              )}
            </div>
            <Textarea
              id={`${fieldId}-notice`}
              rows={3}
              maxLength={BRAND_LIMITS.notice}
              disabled={!editable || busy}
              value={brand.notice}
              onChange={(event) =>
                setBrand((prev) => ({ ...prev, notice: event.target.value }))
              }
            />
          </div>

          <div className='space-y-1.5'>
            <Label htmlFor={`${fieldId}-wallet-notice`}>
              {t('Wallet notice')}
            </Label>
            <Textarea
              id={`${fieldId}-wallet-notice`}
              rows={3}
              maxLength={BRAND_LIMITS.wallet_notice}
              disabled={!editable || busy}
              value={brand.wallet_notice}
              placeholder={t(
                'Optional announcement displayed on the customer wallet page'
              )}
              onChange={(event) =>
                setBrand((prev) => ({
                  ...prev,
                  wallet_notice: event.target.value,
                }))
              }
            />
            <p className='text-muted-foreground text-xs'>
              {t('{{count}} / {{max}} characters', {
                count: [...brand.wallet_notice].length,
                max: BRAND_LIMITS.wallet_notice,
              })}
            </p>
          </div>

          <div className='space-y-1.5'>
            <Label htmlFor={`${fieldId}-voucher-url`}>
              {t('Voucher purchase link')}
            </Label>
            <Input
              id={`${fieldId}-voucher-url`}
              type='url'
              maxLength={BRAND_LIMITS.voucher_purchase_url}
              disabled={!editable || busy}
              placeholder='https://...'
              aria-invalid={voucherUrlInvalid}
              value={brand.voucher_purchase_url}
              onChange={(event) =>
                setBrand((prev) => ({
                  ...prev,
                  voucher_purchase_url: event.target.value,
                }))
              }
            />
            {voucherUrlInvalid ? (
              <p className='text-destructive text-xs'>
                {t('Enter a valid http:// or https:// URL')}
              </p>
            ) : (
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Optional external link shown to customers on the wallet page for purchasing recharge codes.'
                )}
              </p>
            )}
          </div>

          <div className='grid gap-4 sm:grid-cols-2'>
            <div className='space-y-1.5'>
              <Label htmlFor={`${fieldId}-tawk-property`}>
                {t('Tawk.to property ID')}
              </Label>
              <Input
                id={`${fieldId}-tawk-property`}
                maxLength={BRAND_LIMITS.tawk_property_id}
                disabled={!editable || busy}
                value={brand.tawk_property_id}
                onChange={(event) =>
                  setBrand((prev) => ({
                    ...prev,
                    tawk_property_id: event.target.value,
                  }))
                }
              />
            </div>
            <div className='space-y-1.5'>
              <Label htmlFor={`${fieldId}-tawk-widget`}>
                {t('Tawk.to widget ID')}
              </Label>
              <Input
                id={`${fieldId}-tawk-widget`}
                maxLength={BRAND_LIMITS.tawk_widget_id}
                disabled={!editable || busy}
                value={brand.tawk_widget_id}
                onChange={(event) =>
                  setBrand((prev) => ({
                    ...prev,
                    tawk_widget_id: event.target.value,
                  }))
                }
              />
            </div>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t('Custom Home Page')}</CardTitle>
          <CardDescription>
            {t(
              'Enter Markdown, HTML, or an http:// or https:// URL (embedded as an iframe). Setting custom home page content replaces the default homepage and hides default status info.'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent className='space-y-2'>
          <Label htmlFor={`${fieldId}-home-page`}>{t('Home page content')}</Label>
          <Textarea
            id={`${fieldId}-home-page`}
            rows={6}
            className='font-mono text-xs'
            disabled={!editable || busy}
            value={homePageContent}
            placeholder={t(
              'Leave empty to use the default homepage, or paste Markdown, HTML, or https://...'
            )}
            onChange={(event) => setHomePageContent(event.target.value)}
          />
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t('Operations & Visibility')}</CardTitle>
          <CardDescription>
            {t(
              'Manage owner contact, search engine indexing, allowed billing groups, and customer data visibility.'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent className='grid gap-4'>
          <div className='space-y-1.5'>
            <Label htmlFor={`${fieldId}-contact`}>{t('Owner contact')}</Label>
            <Input
              id={`${fieldId}-contact`}
              disabled={!editable || busy}
              value={contactInfo}
              placeholder={t('Telegram, WeChat, or email for operations')}
              onChange={(event) => setContactInfo(event.target.value)}
            />
          </div>

          <div className='space-y-1.5'>
            <Label htmlFor={`${fieldId}-allowed-groups`}>
              {t('Allowed groups')}
            </Label>
            <Input
              id={`${fieldId}-allowed-groups`}
              value={allowedGroups}
              placeholder={t('Every live billing group')}
              disabled={!editable || busy}
              onChange={(event) => setAllowedGroups(event.target.value)}
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'Leave empty to allow every live billing group. Separate group IDs with commas.'
              )}
            </p>
          </div>

          <div className='flex items-center justify-between gap-4 rounded-md border p-3'>
            <div className='space-y-0.5'>
              <Label htmlFor={`${fieldId}-seo`}>
                {t('Let search engines index this site')}
              </Label>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Controls whether robots.txt and meta tags allow search engines to index the subsite.'
                )}
              </p>
            </div>
            <Switch
              id={`${fieldId}-seo`}
              checked={seoIndex}
              disabled={!editable || busy}
              onCheckedChange={setSeoIndex}
            />
          </div>

          <SiteVisibilityEditor
            value={visibility}
            onChange={setVisibility}
            disabled={!editable || busy || !visibilityValid}
          />
          {!visibilityValid && (
            <p role='alert' className='text-destructive text-sm'>
              {t(
                'Saved site visibility settings are invalid. Reload or repair the saved settings before editing this site.'
              )}
            </p>
          )}

          <div className='space-y-1.5'>
            <Label htmlFor={`${fieldId}-reason`}>
              {t('Change note (optional)')}
            </Label>
            <Textarea
              id={`${fieldId}-reason`}
              rows={2}
              disabled={busy}
              value={reason}
              placeholder={t('Optional audit note for this update')}
              onChange={(event) => setReason(event.target.value)}
            />
          </div>

          {editable && (
            <div className='flex flex-wrap items-center gap-2 pt-1'>
              <Button
                type='submit'
                variant='brand'
                disabled={
                  busy || !hasChanges || voucherUrlInvalid || !visibilityValid
                }
              >
                {t('Save site settings')}
              </Button>
              {dirty && (
                <Button
                  type='button'
                  variant='outline'
                  disabled={busy}
                  onClick={handleReset}
                >
                  {t('Reset')}
                </Button>
              )}
            </div>
          )}
        </CardContent>
      </Card>

      <ConfirmDialog
        open={takingDown}
        onOpenChange={setTakingDown}
        title={t('Take down notice')}
        desc={t(
          "The notice disappears from the site at once. The owner's other brand settings stay."
        )}
        destructive
        isLoading={takedown.isPending}
        disabled={!canDelete}
        handleConfirm={() => canDelete && takedown.mutate()}
      />
    </form>
  )
}
