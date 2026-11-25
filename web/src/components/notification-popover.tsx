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
import type { TFunction } from 'i18next'
import { Bell, Megaphone } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { RichContent } from '@/components/rich-content'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import {
  Popover,
  PopoverContent,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
} from '@/components/ui/popover'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Separator } from '@/components/ui/separator'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { SevereTodoList } from '@/features/adty-notifications/components/todo-item-card'
import {
  isHtmlNotificationContent,
  sanitizeNotificationHtml,
} from '@/features/adty-notifications/lib/rich-text'
import {
  formatNotificationRecipients,
  notificationKindLabels,
} from '@/features/adty-notifications/lib/schema'
import { resolveLocalizedAnnouncement } from '@/features/dashboard/lib/text'
import { getAnnouncementDetail } from '@/lib/api'
import { getAnnouncementColorClass } from '@/lib/colors'
import { formatDateTimeObject } from '@/lib/time'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

interface AnnouncementItem {
  ticket_path?: string
  id?: number | string
  type?: string
  content?: string
  content_en?: string
  content_format?: string
  english?: string
  source?: string
  translation_status?: string
  extra?: string
  publishDate?: string | Date
  title?: string
  title_en?: string
  publisher_name?: string
  kind?: keyof typeof notificationKindLabels
  audience?: string
  target_audiences?: string[]
  target_site_ids?: number[]
  target_site_names?: string[]
  target_site_name?: string
  target_team_id?: number
  target_user_id?: number
  business?: string
  read_key?: string
}

interface NotificationPopoverProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  unreadCount: number
  activeTab: 'notice' | 'announcements'
  onTabChange: (tab: 'notice' | 'announcements') => void
  notice: string
  announcements: AnnouncementItem[]
  loading: boolean
  error?: unknown
  onRetry?: () => void
  className?: string
  hasMore?: boolean
  loadingMore?: boolean
  onLoadMore?: () => void
  /** Signed-in readers can hide one announcement from their own inbox. */
  onHideAnnouncement?: (readKey: string) => void
  hidingAnnouncement?: string
}

/**
 * Get relative time string from a date
 */
function getRelativeTime(publishDate: string | Date, t: TFunction): string {
  if (!publishDate) return ''

  const now = new Date()
  const pubDate = new Date(publishDate)

  // If invalid date, return original string
  if (Number.isNaN(pubDate.getTime())) {
    return typeof publishDate === 'string' ? publishDate : ''
  }

  const diffMs = now.getTime() - pubDate.getTime()
  const diffSeconds = Math.floor(diffMs / 1000)
  const diffMinutes = Math.floor(diffSeconds / 60)
  const diffHours = Math.floor(diffMinutes / 60)
  const diffDays = Math.floor(diffHours / 24)
  const diffWeeks = Math.floor(diffDays / 7)
  const diffMonths = Math.floor(diffDays / 30)
  const diffYears = Math.floor(diffDays / 365)

  // If future time, show specific date
  if (diffMs < 0) return formatDateTimeObject(pubDate)

  // Return relative time based on difference
  if (diffSeconds < 60) return t('Just now')
  if (diffMinutes < 60) {
    return diffMinutes === 1
      ? t('1 minute ago')
      : t('{{count}} minutes ago', { count: diffMinutes })
  }
  if (diffHours < 24) {
    return diffHours === 1
      ? t('1 hour ago')
      : t('{{count}} hours ago', { count: diffHours })
  }
  if (diffDays < 7) {
    return diffDays === 1
      ? t('1 day ago')
      : t('{{count}} days ago', { count: diffDays })
  }
  if (diffWeeks < 4) {
    return diffWeeks === 1
      ? t('1 week ago')
      : t('{{count}} weeks ago', { count: diffWeeks })
  }
  if (diffMonths < 12) {
    return diffMonths === 1
      ? t('1 month ago')
      : t('{{count}} months ago', { count: diffMonths })
  }
  if (diffYears < 2) return t('1 year ago')

  // Over 2 years, show specific date
  return formatDateTimeObject(pubDate)
}

