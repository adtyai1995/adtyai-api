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
import { AlertTriangle, Check, Copy, Download, FileText } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { voucherPurposeLabel } from '@/features/adty-ledger/voucher-labels'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'

import {
  buildVoucherExportFilename,
  downloadVoucherTextFile,
  formatVoucherBatchCsv,
  formatVoucherBatchTxt,
  type NormalizedVoucherBatch,
} from './export'

export function VoucherBatchResultDialog(props: {
  batch: NormalizedVoucherBatch | null
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const { copiedText, copyToClipboard } = useCopyToClipboard({
    notify: true,
    successMessage: t('All codes copied to clipboard'),
  })

  if (!props.batch) return null

  const batch = props.batch
  const allCodesText = formatVoucherBatchTxt(batch)
  const isAllCopied = copiedText === allCodesText && allCodesText !== ''

  const handleExportTxt = () => {
    const filename = buildVoucherExportFilename(batch.batch_no, 'txt')
    downloadVoucherTextFile(
      `${allCodesText}\n`,
      filename,
      'text/plain;charset=utf-8'
    )
    toast.success(t('Exported {{filename}}', { filename }))
  }

  const handleExportCsv = () => {
    const filename = buildVoucherExportFilename(batch.batch_no, 'csv')
    downloadVoucherTextFile(
      formatVoucherBatchCsv(batch),
      filename,
      'text/csv;charset=utf-8'
    )
    toast.success(t('Exported {{filename}}', { filename }))
  }

  const hasFundingSplit =
    batch.frozen_earnings_usd !== undefined ||
    batch.frozen_margin_usd !== undefined

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Generated redemption codes')}
      description={t(
        'Save or export these plaintext codes now before closing this window.'
      )}
      contentClassName='sm:max-w-2xl'
      footer={
        <Button
          type='button'
          variant='brand'
          onClick={() => props.onOpenChange(false)}
        >
          {t('Done')}
        </Button>
      }
    >
      <div className='flex flex-col gap-4'>
        <Alert className='border-warning/40 bg-warning-soft text-warning'>
          <AlertTriangle className='size-4' aria-hidden='true' />
          <AlertTitle>{t('One-time plaintext display')}</AlertTitle>
          <AlertDescription className='text-warning/90'>
            {t(
              'Plaintext codes are shown only once and cannot be recovered after closing this dialog. Copy or export them immediately.'
            )}
          </AlertDescription>
        </Alert>

        <dl className='bg-muted/30 border-border grid grid-cols-2 gap-3 rounded-xl border p-3.5 text-sm sm:grid-cols-4'>
          <div className='flex flex-col gap-0.5'>
            <dt className='text-muted-foreground text-xs'>{t('Quantity')}</dt>
            <dd className='font-semibold tabular-nums'>{batch.count}</dd>
          </div>
          <div className='flex flex-col gap-0.5'>
            <dt className='text-muted-foreground text-xs'>
              {t('Unit amount (USD)')}
            </dt>
            <dd className='font-semibold tabular-nums'>
              ${batch.amount_usd}
            </dd>
          </div>
          <div className='flex flex-col gap-0.5'>
            <dt className='text-muted-foreground text-xs'>
              {t('Total amount (USD)')}
            </dt>
            <dd className='font-semibold tabular-nums'>
              ${batch.total_amount_usd}
            </dd>
          </div>
          <div className='flex flex-col gap-0.5'>
            <dt className='text-muted-foreground text-xs'>{t('Purpose')}</dt>
            <dd className='font-medium'>
              {voucherPurposeLabel(t, batch.purpose)}
            </dd>
          </div>
          {batch.batch_no && (
            <div className='col-span-2 flex flex-col gap-0.5 sm:col-span-2'>
              <dt className='text-muted-foreground text-xs'>
                {t('Batch number')}
              </dt>
              <dd className='flex items-center gap-1.5 font-mono text-xs'>
                <span className='truncate'>{batch.batch_no}</span>
                <CopyButton
                  value={batch.batch_no}
                  size='icon'
                  className='size-6'
                  iconClassName='size-3'
                  aria-label={t('Copy batch number')}
                />
              </dd>
            </div>
          )}
          {batch.target_site_label && (
            <div className='col-span-2 flex flex-col gap-0.5 sm:col-span-2'>
              <dt className='text-muted-foreground text-xs'>
                {t('Target site')}
              </dt>
              <dd className='truncate text-xs font-medium'>
                {batch.target_site_label}
              </dd>
            </div>
          )}
          {hasFundingSplit && (
            <div className='col-span-2 flex flex-col gap-0.5 sm:col-span-4'>
              <dt className='text-muted-foreground text-xs'>
                {t('Funding source')}
              </dt>
              <dd className='text-muted-foreground text-xs tabular-nums'>
                {t(
                  'Frozen split: Earnings ${{earnings}} + Excess margin ${{margin}}',
                  {
                    earnings: batch.frozen_earnings_usd ?? '0',
                    margin: batch.frozen_margin_usd ?? '0',
                  }
                )}
              </dd>
            </div>
          )}
        </dl>

        <div className='flex flex-wrap items-center gap-2'>
          <Button
            type='button'
            variant='brand'
            size='sm'
            onClick={() => void copyToClipboard(allCodesText)}
          >
            {isAllCopied ? (
              <Check className='size-3.5' aria-hidden='true' />
            ) : (
              <Copy className='size-3.5' aria-hidden='true' />
            )}
            {t('Copy All')}
          </Button>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={handleExportTxt}
          >
            <FileText className='size-3.5' aria-hidden='true' />
            {t('Export TXT')}
          </Button>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={handleExportCsv}
          >
            <Download className='size-3.5' aria-hidden='true' />
            {t('Export CSV')}
          </Button>
        </div>

        <div
          aria-label={t('Generated redemption codes')}
          className='border-border divide-border max-h-72 divide-y overflow-y-auto rounded-xl border'
        >
          {batch.items.map((item, index) => (
            <div
              key={item.id || `${item.code}-${index}`}
              className='flex items-center justify-between gap-3 px-3.5 py-2.5'
            >
              <div className='flex min-w-0 flex-1 items-center gap-2.5'>
                <span className='text-muted-foreground w-7 shrink-0 text-xs tabular-nums'>
                  #{index + 1}
                </span>
                <code className='min-w-0 flex-1 font-mono text-xs break-all select-all sm:text-sm'>
                  {item.code}
                </code>
                <Badge variant='outline' className='shrink-0 tabular-nums'>
                  ${item.amount_usd}
                </Badge>
              </div>
              <CopyButton
                value={item.code}
                size='icon'
                className='size-7'
                iconClassName='size-3.5'
                variant='outline'
                aria-label={t('Copy voucher code')}
              />
            </div>
          ))}
        </div>
      </div>
    </Dialog>
  )
}
