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
  Activity,
  Check,
  ChevronDown,
  ChevronRight,
  Clock,
  Layers,
  Loader2,
  Play,
  Plus,
  Save,
  Search,
  Timer,
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
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Switch } from '@/components/ui/switch'
import {
  adminGroupsQueryKey,
  getAdminGroups,
  type AdminGroup,
} from '@/features/adty-groups/api'
import { getEnabledModels } from '@/features/channels/api'
import {
  triggerUptimeProbe,
  UPTIME_STATUS_QUERY_KEY,
  type UptimeKumaGroupConfig,
  type UptimeProbeResponseData,
} from '@/features/dashboard/api'
import { getPricing } from '@/features/pricing/api'
import { pickContentText, toContentLang } from '@/lib/adty-lang'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'

import { SettingsSwitchField } from '../components/settings-form-layout'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

interface EditableProbeGroup {
  localId: string
  groupId: string
  categoryName: string
  enabled: boolean
  description: string
  models: string[]
}

interface UptimeKumaSectionProps {
  enabled: boolean
  intervalMinutes: number
  timeoutSeconds: number
  data: string
}

const MAX_GROUPS = 50
const MAX_MODELS_PER_GROUP = 500
const DEFAULT_INTERVAL_MINUTES = 10
const MIN_INTERVAL_MINUTES = 1
const MAX_INTERVAL_MINUTES = 60
const DEFAULT_TIMEOUT_SECONDS = 15
const MIN_TIMEOUT_SECONDS = 5
const MAX_TIMEOUT_SECONDS = 60

function clampInteger(
  value: number | undefined,
  fallback: number,
  min: number,
  max: number
): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) return fallback
  const rounded = Math.round(value)
  if (rounded < min || rounded > max) return fallback
  return rounded
}

function parseGroups(raw: string): EditableProbeGroup[] {
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
        return {
          localId: `group-${idx}-${String(item.groupId ?? item.categoryName ?? idx)}`,
          groupId: typeof item.groupId === 'string' ? item.groupId.trim() : '',
          categoryName:
            typeof item.categoryName === 'string'
              ? item.categoryName.trim()
              : '',
          enabled: typeof item.enabled === 'boolean' ? item.enabled : true,
          description:
            typeof item.description === 'string' ? item.description.trim() : '',
          models: [...new Set(models)],
        }
      })
  } catch {
    return []
  }
}

function serializeGroups(
  groups: EditableProbeGroup[],
  groupDisplayNames: Map<string, string>
): UptimeKumaGroupConfig[] {
  return groups.map((group) => {
    const cleanedModels = [
      ...new Set(group.models.map((m) => m.trim()).filter(Boolean)),
    ]
    const trimmedGroupId = group.groupId.trim()
    const fallbackName =
      (trimmedGroupId ? groupDisplayNames.get(trimmedGroupId) : undefined) ||
      trimmedGroupId
    const categoryName = group.categoryName.trim() || fallbackName
    const payload: UptimeKumaGroupConfig = {
      categoryName,
      enabled: group.enabled,
      models: cleanedModels,
    }
    if (trimmedGroupId) {
      payload.groupId = trimmedGroupId
    }
    if (group.description.trim()) {
      payload.description = group.description.trim()
    }
    return payload
  })
}

function summarizeProbeResult(data: UptimeProbeResponseData | undefined): {
  probed: number
  up: number
  down: number
  hasCounts: boolean
} {
  if (!data) {
    return { probed: 0, up: 0, down: 0, hasCounts: false }
  }
  const hasExplicitCounts =
    typeof data.probed === 'number' ||
    typeof data.total === 'number' ||
    typeof data.up === 'number' ||
    typeof data.down === 'number'
  if (hasExplicitCounts) {
    const up = typeof data.up === 'number' ? data.up : 0
    const down = typeof data.down === 'number' ? data.down : 0
    let probed = up + down
    if (typeof data.probed === 'number') {
      probed = data.probed
    } else if (typeof data.total === 'number') {
      probed = data.total
    }
    return { probed, up, down, hasCounts: true }
  }
  if (Array.isArray(data.results)) {
    let probed = 0
    let up = 0
    let down = 0
    for (const group of data.results) {
      for (const item of group.models ?? []) {
        probed++
        if (item.status === 1) up++
        else if (item.status === 0) down++
      }
    }
    return { probed, up, down, hasCounts: true }
  }
  return { probed: 0, up: 0, down: 0, hasCounts: false }
}