/**
 * Announcement status dot indicator
 */
function AnnouncementDot({ type }: { type?: string }) {
  return (
    <span
      className={cn(
        'mt-1.5 inline-block size-2 shrink-0 rounded-full',
        getAnnouncementColorClass(type)
      )}
    />
  )
}

function AnnouncementDetailButton(props: { readKey: string }) {
  const { t, i18n } = useTranslation()
  const currentLanguage = i18n.resolvedLanguage || i18n.language
  const [open, setOpen] = useState(false)
  const detail = useQuery({
    queryKey: ['adty-announcement', props.readKey],
    queryFn: () => getAnnouncementDetail(props.readKey),
    enabled: open,
  })
  const detailData = detail.data
  const { title: resolvedDetailTitle, content } = resolveLocalizedAnnouncement(
    detailData
      ? {
          id:
            typeof detailData.id === 'string' ||
            typeof detailData.id === 'number'
              ? detailData.id
              : undefined,
          title:
            typeof detailData.title === 'string' ? detailData.title : undefined,
          title_en:
            typeof detailData.title_en === 'string'
              ? detailData.title_en
              : undefined,
          content:
            typeof detailData.content === 'string'
              ? detailData.content
              : undefined,
          content_en:
            typeof detailData.content_en === 'string'
              ? detailData.content_en
              : undefined,
          english:
            typeof detailData.english === 'string'
              ? detailData.english
              : undefined,
          source:
            typeof detailData.source === 'string'
              ? detailData.source
              : undefined,
          translation_status:
            typeof detailData.translation_status === 'string'
              ? detailData.translation_status
              : undefined,
          content_format:
            typeof detailData.content_format === 'string'
              ? detailData.content_format
              : undefined,
        }
      : null,
    currentLanguage
  )
  const detailTitle = resolvedDetailTitle || t('Details')
  const detailFormat =
    typeof detailData?.content_format === 'string'
      ? detailData.content_format
      : undefined
  const detailMode = isHtmlNotificationContent(content, detailFormat)
    ? 'html'
    : 'markdown'
  const safeContent =
    detailMode === 'html' ? sanitizeNotificationHtml(content) : content
  return (
    <>
      <Button
        type='button'
        size='sm'
        variant='outline'
        onClick={() => setOpen(true)}
      >
        {t('Details')}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{detailTitle}</DialogTitle>
          </DialogHeader>
          {detail.isPending && <p className='text-sm'>{t('Loading...')}</p>}
          {detail.isError && (
            <ErrorState onRetry={() => void detail.refetch()} />
          )}
          {detail.isSuccess && (
            <RichContent mode={detailMode} breaks content={safeContent} />
          )}
        </DialogContent>
      </Dialog>
    </>
  )
}

function getAnnouncementRenderKey(announcement: AnnouncementItem): string {
  if (announcement.id !== undefined && announcement.id !== null) {
    return `id:${announcement.id}`
  }

  return JSON.stringify({
    content: announcement.content ?? '',
    extra: announcement.extra ?? '',
    publishDate: announcement.publishDate ?? '',
    type: announcement.type ?? '',
  })
}

/**
 * Empty state component
 */
function EmptyState({
  icon,
  title,
  description,
}: {
  icon: React.ReactNode
  title: string
  description?: string
}) {
  return (
    <Empty className='min-h-48 border-0 p-4'>
      <EmptyHeader>
        <EmptyMedia variant='icon'>{icon}</EmptyMedia>
        <EmptyTitle>{title}</EmptyTitle>
        {description ? (
          <EmptyDescription>{description}</EmptyDescription>
        ) : null}
      </EmptyHeader>
    </Empty>
  )
}

/**
 * Notice tab content
 */
