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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Check,
  ChevronDown,
  ChevronRight,
  Globe,
  Loader2,
  Plus,
  RefreshCw,
  Save,
  Search,
  Sparkles,
  Trash2,
  X,
} from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ScrollArea } from '@/components/ui/scroll-area'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  adminGroupsQueryKey,
  getAdminGroups,
  type AdminGroup,
} from '@/features/adty-groups/api'
import { getEnabledModels } from '@/features/channels/api'
import {
  getUptimeMonitors,
  UPTIME_STATUS_QUERY_KEY,
} from '@/features/dashboard/api'
import type { UptimeKumaGroupConfig } from '@/features/dashboard/types'
import { getPricing } from '@/features/pricing/api'
import { pickContentText, toContentLang } from '@/lib/adty-lang'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'

import { SettingsSwitchField } from '../components/settings-form-layout'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

interface EditableUptimeGroup {
  localId: string
  groupId: string
  categoryName: string
  url: string
  slug: string
  description: string
  models: string[]
  monitorMap: Record<string, string>
}

interface UptimeKumaSectionProps {
  enabled: boolean
  baseUrl: string
  data: string
}

const SLUG_PATTERN = /^[a-zA-Z0-9_-]+$/
const MAX_GROUPS = 50
const MAX_MODELS_PER_GROUP = 500
const AUTO_MONITOR_VALUE = '__auto__'

function isValidHttpUrl(value: string): boolean {
  try {
    const parsed = new URL(value)
    return (
      (parsed.protocol === 'http:' || parsed.protocol === 'https:') &&
      Boolean(parsed.host)
    )
  } catch {
    return false
  }
}

function parseGroups(raw: string): EditableUptimeGroup[] {
  try {
    const parsed: unknown = JSON.parse(raw || '[]')
    if (!Array.isArray(parsed)) return []
    return parsed
      .filter(
        (item): item is Record<string, unknown> =>
          typeof item === 'object' && item !== null
      )
      .map((item, idx) => {
        const models = Array.isArray(item.models)
          ? item.models
              .filter((m): m is string => typeof m === 'string')
              .map((m) => m.trim())
              .filter(Boolean)
          : []
        const rawMap = item.monitorMap
        const monitorMap: Record<string, string> = {}
        if (rawMap && typeof rawMap === 'object' && !Array.isArray(rawMap)) {
          for (const [k, v] of Object.entries(
            rawMap as Record<string, unknown>
          )) {
            if (typeof v === 'string' && k.trim() && v.trim()) {
              monitorMap[k.trim()] = v.trim()
            }
          }
        }
        return {
          localId: `group-${idx}-${String(item.groupId ?? item.categoryName ?? idx)}`,
          groupId: typeof item.groupId === 'string' ? item.groupId.trim() : '',
          categoryName:
            typeof item.categoryName === 'string'
              ? item.categoryName.trim()
              : '',
          url: typeof item.url === 'string' ? item.url.trim() : '',
          slug: typeof item.slug === 'string' ? item.slug.trim() : '',
          description:
            typeof item.description === 'string' ? item.description.trim() : '',
          models: [...new Set(models)],
          monitorMap,
        }
      })
  } catch {
    return []
  }
}

function serializeGroups(
  groups: EditableUptimeGroup[],
  globalBaseUrl: string
): UptimeKumaGroupConfig[] {
  const trimmedBase = globalBaseUrl.trim()
  return groups.map((group) => {
    const cleanedModels = [
      ...new Set(group.models.map((m) => m.trim()).filter(Boolean)),
    ]
    const modelSet = new Set(cleanedModels)
    const cleanedMap: Record<string, string> = {}
    for (const [modelName, monitorName] of Object.entries(group.monitorMap)) {
      const m = modelName.trim()
      const mon = monitorName.trim()
      if (m && mon && modelSet.has(m) && mon !== m) {
        cleanedMap[m] = mon
      }
    }
    const effectiveUrl = group.url.trim() || trimmedBase
    const payload: UptimeKumaGroupConfig = {
      categoryName: group.categoryName.trim(),
      url: effectiveUrl,
      slug: group.slug.trim(),
    }
    if (group.groupId.trim()) {
      payload.groupId = group.groupId.trim()
    }
    if (group.description.trim()) {
      payload.description = group.description.trim()
    }
    if (cleanedModels.length > 0) {
      payload.models = cleanedModels
    }
    if (Object.keys(cleanedMap).length > 0) {
      payload.monitorMap = cleanedMap
    }
    return payload
  })
}