interface ProbeGroupCardProps {
  group: EditableProbeGroup
  displayName: string
  expanded: boolean
  allModels: string[]
  groupModels: string[]
  isProbing: boolean
  onToggleExpand: () => void
  onChange: (next: EditableProbeGroup) => void
  onDelete: () => void
  onProbeGroup: () => void
}

function ProbeGroupCard(props: ProbeGroupCardProps) {
  const { t } = useTranslation()
  const [modelSearch, setModelSearch] = useState('')
  const [customModelInput, setCustomModelInput] = useState('')

  const selectedSet = useMemo(
    () => new Set(props.group.models),
    [props.group.models]
  )
  const groupModelSet = useMemo(
    () => new Set(props.groupModels),
    [props.groupModels]
  )

  const selectableModels = useMemo(() => {
    const merged = new Set<string>([
      ...props.groupModels,
      ...props.allModels,
      ...props.group.models,
    ])
    return [...merged].sort((a, b) => {
      const aInGroup = groupModelSet.has(a) ? 0 : 1
      const bInGroup = groupModelSet.has(b) ? 0 : 1
      if (aInGroup !== bInGroup) return aInGroup - bInGroup
      return a.localeCompare(b)
    })
  }, [groupModelSet, props.allModels, props.group.models, props.groupModels])

  const filteredModels = useMemo(() => {
    const query = modelSearch.trim().toLowerCase()
    if (!query) return selectableModels
    return selectableModels.filter((m) => m.toLowerCase().includes(query))
  }, [modelSearch, selectableModels])

  const updateField = <K extends keyof EditableProbeGroup>(
    key: K,
    value: EditableProbeGroup[K]
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
    updateField(
      'models',
      props.group.models.filter((m) => m !== modelName)
    )
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
    updateField('models', [])
  }

  const headerTitle =
    props.group.categoryName.trim() ||
    props.displayName ||
    props.group.groupId ||
    t('Untitled Group')

  return (
    <Card
      className={cn(
        'overflow-hidden rounded-[14px] py-0 shadow-none transition-opacity',
        !props.group.enabled && 'opacity-75'
      )}
    >
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
          <span className='truncate text-sm font-semibold'>{headerTitle}</span>
          {props.group.groupId && (
            <Badge variant='outline' className='font-mono text-[11px]'>
              {props.group.groupId}
            </Badge>
          )}
          <Badge variant='secondary' className='text-[11px]'>
            {t('{{count}} models', { count: props.group.models.length })}
          </Badge>
          {!props.group.enabled && (
            <Badge
              variant='outline'
              className='text-muted-foreground text-[11px]'
            >
              {t('Disabled')}
            </Badge>
          )}
        </button>

        <div className='flex shrink-0 items-center gap-2'>
          <div className='flex items-center gap-1.5'>
            <Switch
              checked={props.group.enabled}
              onCheckedChange={(checked) => updateField('enabled', checked)}
              aria-label={t('Enable probing for {{group}}', {
                group: headerTitle,
              })}
            />
          </div>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={props.onProbeGroup}
            disabled={props.isProbing || !props.group.groupId}
            className='h-8 gap-1.5 text-xs'
          >
            {props.isProbing ? (
              <Loader2 className='size-3.5 animate-spin' aria-hidden='true' />
            ) : (
              <Play className='size-3.5' aria-hidden='true' />
            )}
            <span className='hidden sm:inline'>{t('Probe Group')}</span>
          </Button>
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
      </div>

      {props.expanded && (
        <CardContent className='border-border space-y-5 border-t px-4 py-4'>
          <div className='grid gap-4 md:grid-cols-2'>
            <div className='space-y-1.5'>
              <Label
                htmlFor={`${props.group.localId}-name`}
                className='text-xs font-medium'
              >
                {t('Display Name (optional)')}
              </Label>
              <Input
                id={`${props.group.localId}-name`}
                value={props.group.categoryName}
                onChange={(e) => updateField('categoryName', e.target.value)}
                placeholder={props.displayName || props.group.groupId}
                maxLength={50}
              />
            </div>

            <div className='space-y-1.5'>
              <Label
                htmlFor={`${props.group.localId}-description`}
                className='text-xs font-medium'
              >
                {t('Description (optional)')}
              </Label>
              <Input
                id={`${props.group.localId}-description`}
                value={props.group.description}
                onChange={(e) => updateField('description', e.target.value)}
                placeholder={t('Optional group status note')}
                maxLength={200}
              />
            </div>
          </div>

          <div className='space-y-3'>
            <div className='flex flex-wrap items-center justify-between gap-2'>
              <div>
                <Label className='text-xs font-semibold'>
                  {t('Monitored Models')}
                </Label>
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Select the exact models to probe periodically for this group'
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
                    {t('Select all visible')}
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
                    {t('Clear all')}
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
                  aria-label={t('Add custom model name...')}
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
                      const isInGroup = groupModelSet.has(modelName)
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
                          {isInGroup && (
                            <span
                              className='bg-primary/60 size-1.5 shrink-0 rounded-full'
                              title={t('Enabled in this group')}
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
                <span className='text-muted-foreground text-xs font-medium'>
                  {t('Selected models ({{count}})', {
                    count: props.group.models.length,
                  })}
                </span>
                <div className='flex max-h-36 flex-wrap gap-1.5 overflow-y-auto rounded-lg border p-2'>
                  {props.group.models.map((modelName) => (
                    <Badge
                      key={modelName}
                      variant='secondary'
                      className='gap-1 pr-1 font-mono text-[11px]'
                    >
                      <span className='max-w-52 truncate' title={modelName}>
                        {modelName}
                      </span>
                      <button
                        type='button'
                        onClick={() => toggleModel(modelName, false)}
                        className='text-muted-foreground hover:text-destructive rounded-xs p-0.5'
                        aria-label={`${t('Remove')} ${modelName}`}
                      >
                        <X className='size-3' />
                      </button>
                    </Badge>
                  ))}
                </div>
              </div>
            )}
          </div>
        </CardContent>
      )}
    </Card>
  )
}