function NoticeContent({
  notice,
  loading,
  t,
}: {
  notice: string
  loading: boolean
  t: TFunction
}) {
  if (loading) {
    return (
      <EmptyState
        icon={<Bell />}
        title={t('Loading...')}
        description={t('Latest platform updates and notices')}
      />
    )
  }

  if (!notice) {
    return (
      <EmptyState icon={<Bell />} title={t('No announcements at this time')} />
    )
  }

  return (
    <ScrollArea className='h-[min(52vh,28rem)] pr-3'>
      <RichContent breaks content={notice} />
    </ScrollArea>
  )
}

/**
 * Announcements tab content
 */
function AnnouncementsContent({
  announcements,
  loading,
  t,
  language,
  onHide,
  hiding,
}: {
  announcements: AnnouncementItem[]
  loading: boolean
  t: TFunction
  language?: string
  onHide?: (readKey: string) => void
  hiding?: string
}) {
  if (loading) {
    return (
      <EmptyState
        icon={<Megaphone />}
        title={t('Loading...')}
        description={t('Latest platform updates and notices')}
      />
    )
  }

  if (announcements.length === 0) {
    return (
      <EmptyState icon={<Megaphone />} title={t('No system announcements')} />
    )
  }

  return (
    <ScrollArea className='h-[min(52vh,28rem)] pr-3'>
      <div className='flex flex-col'>
        {announcements.map((item, idx) => {
          const announcementKey = getAnnouncementRenderKey(item)
          const publishDate = item.publishDate
            ? new Date(item.publishDate)
            : null
          const relativeTime = publishDate
            ? getRelativeTime(publishDate, t)
            : ''
          const absoluteTime = publishDate
            ? formatDateTimeObject(publishDate)
            : ''

          const {
            title: displayTitle,
            content: displayContent,
            extra: displayExtra,
          } = resolveLocalizedAnnouncement(item, language)
          const contentMode = isHtmlNotificationContent(
            displayContent,
            item.content_format
          )
            ? 'html'
            : 'markdown'
          const safeDisplayContent =
            contentMode === 'html'
              ? sanitizeNotificationHtml(displayContent)
              : displayContent
          const recipientSummary = formatNotificationRecipients(item, t)

          return (
            <div key={announcementKey}>
              <div className='py-3'>
                <div className='flex items-start gap-3'>
                  <AnnouncementDot type={item.type} />
                  <div className='flex min-w-0 flex-1 flex-col gap-2'>
                    {displayTitle && (
                      <div className='text-sm font-semibold break-words'>
                        {displayTitle}
                      </div>
                    )}
                    {item.kind && recipientSummary && (
                      <div className='text-muted-foreground text-xs break-words'>
                        {t(notificationKindLabels[item.kind])} ·{' '}
                        {t('Publisher')}: {item.publisher_name} ·{' '}
                        {t('Recipients')}: {recipientSummary}
                      </div>
                    )}
                    <div className='text-sm'>
                      <RichContent
                        mode={contentMode}
                        breaks
                        content={safeDisplayContent}
                      />
                    </div>

                    {item.ticket_path &&
                      /^\/(profile|support|admin-tickets|site-admin\/tickets|site-owner\/tickets|site-staff)#case-\d+(?:-team)?$/.test(
                        item.ticket_path
                      ) && (
                        <a
                          href={item.ticket_path}
                          onClick={(event) => {
                            const { pathname, hash } = event.currentTarget
                            if (
                              pathname === location.pathname &&
                              hash === location.hash
                            ) {
                              window.dispatchEvent(
                                new HashChangeEvent('hashchange')
                              )
                            }
                          }}
                          className='text-primary text-sm underline'
                        >
                          {t('Open ticket')}
                        </a>
                      )}
                    {displayExtra ? (
                      <div className='text-muted-foreground text-xs'>
                        <RichContent breaks content={displayExtra} />
                      </div>
                    ) : null}
                    {item.business && (
                      <div className='text-muted-foreground text-xs break-words'>
                        {t('Related business')}: {item.business}
                      </div>
                    )}

                    {item.read_key && (
                      <div className='flex flex-wrap gap-2'>
                        <AnnouncementDetailButton readKey={item.read_key} />
                        {onHide && (
                          <Button
                            type='button'
                            size='sm'
                            variant='ghost'
                            disabled={hiding === item.read_key}
                            onClick={() => onHide(String(item.read_key))}
                          >
                            {t('Hide')}
                          </Button>
                        )}
                      </div>
                    )}
                    {absoluteTime ? (
                      <div className='text-muted-foreground text-xs'>
                        {relativeTime ? `${relativeTime} • ` : null}
                        {absoluteTime}
                      </div>
                    ) : null}
                  </div>
                </div>
              </div>
              {idx < announcements.length - 1 ? <Separator /> : null}
            </div>
          )
        })}
      </div>
    </ScrollArea>
  )
}

