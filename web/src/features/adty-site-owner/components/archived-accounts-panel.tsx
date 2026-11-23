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
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { useVerifiedOwnerMutation } from '../hooks/use-verified-owner-mutation'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { PageSwitcher } from '@/features/referral/components/page-switcher'

import {
  getArchivedSites,
  getArchivedWebsiteDomains,
  removeArchivedDomain,
  getOwnerEarnings,
  siteOwnerKeys,
  OWNER_PAGE_SIZE,
} from '../api'
import { earningKindLabel } from '../lib/site-owner'
import { SettlementSheetPanel } from './settlement-sheet-panel'
import { WithdrawalPanel } from './withdrawal-panel'

function ArchiveLedger({ siteId }: { siteId: number }) {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const earnings = useQuery({
    queryKey: siteOwnerKeys.list('archived-earnings', { siteId, page }),
    queryFn: () => getOwnerEarnings(page, '', siteId),
  })
  const data = earnings.data
  if (!data) {
    return earnings.isError ? (
      <ErrorState onRetry={() => void earnings.refetch()} />
    ) : (
      <LoadingState />
    )
  }
  return (
    <section>
      <h2>{t('Earnings')} (USD)</h2>
      {data.balances.requires_review && (
        <p>
          {t('Contact the platform to reconcile this historical site account')}
        </p>
      )}
      <dl>
        {[
          [t('Pending settlement'), data.balances.pending_usd],
          [
            t('Withdrawable'),
            data.balances.requires_review ? '—' : data.balances.available_usd,
          ],
          [
            t('Frozen'),
            data.balances.requires_review ? '—' : data.balances.frozen_usd,
          ],
          [t('Withdrawn'), data.balances.withdrawn_usd],
        ].map(([label, value]) => (
          <div className='flex gap-4' key={label}>
            <dt>{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
      <table>
        <thead>
          <tr>
            <th>{t('Date')}</th>
            <th>{t('Type')}</th>
            <th>{t('Earnings')} (USD)</th>
          </tr>
        </thead>
        <tbody>
          {(data.page.items ?? []).map((row) => (
            <tr key={row.id}>
              <td>{row.day}</td>
              <td>{earningKindLabel(t, row.kind)}</td>
              <td>{row.earning_usd}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <PageSwitcher
        page={page}
        pageSize={OWNER_PAGE_SIZE}
        total={data.page.total}
        onPageChange={setPage}
      />
    </section>
  )
}

function ArchivedDomains({siteId}: {siteId:number}) {
 const {t} = useTranslation()
 const domains = useQuery({queryKey:siteOwnerKeys.list('archived-domains',{siteId}),queryFn:()=>getArchivedWebsiteDomains(siteId)})
 const remove = useVerifiedOwnerMutation<number>({action:'domain.delete',target:id=>id,run:(id,proof)=>removeArchivedDomain(siteId,id,proof),successMessage:t('Deleted successfully')})
 if (domains.isError) return <ErrorState onRetry={()=>void domains.refetch()} />
 if (!domains.data?.length) return null
 return <section><h2>{t('Custom domains')}</h2><ul>{domains.data.map(domain=><li key={domain.id} className='flex items-center gap-3'>{domain.hostname}<Button variant='outline' disabled={remove.isPending} onClick={()=>remove.mutate(domain.id)}>{t('Remove domain')}</Button></li>)}</ul></section>
}

export function ArchivedAccountsPanel() {
  const { t } = useTranslation()
  const [selected, setSelected] = useState(0)
  const sites = useQuery({
    queryKey: siteOwnerKeys.list('archived-sites', {}),
    queryFn: getArchivedSites,
  })
  const siteId = selected || sites.data?.[0]?.id
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Historical site accounts')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        {sites.isError && <ErrorState onRetry={() => void sites.refetch()} />}
        {sites.isPending && <LoadingState />}
        {sites.data && !sites.data.length && <p>{t('No data yet')}</p>}
        {siteId && (
          <div className='flex max-w-4xl flex-col gap-6' key={siteId}>
            <NativeSelect
              aria-label={t('Site')}
              value={String(siteId)}
              onChange={(e) => setSelected(Number(e.target.value))}
            >
              {sites.data?.map((site) => (
                <NativeSelectOption value={site.id} key={site.id}>
                  {site.subdomain}.{site.root_domain}
                </NativeSelectOption>
              ))}
            </NativeSelect>
            <ArchivedDomains siteId={siteId} />
            <ArchiveLedger siteId={siteId} />
            <WithdrawalPanel key={siteId} siteId={siteId} />
            <SettlementSheetPanel siteId={siteId} />
          </div>
        )}
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