export function UptimeKumaSection(props: UptimeKumaSectionProps) {
  const { t, i18n } = useTranslation()
  const contentLang = toContentLang(i18n.resolvedLanguage || i18n.language)
  const queryClient = useQueryClient()
  const updateOption = useUpdateOption()

  const [isEnabled, setIsEnabled] = useState(props.enabled)
  const [intervalInput, setIntervalInput] = useState(() =>
    String(
      clampInteger(
        props.intervalMinutes,
        DEFAULT_INTERVAL_MINUTES,
        MIN_INTERVAL_MINUTES,
        MAX_INTERVAL_MINUTES
      )
    )
  )
  const [timeoutInput, setTimeoutInput] = useState(() =>
    String(
      clampInteger(
        props.timeoutSeconds,
        DEFAULT_TIMEOUT_SECONDS,
        MIN_TIMEOUT_SECONDS,
        MAX_TIMEOUT_SECONDS
      )
    )
  )
  const [groups, setGroups] = useState<EditableProbeGroup[]>(() =>
    parseGroups(props.data)
  )
  const [expandedIds, setExpandedIds] = useState<Record<string, boolean>>({})
  const [intervalDirty, setIntervalDirty] = useState(false)
  const [timeoutDirty, setTimeoutDirty] = useState(false)
  const [groupsDirty, setGroupsDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [probingAll, setProbingAll] = useState(false)
  const [probingGroupIds, setProbingGroupIds] = useState<
    Record<string, boolean>
  >({})

  const [showAddGroupDialog, setShowAddGroupDialog] = useState(false)
  const [groupSearch, setGroupSearch] = useState('')
  const [deleteTargetId, setDeleteTargetId] = useState<string | null>(null)

  useEffect(() => {
    setIsEnabled(props.enabled)
  }, [props.enabled])

  useEffect(() => {
    setIntervalInput(
      String(
        clampInteger(
          props.intervalMinutes,
          DEFAULT_INTERVAL_MINUTES,
          MIN_INTERVAL_MINUTES,
          MAX_INTERVAL_MINUTES
        )
      )
    )
    setIntervalDirty(false)
  }, [props.intervalMinutes])

  useEffect(() => {
    setTimeoutInput(
      String(
        clampInteger(
          props.timeoutSeconds,
          DEFAULT_TIMEOUT_SECONDS,
          MIN_TIMEOUT_SECONDS,
          MAX_TIMEOUT_SECONDS
        )
      )
    )
    setTimeoutDirty(false)
  }, [props.timeoutSeconds])

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
    queryKey: ['model-probe', 'available-models'],
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

  const groupDisplayNames = useMemo(() => {
    const map = new Map<string, string>()
    for (const item of adminGroupsQuery.data ?? []) {
      const name = pickContentText(item.names, contentLang) || item.id
      map.set(item.id, name)
    }
    return map
  }, [adminGroupsQuery.data, contentLang])

  const addedGroupIds = useMemo(
    () => new Set(groups.map((g) => g.groupId).filter(Boolean)),
    [groups]
  )

  const unaddedAdminGroups = useMemo(
    () => (adminGroupsQuery.data ?? []).filter((g) => !addedGroupIds.has(g.id)),
    [addedGroupIds, adminGroupsQuery.data]
  )

  const availableAdminGroups = useMemo(() => {
    const query = groupSearch.trim().toLowerCase()
    if (!query) return unaddedAdminGroups
    return unaddedAdminGroups.filter((g) => {
      const displayName = pickContentText(g.names, contentLang).toLowerCase()
      return g.id.toLowerCase().includes(query) || displayName.includes(query)
    })
  }, [contentLang, groupSearch, unaddedAdminGroups])

  const hasChanges = intervalDirty || timeoutDirty || groupsDirty

  const buildEditableGroupFromAdmin = (
    adminGroup: AdminGroup,
    indexOffset = 0
  ): EditableProbeGroup => {
    const displayName =
      pickContentText(adminGroup.names, contentLang) || adminGroup.id
    const desc = pickContentText(adminGroup.descs, contentLang).slice(0, 200)
    const groupModels = systemModelsQuery.data?.byGroup[adminGroup.id] ?? []
    return {
      localId: `group-${Date.now()}-${indexOffset}-${adminGroup.id}`,
      groupId: adminGroup.id,
      categoryName: displayName.slice(0, 50),
      enabled: true,
      description: desc,
      models: groupModels.slice(0, MAX_MODELS_PER_GROUP),
    }
  }

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
        t('Monitored groups cannot exceed {{count}} entries', {
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
    const newGroup = buildEditableGroupFromAdmin(adminGroup)
    setGroups((prev) => [...prev, newGroup])
    setExpandedIds((prev) => ({ ...prev, [newGroup.localId]: true }))
    setGroupsDirty(true)
    setShowAddGroupDialog(false)
  }

  const handleAddAllGroups = () => {
    if (unaddedAdminGroups.length === 0) return
    const remainingSlots = Math.max(0, MAX_GROUPS - groups.length)
    if (remainingSlots === 0) {
      toast.error(
        t('Monitored groups cannot exceed {{count}} entries', {
          count: MAX_GROUPS,
        })
      )
      return
    }
    const batch = unaddedAdminGroups
      .slice(0, remainingSlots)
      .map((adminGroup, idx) => buildEditableGroupFromAdmin(adminGroup, idx))
    setGroups((prev) => [...prev, ...batch])
    if (batch[0]) {
      setExpandedIds((prev) => ({ ...prev, [batch[0].localId]: true }))
    }
    setGroupsDirty(true)
    setShowAddGroupDialog(false)
    toast.success(
      t('Added {{count}} groups. Click Save Settings to apply.', {
        count: batch.length,
      })
    )
  }

  const handleUpdateGroup = (next: EditableProbeGroup) => {
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

  const showProbeSummaryToast = (data: UptimeProbeResponseData | undefined) => {
    const summary = summarizeProbeResult(data)
    if (summary.hasCounts) {
      toast.success(
        t('Probe finished: {{probed}} checked, {{up}} up, {{down}} down', {
          probed: summary.probed,
          up: summary.up,
          down: summary.down,
        })
      )
      return
    }
    toast.success(t('Model connectivity probe completed'))
  }

  const handleProbeNow = async () => {
    setProbingAll(true)
    try {
      const res = requireServerSuccess(await triggerUptimeProbe())
      showProbeSummaryToast(res.data)
      void queryClient.invalidateQueries({ queryKey: UPTIME_STATUS_QUERY_KEY })
    } catch (error) {
      handleServerError(error, t('Failed to run connectivity probe'))
    } finally {
      setProbingAll(false)
    }
  }

  const handleProbeGroup = async (group: EditableProbeGroup) => {
    const groupId = group.groupId.trim()
    if (!groupId) return
    setProbingGroupIds((prev) => ({ ...prev, [group.localId]: true }))
    try {
      const res = requireServerSuccess(
        await triggerUptimeProbe({ group_id: groupId })
      )
      showProbeSummaryToast(res.data)
      void queryClient.invalidateQueries({ queryKey: UPTIME_STATUS_QUERY_KEY })
    } catch (error) {
      handleServerError(error, t('Failed to run connectivity probe'))
    } finally {
      setProbingGroupIds((prev) => ({ ...prev, [group.localId]: false }))
    }
  }

  const validateBeforeSave = (): {
    error: string | null
    interval: number
    timeout: number
  } => {
    const parsedInterval = Number(intervalInput.trim())
    if (
      !Number.isInteger(parsedInterval) ||
      parsedInterval < MIN_INTERVAL_MINUTES ||
      parsedInterval > MAX_INTERVAL_MINUTES
    ) {
      return {
        error: t(
          'Probe interval must be a whole number between {{min}} and {{max}} minutes',
          { min: MIN_INTERVAL_MINUTES, max: MAX_INTERVAL_MINUTES }
        ),
        interval: DEFAULT_INTERVAL_MINUTES,
        timeout: DEFAULT_TIMEOUT_SECONDS,
      }
    }

    const parsedTimeout = Number(timeoutInput.trim())
    if (
      !Number.isInteger(parsedTimeout) ||
      parsedTimeout < MIN_TIMEOUT_SECONDS ||
      parsedTimeout > MAX_TIMEOUT_SECONDS
    ) {
      return {
        error: t(
          'Probe timeout must be a whole number between {{min}} and {{max}} seconds',
          { min: MIN_TIMEOUT_SECONDS, max: MAX_TIMEOUT_SECONDS }
        ),
        interval: parsedInterval,
        timeout: DEFAULT_TIMEOUT_SECONDS,
      }
    }

    if (groups.length > MAX_GROUPS) {
      return {
        error: t('Monitored groups cannot exceed {{count}} entries', {
          count: MAX_GROUPS,
        }),
        interval: parsedInterval,
        timeout: parsedTimeout,
      }
    }

    const seenGroupIds = new Set<string>()
    for (let idx = 0; idx < groups.length; idx++) {
      const g = groups[idx]
      const gId = g.groupId.trim()
      if (!gId && !g.categoryName.trim()) {
        return {
          error: t('Group #{{index}} requires a group ID or display name', {
            index: idx + 1,
          }),
          interval: parsedInterval,
          timeout: parsedTimeout,
        }
      }
      if (gId) {
        if (seenGroupIds.has(gId)) {
          return {
            error: t('Group #{{index}} duplicates another monitored group', {
              index: idx + 1,
            }),
            interval: parsedInterval,
            timeout: parsedTimeout,
          }
        }
        seenGroupIds.add(gId)
      }
    }

    return { error: null, interval: parsedInterval, timeout: parsedTimeout }
  }

  const handleSaveAll = async () => {
    const { error: validationError, interval, timeout } = validateBeforeSave()
    if (validationError) {
      toast.error(validationError)
      return
    }
    setSaving(true)
    try {
      if (intervalDirty) {
        await updateOption.mutateAsync({
          key: 'console_setting.model_probe_interval_minutes',
          value: interval,
        })
        setIntervalDirty(false)
      }
      if (timeoutDirty) {
        await updateOption.mutateAsync({
          key: 'console_setting.model_probe_timeout_seconds',
          value: timeout,
        })
        setTimeoutDirty(false)
      }
      if (groupsDirty) {
        const serialized = serializeGroups(groups, groupDisplayNames)
        await updateOption.mutateAsync({
          key: 'console_setting.uptime_kuma_groups',
          value: JSON.stringify(serialized),
        })
        setGroupsDirty(false)
      }
      void queryClient.invalidateQueries({ queryKey: UPTIME_STATUS_QUERY_KEY })
    } catch (error) {
      handleServerError(error, t('Failed to save model probe settings'))
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
            const modelCount = (modelsByGroup[adminGroup.id] ?? []).length
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
                    {t('{{count}} models', { count: modelCount })}
                  </Badge>
                  <Badge variant='outline' className='text-[11px]'>
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
      title={t('Model Connectivity Monitor')}
      description={t(
        'Configure built-in scheduled connectivity probes per group and model for Overview, Pricing, and Model Status pages'
      )}
    >
      <div className='space-y-5'>
        <Card className='bg-muted/20 rounded-[14px] py-0 shadow-none'>
          <CardContent className='flex flex-col gap-4 p-4'>
            <div className='flex flex-wrap items-center justify-between gap-3'>
              <SettingsSwitchField
                checked={isEnabled}
                onCheckedChange={handleToggleEnabled}
                label={t('Enable Model Connectivity Monitor')}
                description={t(
                  'Periodically probe configured models and display 24h connectivity status across Overview, Pricing, and Model Status pages'
                )}
                className='flex-1 py-0'
              />
              <div className='flex flex-wrap items-center gap-2'>
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  onClick={() => void handleProbeNow()}
                  disabled={probingAll || saving}
                >
                  {probingAll ? (
                    <Loader2
                      className='size-4 animate-spin'
                      aria-hidden='true'
                    />
                  ) : (
                    <Activity className='size-4' aria-hidden='true' />
                  )}
                  {probingAll ? t('Probing...') : t('Probe Now')}
                </Button>
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  onClick={handleOpenAddGroup}
                >
                  <Plus className='size-4' aria-hidden='true' />
                  {t('Add Group')}
                </Button>
                {unaddedAdminGroups.length > 0 && (
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    onClick={handleAddAllGroups}
                  >
                    <Layers className='size-4' aria-hidden='true' />
                    {t('Add all groups')}
                  </Button>
                )}
                <Button
                  type='button'
                  variant='brand'
                  size='sm'
                  onClick={handleSaveAll}
                  disabled={!hasChanges || saving || updateOption.isPending}
                >
                  <Save className='size-4' aria-hidden='true' />
                  {saving ? t('Saving...') : t('Save Settings')}
                </Button>
              </div>
            </div>

            <div className='border-border/60 grid gap-4 border-t pt-3 sm:grid-cols-2'>
              <div className='space-y-1.5'>
                <Label
                  htmlFor='model-probe-interval-minutes'
                  className='flex items-center gap-1.5 text-xs font-semibold'
                >
                  <Clock className='text-muted-foreground size-3.5' />
                  {t('Probe Interval (minutes)')}
                </Label>
                <Input
                  id='model-probe-interval-minutes'
                  type='number'
                  min={MIN_INTERVAL_MINUTES}
                  max={MAX_INTERVAL_MINUTES}
                  step={1}
                  value={intervalInput}
                  onChange={(e) => {
                    setIntervalInput(e.target.value)
                    setIntervalDirty(true)
                  }}
                  className='max-w-44 font-mono text-sm'
                />
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Interval between automatic probe cycles ({{min}}–{{max}} min)',
                    {
                      min: MIN_INTERVAL_MINUTES,
                      max: MAX_INTERVAL_MINUTES,
                    }
                  )}
                </p>
              </div>

              <div className='space-y-1.5'>
                <Label
                  htmlFor='model-probe-timeout-seconds'
                  className='flex items-center gap-1.5 text-xs font-semibold'
                >
                  <Timer className='text-muted-foreground size-3.5' />
                  {t('Probe Timeout (seconds)')}
                </Label>
                <Input
                  id='model-probe-timeout-seconds'
                  type='number'
                  min={MIN_TIMEOUT_SECONDS}
                  max={MAX_TIMEOUT_SECONDS}
                  step={1}
                  value={timeoutInput}
                  onChange={(e) => {
                    setTimeoutInput(e.target.value)
                    setTimeoutDirty(true)
                  }}
                  className='max-w-44 font-mono text-sm'
                />
                <p className='text-muted-foreground text-xs'>
                  {t('Per-model probe request timeout ({{min}}–{{max}} sec)', {
                    min: MIN_TIMEOUT_SECONDS,
                    max: MAX_TIMEOUT_SECONDS,
                  })}
                </p>
              </div>
            </div>
          </CardContent>
        </Card>

        {groups.length === 0 ? (
          <EmptyState
            bordered
            title={t('No monitored groups configured')}
            description={t(
              'Click "Add Group" to select system groups and models for built-in connectivity probing.'
            )}
            action={
              <div className='flex flex-wrap items-center justify-center gap-2'>
                <Button type='button' size='sm' onClick={handleOpenAddGroup}>
                  <Plus className='size-4' />
                  {t('Add Group')}
                </Button>
                {unaddedAdminGroups.length > 0 && (
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    onClick={handleAddAllGroups}
                  >
                    <Layers className='size-4' />
                    {t('Add all groups')}
                  </Button>
                )}
              </div>
            }
          />
        ) : (
          <div className='space-y-3'>
            {groups.map((group) => (
              <ProbeGroupCard
                key={group.localId}
                group={group}
                displayName={
                  (group.groupId
                    ? groupDisplayNames.get(group.groupId)
                    : undefined) ?? group.groupId
                }
                expanded={Boolean(expandedIds[group.localId])}
                allModels={allModels}
                groupModels={
                  group.groupId ? (modelsByGroup[group.groupId] ?? []) : []
                }
                isProbing={
                  Boolean(probingGroupIds[group.localId]) || probingAll
                }
                onToggleExpand={() =>
                  setExpandedIds((prev) => ({
                    ...prev,
                    [group.localId]: !prev[group.localId],
                  }))
                }
                onChange={handleUpdateGroup}
                onDelete={() => setDeleteTargetId(group.localId)}
                onProbeGroup={() => void handleProbeGroup(group)}
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
          'Choose a system group to enable built-in model connectivity probing'
        )}
        contentHeight='auto'
        bodyClassName='space-y-3'
      >
        <div className='flex flex-wrap items-center gap-2'>
          <div className='relative min-w-0 flex-1'>
            <Search className='text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2' />
            <Input
              value={groupSearch}
              onChange={(e) => setGroupSearch(e.target.value)}
              placeholder={t('Search system groups...')}
              className='pl-8'
            />
          </div>
          {unaddedAdminGroups.length > 0 && (
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={handleAddAllGroups}
              className='shrink-0'
            >
              <Plus className='size-4' />
              {t('Add all groups')}
            </Button>
          )}
        </div>

        {groupDialogBody}
      </Dialog>

      <ConfirmDialog
        open={deleteTargetId !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteTargetId(null)
        }}
        title={t('Are you sure?')}
        desc={t(
          'This monitored group configuration will be removed from the list.'
        )}
        confirmText={t('Delete')}
        destructive
        handleConfirm={handleConfirmDelete}
      />
    </SettingsSection>
  )
}