/**
 * Notification popover with Notice and Announcements tabs
 */
export function NotificationPopover({
  open,
  onOpenChange,
  unreadCount,
  activeTab,
  onTabChange,
  notice,
  announcements,
  loading,
  error,
  onRetry,
  className,
  hasMore,
  loadingMore,
  onLoadMore,
  onHideAnnouncement,
  hidingAnnouncement,
}: NotificationPopoverProps) {
  const { t, i18n } = useTranslation()
  const staffSiteId = useAuthStore((state) => state.auth.user?.staff_site_id ?? 0)
  const currentLanguage = i18n.resolvedLanguage || i18n.language
  return (
    <Popover open={open} onOpenChange={onOpenChange}>
      <PopoverTrigger
        render={
          <Button
            variant='ghost'
            size='icon'
            className={cn('relative size-9', className)}
            aria-label={t('Notifications')}
          />
        }
      >
        <Bell className='size-[1.2rem]' />
        {unreadCount > 0 ? (
          <Badge
            variant='destructive'
            className='absolute -top-1 -right-1 flex h-5 min-w-5 items-center justify-center px-1 text-[10px] font-semibold tabular-nums'
          >
            {unreadCount > 99 ? '99+' : unreadCount}
          </Badge>
        ) : null}
      </PopoverTrigger>

      <PopoverContent
        align='end'
        sideOffset={8}
        className='w-[min(26rem,calc(100vw-1rem))] gap-3 p-3'
      >
        <PopoverHeader className='gap-1 px-1'>
          <PopoverTitle>{t('System Announcements')}</PopoverTitle>
          <p className='text-muted-foreground text-xs'>
            {t('Latest platform updates and notices')}
          </p>
        </PopoverHeader>
        {open && onHideAnnouncement && staffSiteId <= 0 ? (
          <SevereTodoList />
        ) : null}

        {error ? (
          <ErrorState onRetry={onRetry} className='min-h-48' />
        ) : (
          <Tabs
            value={activeTab}
            onValueChange={onTabChange as (value: string) => void}
          >
            <TabsList className='grid w-full grid-cols-2'>
              <TabsTrigger value='notice' className='gap-1.5'>
                <Bell className='size-3.5' />
                {t('Notice')}
              </TabsTrigger>
              <TabsTrigger value='announcements' className='gap-1.5'>
                <Megaphone className='size-3.5' />
                {t('Timeline')}
              </TabsTrigger>
            </TabsList>

            <TabsContent value='notice' className='mt-2'>
              <NoticeContent notice={notice} loading={loading} t={t} />
            </TabsContent>

            <TabsContent value='announcements' className='mt-2'>
              <AnnouncementsContent
                announcements={announcements}
                loading={loading}
                t={t}
                language={currentLanguage}
                onHide={onHideAnnouncement}
                hiding={hidingAnnouncement}
              />
              {hasMore && (
                <Button
                  className='mt-2 w-full'
                  size='sm'
                  variant='outline'
                  disabled={loadingMore}
                  onClick={onLoadMore}
                >
                  {t(loadingMore ? 'Loading...' : 'Load more')}
                </Button>
              )}
            </TabsContent>
          </Tabs>
        )}

        <div className='flex justify-end'>
          <Button size='sm' onClick={() => onOpenChange(false)}>
            {t('Close')}
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  )
}
