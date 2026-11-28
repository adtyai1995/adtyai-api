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
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { HtmlContent } from '@/components/html-content'
import { useAdtySite } from '@/features/adty-site/api'
import { useStatus } from '@/hooks/use-status'
import { useSystemConfig } from '@/hooks/use-system-config'
import { ADTY_BRAND_NAME } from '@/lib/adty-brand'
import { DEFAULT_LOGO } from '@/lib/constants'
import { subsiteDocument } from '@/lib/subsite-document'
import { cn } from '@/lib/utils'

interface FooterLink {
  text: string
  href: string
}

interface FooterColumnProps {
  title: string
  links: FooterLink[]
}

interface FooterProps {
  logo?: string
  name?: string
  columns?: FooterColumnProps[]
  copyright?: string
  className?: string
}

function FooterLinkItem(props: { link: FooterLink }) {
  const { t } = useTranslation()
  const isExternal = props.link.href.startsWith('http')
  const label = t(props.link.text)
  const className =
    'text-muted-foreground hover:text-foreground text-[13px] transition-colors duration-200'

  if (props.link.href.startsWith('mailto:')) {
    return (
      <a href={props.link.href} className={className}>
        {label}
      </a>
    )
  }

  if (isExternal) {
    return (
      <a
        href={props.link.href}
        target='_blank'
        rel='noopener noreferrer'
        className={className}
      >
        {label}
      </a>
    )
  }

  return (
    <Link to={props.link.href} className={className}>
      {label}
    </Link>
  )
}

/** Main-site-only partnership entry, hidden on white-label subsites. */
function PartnershipColumn() {
  const { t } = useTranslation()
  const { status } = useStatus()
  if (subsiteDocument() || status?.adty_site) {
    return null
  }
  return (
    <nav aria-labelledby='footer-partnership-title' className='min-w-40'>
      <p
        id='footer-partnership-title'
        className='text-muted-foreground text-sm'
      >
        {t('Partnership')}
      </p>
      <ul className='mt-5 space-y-3.5'>
        <li>
          <Link
            to='/partner'
            className='text-foreground/85 hover:text-primary text-[15px] transition-colors duration-200'
          >
            {t('Become a Partner')}
          </Link>
        </li>
      </ul>
    </nav>
  )
}

/** The site legal pages that are switched on, as one titled footer column. */
function LegalColumn() {
  const { t } = useTranslation()
  const { status } = useStatus()
  const refundPolicyEnabled = useAdtySite().data?.refund_policy_enabled === true
  const items: { key: string; label: string; href: string }[] = []
  if (status?.user_agreement_enabled) {
    items.push({
      key: 'user-agreement',
      label: t('User Agreement'),
      href: '/user-agreement',
    })
  }
  if (status?.privacy_policy_enabled) {
    items.push({
      key: 'privacy-policy',
      label: t('Privacy Policy'),
      href: '/privacy-policy',
    })
  }
  if (refundPolicyEnabled) {
    items.push({
      key: 'refund-policy',
      label: t('Refund Policy'),
      href: '/refund-policy',
    })
  }
  if (items.length === 0) {
    return null
  }
  return (
    <nav aria-labelledby='footer-legal-title' className='min-w-40'>
      <p id='footer-legal-title' className='text-muted-foreground text-sm'>
        {t('Legal')}
      </p>
      <ul className='mt-5 space-y-3.5'>
        {items.map((item) => (
          <li key={item.key}>
            <Link
              to={item.href}
              className='text-foreground/85 hover:text-primary text-[15px] transition-colors duration-200'
            >
              {item.label}
            </Link>
          </li>
        ))}
      </ul>
    </nav>
  )
}

export function Footer(props: FooterProps) {
  const { t } = useTranslation()
  const {
    systemName,
    logo: systemLogo,
    footerHtml,
    demoSiteEnabled,
  } = useSystemConfig()

  const displayLogo =
    systemLogo || (subsiteDocument() ? '' : props.logo || '/logo.png')
  const displayName = systemName || props.name || ADTY_BRAND_NAME
  const isDemoSiteMode = Boolean(demoSiteEnabled)
  const currentYear = new Date().getFullYear()
  const displayColumns = props.columns ?? []

  return (
    <footer
      className={cn('border-border bg-background border-t', props.className)}
    >
      <div className='mx-auto w-full max-w-[1320px] px-5 pt-16 pb-10 sm:px-8 md:px-10 md:pt-20'>
        <div className='flex flex-col justify-between gap-12 md:flex-row md:items-start'>
          <div className='shrink-0'>
            <Link to='/' className='group flex items-center gap-2.5'>
              {/* The default ADTY artwork already carries the wordmark, so the
                  name is not repeated next to it; a site-configured logo keeps
                  the accessible name through `alt`. */}
              {!displayLogo && (
                <span className='text-2xl font-bold tracking-tight'>
                  {displayName}
                </span>
              )}
              {displayLogo === DEFAULT_LOGO && (
                <>
                  <img
                    src={displayLogo}
                    alt={displayName}
                    className='h-10 w-auto max-w-none object-contain dark:hidden'
                  />
                  <img
                    src='/logo-dark.png'
                    alt=''
                    aria-hidden='true'
                    className='hidden h-10 w-auto max-w-none object-contain dark:block'
                  />
                </>
              )}
              {displayLogo && displayLogo !== DEFAULT_LOGO && (
                <img
                  src={displayLogo}
                  alt={displayName}
                  className='h-10 w-auto max-w-none object-contain dark:rounded-md dark:bg-white/10 dark:px-1 dark:py-0.5'
                />
              )}
            </Link>
            <p className='text-muted-foreground mt-5 max-w-[300px] text-[15px] leading-relaxed'>
              {t('One API key for every model')}
            </p>
          </div>

          <div className='flex flex-wrap gap-12 md:gap-20'>
            {isDemoSiteMode &&
              displayColumns.map((column) => (
                <div key={column.title}>
                  <p className='text-muted-foreground text-sm'>
                    {t(column.title)}
                  </p>
                  <ul className='mt-5 space-y-3.5'>
                    {column.links.map((link) => (
                      <li key={`${link.href}:${link.text}`}>
                        <FooterLinkItem link={link} />
                      </li>
                    ))}
                  </ul>
                </div>
              ))}
            <PartnershipColumn />
            <LegalColumn />
          </div>
        </div>

        {footerHtml && (
          <HtmlContent
            content={footerHtml}
            className='custom-footer text-muted-foreground mt-10 text-[13px]'
          />
        )}

        <p className='border-border text-muted-foreground mt-14 border-t pt-6 text-center text-xs'>
          &copy; {currentYear} {displayName}.{' '}
          {props.copyright ?? t('footer.defaultCopyright')}
        </p>
      </div>
    </footer>
  )
}