function makeUniqueCategoryName(
  baseName: string,
  existingNames: Set<string>
): string {
  const trimmed = baseName.trim().slice(0, 50)
  if (!existingNames.has(trimmed)) return trimmed
  for (let i = 2; i < 100; i++) {
    const suffix = ` (${i})`
    const candidate = `${trimmed.slice(0, 50 - suffix.length)}${suffix}`
    if (!existingNames.has(candidate)) return candidate
  }
  return trimmed
}

interface UptimeGroupCardProps {
  group: EditableUptimeGroup
  globalBaseUrl: string
  expanded: boolean
  allModels: string[]
  groupModels: string[]
  fetchedMonitors: string[] | undefined
  isFetchingMonitors: boolean
  onToggleExpand: () => void
  onChange: (next: EditableUptimeGroup) => void
  onDelete: () => void
  onFetchMonitors: () => void
}

function UptimeGroupCard(props: UptimeGroupCardProps) {
  const { t } = useTranslation()
  const [modelSearch, setModelSearch] = useState('')
  const [customModelInput, setCustomModelInput] = useState('')

  const effectiveUrl = (
    props.group.url.trim() || props.globalBaseUrl.trim()
  ).replace(/\/+$/, '')
  const previewSlug = props.group.slug.trim()
  const fetchedMonitors = props.fetchedMonitors
  const monitorLowerSet = useMemo(
    () => new Set((fetchedMonitors ?? []).map((m) => m.toLowerCase())),
    [fetchedMonitors]
  )
  const selectedSet = useMemo(
    () => new Set(props.group.models),
    [props.group.models]
  )

  const selectableModels = useMemo(() => {
    const merged = new Set<string>([...props.allModels, ...props.group.models])
    return [...merged].sort((a, b) => a.localeCompare(b))
  }, [props.allModels, props.group.models])

  const filteredModels = useMemo(() => {
    const query = modelSearch.trim().toLowerCase()
    if (!query) return selectableModels
    return selectableModels.filter((m) => m.toLowerCase().includes(query))
  }, [modelSearch, selectableModels])

  const updateField = <K extends keyof EditableUptimeGroup>(
    key: K,
    value: EditableUptimeGroup[K]
  ) => {
    props.onChange({ ...props.group, [key]: value })
  }

  const toggleModel = (modelName: string, checked: boolean) => {
    if (checked) {
      if (selectedSet.has(modelName)) return
      if (props.group.models.length >= MAX_MODELS_PER_GROUP) {
        toast.error(
          t('Each group can monitor at most {{count}} models', {
            count: MAX_MODELS_PER_GROUP,
          })
        )
        return
      }
      updateField('models', [...props.group.models, modelName])
      return
    }
    const nextMap = { ...props.group.monitorMap }
    delete nextMap[modelName]
    props.onChange({
      ...props.group,
      models: props.group.models.filter((m) => m !== modelName),
      monitorMap: nextMap,
    })
  }

  const handleAddCustomModel = () => {
    const trimmed = customModelInput.trim()
    if (!trimmed) return
    if (selectedSet.has(trimmed)) {
      setCustomModelInput('')
      return
    }
    if (props.group.models.length >= MAX_MODELS_PER_GROUP) {
      toast.error(
        t('Each group can monitor at most {{count}} models', {
          count: MAX_MODELS_PER_GROUP,
        })
      )
      return
    }
    updateField('models', [...props.group.models, trimmed])
    setCustomModelInput('')
  }

  const handleSelectGroupModels = () => {
    if (props.groupModels.length === 0) return
    const next = [...new Set([...props.group.models, ...props.groupModels])]
    updateField('models', next.slice(0, MAX_MODELS_PER_GROUP))
  }

  const handleSelectVisibleModels = () => {
    const next = [...new Set([...props.group.models, ...filteredModels])]
    updateField('models', next.slice(0, MAX_MODELS_PER_GROUP))
  }

  const handleClearModels = () => {
    props.onChange({
      ...props.group,
      models: [],
      monitorMap: {},
    })
  }

  const handleAutoMatchMonitors = () => {
    if (!fetchedMonitors || fetchedMonitors.length === 0) return
    const matched = selectableModels.filter((modelName) =>
      monitorLowerSet.has(modelName.toLowerCase())
    )
    if (matched.length === 0) {
      toast.info(t('No matching system models found for fetched monitors'))
      return
    }
    const next = [...new Set([...props.group.models, ...matched])]
    updateField('models', next.slice(0, MAX_MODELS_PER_GROUP))
    toast.success(
      t('Matched {{count}} models from Uptime Kuma monitors', {
        count: matched.length,
      })
    )
  }

  const handleMonitorOverrideChange = (modelName: string, value: string) => {
    const nextMap = { ...props.group.monitorMap }
    const trimmed = value.trim()
    if (!trimmed || trimmed === AUTO_MONITOR_VALUE || trimmed === modelName) {
      delete nextMap[modelName]
    } else {
      nextMap[modelName] = trimmed
    }
    updateField('monitorMap', nextMap)
  }

  return (
    <div className='border-border bg-card overflow-hidden rounded-[14px] border'>
      <div className='flex flex-wrap items-center justify-between gap-2 px-4 py-3'>
        <button
          type='button'
          onClick={props.onToggleExpand}
          aria-expanded={props.expanded}
          className='flex min-w-0 flex-1 items-center gap-2 text-left'
        >
          {props.expanded ? (
            <ChevronDown className='text-muted-foreground size-4 shrink-0' />
          ) : (
            <ChevronRight className='text-muted-foreground size-4 shrink-0' />
          )}
          <span className='truncate text-sm font-semibold'>
            {props.group.categoryName || t('Untitled Group')}
          </span>
          {props.group.groupId && (
            <Badge variant='outline' className='font-mono text-[11px]'>
              {props.group.groupId}
            </Badge>
          )}
          {previewSlug && (
            <Badge
              variant='secondary'
              className='hidden font-mono text-[11px] sm:inline-flex'
            >
              /status/{previewSlug}
            </Badge>
          )}
          <Badge variant='secondary' className='text-[11px]'>
            {t('{{count}} models', { count: props.group.models.length })}
          </Badge>
        </button>

        <Button
          type='button'
          variant='ghost'
          size='sm'
          onClick={props.onDelete}
          className='text-muted-foreground hover:text-destructive size-8 p-0'
          aria-label={t('Delete')}
        >
          <Trash2 className='size-4' />
        </Button>
      </div>

      {props.expanded && (
        <div className='border-border space-y-5 border-t px-4 py-4'>
          <div className='grid gap-4 md:grid-cols-3'>
            <div className='space-y-1.5'>
              <Label
                htmlFor={`${props.group.localId}-name`}
                className='text-xs font-medium'
              >
                {t('Category Name')}
              </Label>
              <Input
                id={`${props.group.localId}-name`}
                value={props.group.categoryName}
                onChange={(e) => updateField('categoryName', e.target.value)}
                placeholder={t('e.g., Core APIs, OpenAI, Claude')}
                maxLength={50}
              />
            </div>

            <div className='space-y-1.5'>
              <Label
                htmlFor={`${props.group.localId}-url`}
                className='text-xs font-medium'
              >
                {t('Uptime Kuma URL')}
              </Label>
              <Input
                id={`${props.group.localId}-url`}
                value={props.group.url}
                onChange={(e) => updateField('url', e.target.value)}
                placeholder={
                  props.globalBaseUrl.trim() || 'https://status.example.com'
                }
              />
            </div>

            <div className='space-y-1.5'>
              <Label
                htmlFor={`${props.group.localId}-slug`}
                className='text-xs font-medium'
              >
                {t('Status Page Slug')}
              </Label>
              <div className='flex gap-2'>
                <Input
                  id={`${props.group.localId}-slug`}
                  value={props.group.slug}
                  onChange={(e) => updateField('slug', e.target.value)}
                  placeholder='my-status'
                  maxLength={100}
                  className='font-mono text-sm'
                />
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  onClick={props.onFetchMonitors}
                  disabled={
                    props.isFetchingMonitors || !effectiveUrl || !previewSlug
                  }
                  className='shrink-0'
                >
                  {props.isFetchingMonitors ? (
                    <Loader2 className='size-3.5 animate-spin' />
                  ) : (
                    <RefreshCw className='size-3.5' />
                  )}
                  <span className='hidden sm:inline'>
                    {t('Fetch Uptime Kuma monitors')}
                  </span>
                </Button>
              </div>
              {effectiveUrl && previewSlug && (
                <p className='text-muted-foreground truncate font-mono text-[11px]'>
                  {effectiveUrl}/status/{previewSlug}
                </p>
              )}
            </div>
          </div>

          {fetchedMonitors !== undefined && (
            <div className='bg-muted/30 space-y-2 rounded-lg border p-3'>
              <div className='flex flex-wrap items-center justify-between gap-2'>
                <div className='flex items-center gap-2 text-xs font-medium'>
                  <span>{t('Available Uptime Kuma monitors')}</span>
                  <Badge variant='secondary' className='font-mono text-[11px]'>
                    {fetchedMonitors.length}
                  </Badge>
                </div>
                {fetchedMonitors.length > 0 && (
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    className='h-7 gap-1.5 text-xs'
                    onClick={handleAutoMatchMonitors}
                  >
                    <Sparkles className='size-3.5' />
                    {t('Auto-match models')}
                  </Button>
                )}
              </div>
              {fetchedMonitors.length === 0 ? (
                <p className='text-muted-foreground text-xs'>
                  {t('No monitors found on this status page')}
                </p>
              ) : (
                <div className='flex flex-wrap gap-1.5'>
                  {fetchedMonitors.map((monitorName) => (
                    <Badge
                      key={monitorName}
                      variant='outline'
                      className='bg-background font-mono text-[11px]'
                    >
                      {monitorName}
                    </Badge>
                  ))}
                </div>
              )}
            </div>
          )}

          <div className='space-y-3'>
            <div className='flex flex-wrap items-center justify-between gap-2'>
              <div>
                <Label className='text-xs font-semibold'>
                  {t('Monitored Models')}
                </Label>
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Select the models displayed for this group on the status page and pricing table'
                  )}
                </p>
              </div>
              <div className='flex flex-wrap items-center gap-1.5'>
                {props.groupModels.length > 0 && (
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    className='h-7 text-xs'
                    onClick={handleSelectGroupModels}
                  >
                    {t('Select group models ({{count}})', {
                      count: props.groupModels.length,
                    })}
                  </Button>
                )}
                {filteredModels.length > 0 && (
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    className='h-7 text-xs'
                    onClick={handleSelectVisibleModels}
                  >
                    {t('Select visible')}
                  </Button>
                )}
                {props.group.models.length > 0 && (
                  <Button
                    type='button'
                    variant='ghost'
                    size='sm'
                    className='text-muted-foreground h-7 text-xs'
                    onClick={handleClearModels}
                  >
                    {t('Clear')}
                  </Button>
                )}
              </div>
            </div>

            <div className='grid gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,18rem)]'>
              <div className='relative'>
                <Search className='text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2' />
                <Input
                  value={modelSearch}
                  onChange={(e) => setModelSearch(e.target.value)}
                  placeholder={t('Search models...')}
                  className='h-8 pl-8 text-xs'
                />
              </div>
              <div className='flex gap-1.5'>
                <Input
                  value={customModelInput}
                  onChange={(e) => setCustomModelInput(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') {
                      e.preventDefault()
                      handleAddCustomModel()
                    }
                  }}
                  placeholder={t('Add custom model name...')}
                  className='h-8 font-mono text-xs'
                />
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  className='h-8 shrink-0 px-2.5 text-xs'
                  onClick={handleAddCustomModel}
                  disabled={!customModelInput.trim()}
                >
                  <Plus className='size-3.5' />
                </Button>
              </div>
            </div>

            <div className='rounded-lg border'>
              {filteredModels.length === 0 ? (
                <div className='text-muted-foreground py-6 text-center text-xs'>
                  {t('No matching items')}
                </div>
              ) : (
                <ScrollArea className='h-48'>
                  <div className='grid grid-cols-1 gap-1 p-2 sm:grid-cols-2 lg:grid-cols-3'>
                    {filteredModels.map((modelName) => {
                      const isChecked = selectedSet.has(modelName)
                      const matchesFetched =
                        fetchedMonitors !== undefined &&
                        monitorLowerSet.has(modelName.toLowerCase())
                      return (
                        <label
                          key={modelName}
                          className={cn(
                            'hover:bg-muted/60 flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-xs transition-colors',
                            isChecked && 'bg-muted/40'
                          )}
                        >
                          <Checkbox
                            checked={isChecked}
                            onCheckedChange={(checked) =>
                              toggleModel(modelName, checked === true)
                            }
                          />
                          <span
                            className='min-w-0 flex-1 truncate font-mono'
                            title={modelName}
                          >
                            {modelName}
                          </span>
                          {matchesFetched && (
                            <span
                              className='bg-success size-1.5 shrink-0 rounded-full'
                              title={t('Matched in Uptime Kuma')}
                            />
                          )}
                        </label>
                      )
                    })}
                  </div>
                </ScrollArea>
              )}
            </div>

            {props.group.models.length > 0 && (
              <div className='space-y-2'>
                <div className='flex items-center justify-between'>
                  <span className='text-muted-foreground text-xs font-medium'>
                    {t('Selected models & optional monitor name override')} (
                    {props.group.models.length})
                  </span>
                </div>
                <div className='divide-border max-h-64 divide-y overflow-y-auto rounded-lg border'>
                  {props.group.models.map((modelName) => {
                    const override = props.group.monitorMap[modelName] ?? ''
                    const effectiveMonitor = override || modelName
                    const isMatched =
                      fetchedMonitors !== undefined &&
                      monitorLowerSet.has(effectiveMonitor.toLowerCase())
                    return (
                      <div
                        key={modelName}
                        className='flex flex-wrap items-center gap-2 px-3 py-2 text-xs sm:flex-nowrap'
                      >
                        <div className='flex min-w-0 flex-1 items-center gap-2'>
                          <span
                            className='truncate font-mono font-medium'
                            title={modelName}
                          >
                            {modelName}
                          </span>
                          {fetchedMonitors !== undefined && (
                            <StatusBadge
                              variant={isMatched ? 'success' : 'warning'}
                              label={isMatched ? t('Matched') : t('Unmatched')}
                              size='sm'
                              copyable={false}
                            />
                          )}
                        </div>

                        <div className='flex w-full items-center gap-1.5 sm:w-auto'>
                          <Input
                            value={override}
                            onChange={(e) =>
                              handleMonitorOverrideChange(
                                modelName,
                                e.target.value
                              )
                            }
                            placeholder={t(
                              'Monitor name (default: model name)'
                            )}
                            className='h-7 min-w-0 flex-1 font-mono text-xs sm:w-48'
                            aria-label={`${t('Monitor Name Override')}: ${modelName}`}
                          />
                          {fetchedMonitors && fetchedMonitors.length > 0 && (
                            <Select
                              value={
                                override && fetchedMonitors.includes(override)
                                  ? override
                                  : AUTO_MONITOR_VALUE
                              }
                              onValueChange={(value) => {
                                if (value === null) return
                                handleMonitorOverrideChange(modelName, value)
                              }}
                            >
                              <SelectTrigger
                                size='sm'
                                className='h-7 w-32 shrink-0 text-xs'
                              >
                                <SelectValue placeholder={t('Pick monitor')} />
                              </SelectTrigger>
                              <SelectContent>
                                <SelectItem value={AUTO_MONITOR_VALUE}>
                                  {t('Same as model')}
                                </SelectItem>
                                {fetchedMonitors.map((mon) => (
                                  <SelectItem key={mon} value={mon}>
                                    {mon}
                                  </SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                          )}
                          <Button
                            type='button'
                            variant='ghost'
                            size='sm'
                            onClick={() => toggleModel(modelName, false)}
                            className='text-muted-foreground hover:text-destructive size-7 shrink-0 p-0'
                            aria-label={t('Remove')}
                          >
                            <X className='size-3.5' />
                          </Button>
                        </div>
                      </div>
                    )
                  })}
                </div>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

export function UptimeKumaSection(props: UptimeKumaSectionProps) {
  const { t, i18n } = useTranslation()
  const contentLang = toContentLang(i18n.resolvedLanguage || i18n.language)
  const queryClient = useQueryClient()
  const updateOption = useUpdateOption()

  const [isEnabled, setIsEnabled] = useState(props.enabled)
  const [globalBaseUrl, setGlobalBaseUrl] = useState(props.baseUrl)
  const [groups, setGroups] = useState<EditableUptimeGroup[]>(() =>
    parseGroups(props.data)
  )
  const [expandedIds, setExpandedIds] = useState<Record<string, boolean>>({})
  const [baseUrlDirty, setBaseUrlDirty] = useState(false)
  const [groupsDirty, setGroupsDirty] = useState(false)
  const [saving, setSaving] = useState(false)

  const [showAddGroupDialog, setShowAddGroupDialog] = useState(false)
  const [groupSearch, setGroupSearch] = useState('')
  const [deleteTargetId, setDeleteTargetId] = useState<string | null>(null)

  const [fetchedMonitorsByGroup, setFetchedMonitorsByGroup] = useState<
    Record<string, string[]>
  >({})
  const [fetchingMonitorsByGroup, setFetchingMonitorsByGroup] = useState<
    Record<string, boolean>
  >({})

  useEffect(() => {
    setIsEnabled(props.enabled)
  }, [props.enabled])

  useEffect(() => {
    setGlobalBaseUrl(props.baseUrl)
    setBaseUrlDirty(false)
  }, [props.baseUrl])

  useEffect(() => {
    const parsed = parseGroups(props.data)
    setGroups(parsed)
    setGroupsDirty(false)
    setExpandedIds((prev) => {
      if (Object.keys(prev).length > 0) return prev
      const initial: Record<string, boolean> = {}
      if (parsed[0]) initial[parsed[0].localId] = true
      return initial
    })
  }, [props.data])

  const adminGroupsQuery = useQuery({
    queryKey: adminGroupsQueryKey,
    queryFn: getAdminGroups,
    staleTime: 60 * 1000,
  })

  const systemModelsQuery = useQuery({
    queryKey: ['uptime-kuma', 'available-models'],
    queryFn: async () => {
      const [enabledRes, pricingRes] = await Promise.allSettled([
        getEnabledModels(),
        getPricing(),
      ])
      const all = new Set<string>()
      const byGroup = new Map<string, Set<string>>()

      if (
        enabledRes.status === 'fulfilled' &&
        enabledRes.value.success &&
        Array.isArray(enabledRes.value.data)
      ) {
        for (const name of enabledRes.value.data) {
          if (typeof name === 'string' && name.trim()) {
            all.add(name.trim())
          }
        }
      }

      if (
        pricingRes.status === 'fulfilled' &&
        pricingRes.value.success &&
        Array.isArray(pricingRes.value.data)
      ) {
        for (const item of pricingRes.value.data) {
          const modelName = item.model_name?.trim()
          if (!modelName) continue
          all.add(modelName)
          for (const gId of item.enable_groups ?? []) {
            if (!byGroup.has(gId)) {
              byGroup.set(gId, new Set<string>())
            }
            byGroup.get(gId)?.add(modelName)
          }
        }
      }

      const byGroupList: Record<string, string[]> = {}
      for (const [gId, modelSet] of byGroup.entries()) {
        byGroupList[gId] = [...modelSet].sort((a, b) => a.localeCompare(b))
      }

      return {
        allModels: [...all].sort((a, b) => a.localeCompare(b)),
        byGroup: byGroupList,
      }
    },
    staleTime: 60 * 1000,
  })

  const addedGroupIds = useMemo(
    () => new Set(groups.map((g) => g.groupId).filter(Boolean)),
    [groups]
  )

  const availableAdminGroups = useMemo(() => {
    const list = adminGroupsQuery.data ?? []
    const query = groupSearch.trim().toLowerCase()
    return list
      .filter((g) => !addedGroupIds.has(g.id))
      .filter((g) => {
        if (!query) return true
        const displayName = pickContentText(g.names, contentLang).toLowerCase()
        return g.id.toLowerCase().includes(query) || displayName.includes(query)
      })
  }, [addedGroupIds, adminGroupsQuery.data, contentLang, groupSearch])

  const hasChanges = baseUrlDirty || groupsDirty

  const handleToggleEnabled = async (checked: boolean) => {
    try {
      await updateOption.mutateAsync({
        key: 'console_setting.uptime_kuma_enabled',
        value: checked,
      })
      setIsEnabled(checked)
      void queryClient.invalidateQueries({ queryKey: UPTIME_STATUS_QUERY_KEY })
    } catch (error) {
      handleServerError(error, t('Failed to update setting'))
    }
  }

  const handleOpenAddGroup = () => {
    if (groups.length >= MAX_GROUPS) {
      toast.error(
        t('Uptime Kuma groups cannot exceed {{count}} entries', {
          count: MAX_GROUPS,
        })
      )
      return
    }
    setGroupSearch('')
    setShowAddGroupDialog(true)
    void adminGroupsQuery.refetch()
  }

  const handleSelectAdminGroup = (adminGroup: AdminGroup) => {
    const displayName =
      pickContentText(adminGroup.names, contentLang) || adminGroup.id
    const existingNames = new Set(groups.map((g) => g.categoryName.trim()))
    const localId = `group-${Date.now()}-${adminGroup.id}`
    const newGroup: EditableUptimeGroup = {
      localId,
      groupId: adminGroup.id,
      categoryName: makeUniqueCategoryName(displayName, existingNames),
      url: globalBaseUrl.trim(),
      slug: '',
      description: pickContentText(adminGroup.descs, contentLang).slice(0, 200),
      models: [],
      monitorMap: {},
    }
    setGroups((prev) => [...prev, newGroup])
    setExpandedIds((prev) => ({ ...prev, [localId]: true }))
    setGroupsDirty(true)
    setShowAddGroupDialog(false)
  }

  const handleUpdateGroup = (next: EditableUptimeGroup) => {
    setGroups((prev) =>
      prev.map((item) => (item.localId === next.localId ? next : item))
    )
    setGroupsDirty(true)
  }

  const handleConfirmDelete = () => {
    if (!deleteTargetId) return
    setGroups((prev) => prev.filter((item) => item.localId !== deleteTargetId))
    setGroupsDirty(true)
    setDeleteTargetId(null)
  }

  const handleFetchMonitors = async (group: EditableUptimeGroup) => {
    const effectiveUrl = group.url.trim() || globalBaseUrl.trim()
    const slug = group.slug.trim()
    if (!effectiveUrl || !isValidHttpUrl(effectiveUrl)) {
      toast.error(t('Please enter a valid Uptime Kuma URL first'))
      return
    }
    if (!slug || !SLUG_PATTERN.test(slug)) {
      toast.error(t('Please enter a valid status page slug first'))
      return
    }
    setFetchingMonitorsByGroup((prev) => ({ ...prev, [group.localId]: true }))
    try {
      const res = requireServerSuccess(
        await getUptimeMonitors({ url: effectiveUrl, slug })
      )
      const names = Array.isArray(res.data) ? res.data : []
      setFetchedMonitorsByGroup((prev) => ({
        ...prev,
        [group.localId]: names,
      }))
      toast.success(
        t('Fetched {{count}} monitors from Uptime Kuma', {
          count: names.length,
        })
      )
    } catch (error) {
      handleServerError(error, t('Failed to fetch Uptime Kuma monitors'))
    } finally {
      setFetchingMonitorsByGroup((prev) => ({
        ...prev,
        [group.localId]: false,
      }))
    }
  }

  const validateBeforeSave = (): string | null => {
    const trimmedBase = globalBaseUrl.trim()
    if (trimmedBase && !isValidHttpUrl(trimmedBase)) {
      return t('Global Uptime Kuma URL must be a valid http or https URL')
    }
    if (groups.length > MAX_GROUPS) {
      return t('Uptime Kuma groups cannot exceed {{count}} entries', {
        count: MAX_GROUPS,
      })
    }
    const seenNames = new Set<string>()
    for (let idx = 0; idx < groups.length; idx++) {
      const g = groups[idx]
      const label = g.categoryName.trim() || `#${idx + 1}`
      if (!g.categoryName.trim()) {
        return t('Group #{{index}} is missing the category name field', {
          index: idx + 1,
        })
      }
      if (seenNames.has(g.categoryName.trim())) {
        return t('Group #{{index}} category name duplicates another group', {
          index: idx + 1,
        })
      }
      seenNames.add(g.categoryName.trim())
      const effectiveUrl = g.url.trim() || trimmedBase
      if (!effectiveUrl || !isValidHttpUrl(effectiveUrl)) {
        return t('Group "{{name}}" requires a valid Uptime Kuma URL', {
          name: label,
        })
      }
      const slug = g.slug.trim()
      if (!slug || !SLUG_PATTERN.test(slug)) {
        return t('Group "{{name}}" requires a valid status page slug', {
          name: label,
        })
      }
    }
    return null
  }

  const handleSaveAll = async () => {
    const validationError = validateBeforeSave()
    if (validationError) {
      toast.error(validationError)
      return
    }
    setSaving(true)
    try {
      if (baseUrlDirty) {
        await updateOption.mutateAsync({
          key: 'console_setting.uptime_kuma_base_url',
          value: globalBaseUrl.trim(),
        })
        setBaseUrlDirty(false)
      }
      if (groupsDirty) {
        const serialized = serializeGroups(groups, globalBaseUrl)
        await updateOption.mutateAsync({
          key: 'console_setting.uptime_kuma_groups',
          value: JSON.stringify(serialized),
        })
        setGroupsDirty(false)
      }
      void queryClient.invalidateQueries({ queryKey: UPTIME_STATUS_QUERY_KEY })
    } catch (error) {
      handleServerError(error, t('Failed to save Uptime Kuma settings'))
    } finally {
      setSaving(false)
    }
  }

  const allModels = systemModelsQuery.data?.allModels ?? []
  const modelsByGroup = systemModelsQuery.data?.byGroup ?? {}

  let groupDialogBody: React.ReactNode
  if (adminGroupsQuery.isLoading) {
    groupDialogBody = <LoadingState message={t('Loading groups...')} />
  } else if (availableAdminGroups.length === 0) {
    groupDialogBody = (
      <div className='text-muted-foreground rounded-lg border py-8 text-center text-sm'>
        {t('All system groups have been added or no groups match')}
      </div>
    )
  } else {
    groupDialogBody = (
      <ScrollArea className='max-h-72'>
        <div className='space-y-1.5 pr-2'>
          {availableAdminGroups.map((adminGroup) => {
            const displayName =
              pickContentText(adminGroup.names, contentLang) || adminGroup.id
            const desc = pickContentText(adminGroup.descs, contentLang)
            return (
              <button
                key={adminGroup.id}
                type='button'
                onClick={() => handleSelectAdminGroup(adminGroup)}
                className='hover:bg-muted/60 border-border flex w-full items-center justify-between gap-3 rounded-lg border px-3 py-2.5 text-left transition-colors'
              >
                <div className='min-w-0 flex-1'>
                  <div className='flex items-center gap-2'>
                    <span className='truncate text-sm font-medium'>
                      {displayName}
                    </span>
                    {displayName !== adminGroup.id && (
                      <span className='text-muted-foreground font-mono text-xs'>
                        {adminGroup.id}
                      </span>
                    )}
                  </div>
                  {desc && (
                    <p className='text-muted-foreground mt-0.5 truncate text-xs'>
                      {desc}
                    </p>
                  )}
                </div>
                <div className='flex shrink-0 items-center gap-2'>
                  <Badge variant='secondary' className='text-[11px]'>
                    {t('{{count}} channels', {
                      count: adminGroup.refs.channels,
                    })}
                  </Badge>
                  <Check className='text-muted-foreground size-4 opacity-50' />
                </div>
              </button>
            )
          })}
        </div>
      </ScrollArea>
    )
  }

  return (
    <SettingsSection
      title={t('Uptime Kuma')}
      description={t(
        'Connect Uptime Kuma status pages to system groups for 24h model availability monitoring'
      )}
    >
      <div className='space-y-5'>
        <div className='bg-muted/20 flex flex-col gap-4 rounded-[14px] border p-4'>
          <div className='flex flex-wrap items-center justify-between gap-3'>
            <SettingsSwitchField
              checked={isEnabled}
              onCheckedChange={handleToggleEnabled}
              label={t('Enable Uptime Kuma monitoring')}
              description={t(
                'Display 24h connectivity status on Overview, Pricing, and Model Status pages'
              )}
              className='flex-1 py-0'
            />
            <div className='flex flex-wrap items-center gap-2'>
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={handleOpenAddGroup}
              >
                <Plus className='size-4' />
                {t('Add Group')}
              </Button>
              <Button
                type='button'
                variant='brand'
                size='sm'
                onClick={handleSaveAll}
                disabled={!hasChanges || saving || updateOption.isPending}
              >
                <Save className='size-4' />
                {saving ? t('Saving...') : t('Save Settings')}
              </Button>
            </div>
          </div>

          <div className='border-border/60 space-y-1.5 border-t pt-3'>
            <Label
              htmlFor='uptime-kuma-global-base-url'
              className='flex items-center gap-1.5 text-xs font-semibold'
            >
              <Globe className='text-muted-foreground size-3.5' />
              {t('Global Uptime Kuma URL')}
            </Label>
            <Input
              id='uptime-kuma-global-base-url'
              value={globalBaseUrl}
              onChange={(e) => {
                setGlobalBaseUrl(e.target.value)
                setBaseUrlDirty(true)
              }}
              placeholder='https://status.example.com'
              className='max-w-xl font-mono text-sm'
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'Default Uptime Kuma server URL automatically pre-filled when adding groups'
              )}
            </p>
          </div>
        </div>

        {groups.length === 0 ? (
          <EmptyState
            bordered
            title={t('No Uptime Kuma groups configured')}
            description={t(
              'Click "Add Group" to bind a system group to an Uptime Kuma status page.'
            )}
            action={
              <Button type='button' size='sm' onClick={handleOpenAddGroup}>
                <Plus className='size-4' />
                {t('Add Group')}
              </Button>
            }
          />
        ) : (
          <div className='space-y-3'>
            {groups.map((group) => (
              <UptimeGroupCard
                key={group.localId}
                group={group}
                globalBaseUrl={globalBaseUrl}
                expanded={Boolean(expandedIds[group.localId])}
                allModels={allModels}
                groupModels={
                  group.groupId ? (modelsByGroup[group.groupId] ?? []) : []
                }
                fetchedMonitors={fetchedMonitorsByGroup[group.localId]}
                isFetchingMonitors={Boolean(
                  fetchingMonitorsByGroup[group.localId]
                )}
                onToggleExpand={() =>
                  setExpandedIds((prev) => ({
                    ...prev,
                    [group.localId]: !prev[group.localId],
                  }))
                }
                onChange={handleUpdateGroup}
                onDelete={() => setDeleteTargetId(group.localId)}
                onFetchMonitors={() => void handleFetchMonitors(group)}
              />
            ))}
          </div>
        )}
      </div>

      <Dialog
        open={showAddGroupDialog}
        onOpenChange={setShowAddGroupDialog}
        title={t('Select Group')}
        description={t(
          'Choose a system group to configure Uptime Kuma monitoring'
        )}
        contentHeight='auto'
        bodyClassName='space-y-3'
      >
        <div className='relative'>
          <Search className='text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2' />
          <Input
            value={groupSearch}
            onChange={(e) => setGroupSearch(e.target.value)}
            placeholder={t('Search system groups...')}
            className='pl-8'
          />
        </div>

        {groupDialogBody}
      </Dialog>

      <ConfirmDialog
        open={deleteTargetId !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteTargetId(null)
        }}
        title={t('Are you sure?')}
        desc={t('This Uptime Kuma group will be removed from the list.')}
        confirmText={t('Delete')}
        destructive
        handleConfirm={handleConfirmDelete}
      />
    </SettingsSection>
  )
}
