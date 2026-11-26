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
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { RichContent } from '@/components/rich-content'
import { ScrollArea } from '@/components/ui/scroll-area'
import {
  isHtmlNotificationContent,
  sanitizeNotificationHtml,
} from '@/features/adty-notifications/lib/rich-text'
import { resolveLocalizedAnnouncement } from '@/features/dashboard/lib/text'
import { formatDateTimeObject } from '@/lib/time'

interface AnnouncementDetailModalProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  announcement: {
    title?: string
    title_en?: string
    content?: string
    content_en?: string
    english?: string
    source?: string
    translation_status?: string
    translation_notice?: string
    content_format?: string
    tag?: string
    publishDate?: string
    extra?: string
  } | null
}

export function AnnouncementDetailModal({
  open,
  onOpenChange,
  announcement,
}: AnnouncementDetailModalProps) {
  const { t, i18n } = useTranslation()
  const currentLanguage = i18n.resolvedLanguage || i18n.language
  const {
    title: displayTitle,
    content: rawContent,
    extra: displayExtra,
    translationNotReady,
  } = resolveLocalizedAnnouncement(announcement, currentLanguage)
  const contentMode = isHtmlNotificationContent(
    rawContent,
    announcement?.content_format
  )
    ? 'html'
    : 'markdown'
  const safeContent =
    contentMode === 'html' ? sanitizeNotificationHtml(rawContent) : rawContent
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={displayTitle || t('Announcement Details')}
      description={
        announcement?.publishDate
          ? `${t('Published:')} ${formatDateTimeObject(new Date(announcement.publishDate))}`
          : undefined
      }
      contentClassName='sm:max-w-lg'
      contentHeight='auto'
      bodyClassName='space-y-4'
    >
      <ScrollArea className='max-h-[min(58vh,520px)] pr-4'>
        <div className='space-y-4'>
          {(safeContent || translationNotReady) && (
            <div className='space-y-2'>
              <h4 className='font-medium'>{t('Content')}</h4>
              {safeContent && (
                <RichContent mode={contentMode} breaks content={safeContent} />
              )}
              {translationNotReady && (
                <p className='text-muted-foreground text-xs'>
                  {t('English is not ready')}
                </p>
              )}
            </div>
          )}
          {displayExtra && (
            <div>
              <h4 className='mb-2 font-medium'>
                {t('Additional Information')}
              </h4>
              <RichContent
                breaks
                content={displayExtra}
                className='text-muted-foreground'
              />
            </div>
          )}
        </div>
      </ScrollArea>
    </Dialog>
  )
}
