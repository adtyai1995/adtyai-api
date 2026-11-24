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
import { ArrowUpDown, Check, ChevronDown, Filter } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTableViewModeToggle } from '@/components/data-table'
import {
  sideDrawerContentClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { useGroupDisplay } from '@/features/adty-groups/hooks/use-group-display'
import { cn } from '@/lib/utils'

import {
  FILTER_ALL,
  getSortLabels,
  type SortOption,
  type ViewMode,
} from '../constants'
import {
  formatEffectiveGroupRatio,
  resolvePricingGroupName,
} from '../lib/model-helpers'
import type {
  PricingModel,
  PricingUsableGroupValue,
  PricingVendor,
  TokenUnit,
} from '../types'
import { PricingSidebar } from './pricing-sidebar'
import { SearchBar } from './search-bar'

export interface PricingToolbarProps {
  /** Search stays optional so the toolbar can render without a catalog table. */
  searchValue?: string
  onSearchChange?: (value: string) => void
  onSearchClear?: () => void
  searchPlaceholder?: string
  filteredCount: number
  totalCount?: number
  sortBy: string
  onSortChange: (value: string) => void
  tokenUnit: TokenUnit
  onTokenUnitChange: (value: TokenUnit) => void
  showRechargePrice: boolean
  onRechargePriceChange: (value: boolean) => void
  viewMode: ViewMode
  onViewModeChange: (value: ViewMode) => void
  quotaTypeFilter: string
  endpointTypeFilter: string
  vendorFilter: string
  groupFilter: string
  tagFilter: string
  onQuotaTypeChange: (value: string) => void
  onEndpointTypeChange: (value: string) => void
  onVendorChange: (value: string) => void
  onGroupChange: (value: string) => void
  onTagChange: (value: string) => void
  vendors: PricingVendor[]
  groups: string[]
  groupRatios?: Record<string, number>
  usableGroup?: Record<string, PricingUsableGroupValue>
  tags: string[]
  models: PricingModel[]
  hasActiveFilters: boolean
  activeFilterCount: number
  onClearFilters: () => void
}

export function PricingToolbar(props: PricingToolbarProps) {
  const { t } = useTranslation()
  const groupDisplay = useGroupDisplay()
  const [mobileFiltersOpen, setMobileFiltersOpen] = useState(false)
  const sortLabels = getSortLabels(t)
  const hasSearch =
    props.searchValue !== undefined && props.onSearchChange !== undefined
  const getGroupLabel = (group: string) =>
    group === FILTER_ALL
      ? t('All Groups')
      : resolvePricingGroupName(
          group,
          groupDisplay.name(group),
          props.usableGroup
        )
  const activeGroupLabel = getGroupLabel(props.groupFilter)
  const activeGroupRatio =
    props.groupFilter === FILTER_ALL
      ? undefined
      : formatEffectiveGroupRatio(props.groupRatios?.[props.groupFilter])

  return (
    <div className='bg-card rounded-[14px] border'>
      <div className='flex flex-wrap items-center gap-2 p-2.5'>
        <div className='flex min-w-0 flex-1 flex-wrap items-center gap-2'>
          <Button
            type='button'
            variant='outline'
            onClick={() => setMobileFiltersOpen(true)}
            className='h-9 gap-1.5 px-3 text-[13px]'
          >
            <Filter className='size-4' />
            {t('Filter')}
            {props.activeFilterCount > 0 && (
              <Badge className='ml-0.5 size-5 justify-center p-0 text-[10px]'>
                {props.activeFilterCount}
              </Badge>
            )}
          </Button>

          {hasSearch && (
            <SearchBar
              value={props.searchValue ?? ''}
              onChange={(value) => props.onSearchChange?.(value)}
              onClear={() => props.onSearchClear?.()}
              placeholder={props.searchPlaceholder}
              className='min-w-0 flex-1 sm:max-w-[280px]'
            />
          )}

          <DropdownMenu modal={false}>
            <DropdownMenuTrigger
              render={
                <Button
                  type='button'
                  variant='outline'
                  className='h-9 max-w-[200px] gap-1.5 px-3 text-[13px]'
                />
              }
            >
              <span className='text-muted-foreground shrink-0'>
                {t('Group')}
              </span>
              <span className='min-w-0 truncate font-medium'>
                {activeGroupLabel}
              </span>
              {activeGroupRatio && (
                <span className='bg-muted text-muted-foreground shrink-0 rounded px-1 py-0.5 font-mono text-[11px]'>
                  {activeGroupRatio}
                </span>
              )}
              <ChevronDown className='text-muted-foreground/70 size-3.5 shrink-0' />
            </DropdownMenuTrigger>
            <DropdownMenuContent align='start' className='w-60'>
              <DropdownMenuGroup>
                {[FILTER_ALL, ...props.groups].map((group) => {
                  const ratioLabel =
                    group === FILTER_ALL
                      ? undefined
                      : formatEffectiveGroupRatio(props.groupRatios?.[group])
                  return (
                    <DropdownMenuItem
                      key={group}
                      onClick={() => props.onGroupChange(group)}
                      className='gap-2'
                    >
                      <Check
                        className={cn(
                          'size-4 shrink-0',
                          props.groupFilter === group
                            ? 'opacity-100'
                            : 'opacity-0'
                        )}
                      />
                      <span className='min-w-0 flex-1 truncate'>
                        {getGroupLabel(group)}
                      </span>
                      {ratioLabel && (
                        <span className='text-muted-foreground shrink-0 font-mono text-xs tabular-nums'>
                          {ratioLabel}
                        </span>
                      )}
                    </DropdownMenuItem>
                  )
                })}
              </DropdownMenuGroup>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>

        <div className='ml-auto flex min-w-0 flex-wrap items-center gap-2'>
          <div className='text-muted-foreground mr-1 flex items-baseline gap-1 text-[13px]'>
            <span className='text-foreground font-semibold tabular-nums'>
              {props.filteredCount.toLocaleString()}
            </span>
            <span>{props.filteredCount === 1 ? t('model') : t('models')}</span>
            {props.totalCount != null &&
              props.filteredCount !== props.totalCount && (
                <span className='text-muted-foreground/60 text-xs'>
                  / {props.totalCount.toLocaleString()}
                </span>
              )}
          </div>
          <ToggleGroup
            value={[props.showRechargePrice ? 'recharge' : 'standard']}
            onValueChange={(values) => {
              if (values.length > 0) {
                props.onRechargePriceChange(values[0] === 'recharge')
              }
            }}
            variant='outline'
            size='sm'
            aria-label={t('Price display mode')}
          >
            <ToggleGroupItem value='standard'>{t('Standard')}</ToggleGroupItem>
            <ToggleGroupItem value='recharge'>{t('Recharge')}</ToggleGroupItem>
          </ToggleGroup>
          <ToggleGroup
            value={[props.tokenUnit]}
            onValueChange={(values) => {
              if (values[0] === 'M' || values[0] === 'K') {
                props.onTokenUnitChange(values[0])
              }
            }}
            variant='outline'
            size='sm'
            aria-label={t('Token unit')}
          >
            <ToggleGroupItem value='M'>/1M</ToggleGroupItem>
            <ToggleGroupItem value='K'>/1K</ToggleGroupItem>
          </ToggleGroup>

          <DropdownMenu modal={false}>
            <DropdownMenuTrigger
              render={
                <Button
                  type='button'
                  variant='outline'
                  className='h-9 gap-1.5 px-3 text-[13px]'
                />
              }
            >
              <ArrowUpDown className='size-3.5' />
              <span>{sortLabels[props.sortBy as SortOption] || t('Sort')}</span>
            </DropdownMenuTrigger>
            <DropdownMenuContent align='end' className='w-44'>
              <DropdownMenuGroup>
                {Object.entries(sortLabels).map(([value, label]) => (
                  <DropdownMenuItem
                    key={value}
                    onClick={() => props.onSortChange(value)}
                    className='gap-2'
                  >
                    <Check
                      className={cn(
                        'size-4 shrink-0',
                        props.sortBy === value ? 'opacity-100' : 'opacity-0'
                      )}
                    />
                    {label}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuGroup>
            </DropdownMenuContent>
          </DropdownMenu>

          <DataTableViewModeToggle
            value={props.viewMode}
            onChange={props.onViewModeChange}
          />
        </div>
      </div>

      {props.groups.length > 0 && (
        <div
          role='group'
          aria-label={t('Groups')}
          className='border-border/70 flex flex-wrap items-center gap-1.5 border-t px-2.5 py-2'
        >
          {[FILTER_ALL, ...props.groups].map((group) => {
            const active = props.groupFilter === group
            const label = getGroupLabel(group)
            const ratioLabel =
              group === FILTER_ALL
                ? undefined
                : formatEffectiveGroupRatio(props.groupRatios?.[group])
            return (
              <Button
                key={group}
                type='button'
                variant={active ? 'secondary' : 'outline'}
                size='sm'
                onClick={() => props.onGroupChange(group)}
                aria-pressed={active}
                className='h-7 max-w-full gap-1.5 px-2.5 text-xs'
                title={label}
              >
                <span className='truncate'>{label}</span>
                {ratioLabel && (
                  <span
                    className={cn(
                      'rounded px-1.5 py-0.5 font-mono text-[11px] tabular-nums',
                      active
                        ? 'bg-background text-foreground'
                        : 'bg-muted text-muted-foreground'
                    )}
                  >
                    {ratioLabel}
                  </span>
                )}
              </Button>
            )
          })}
        </div>
      )}

      <Sheet open={mobileFiltersOpen} onOpenChange={setMobileFiltersOpen}>
        <SheetContent
          side='left'
          className={sideDrawerContentClassName('sm:max-w-md')}
        >
          <SheetHeader className={sideDrawerHeaderClassName()}>
            <SheetTitle>{t('Filter')}</SheetTitle>
            <SheetDescription>
              {t('Filter models by provider, group, type, endpoint, and tags.')}
            </SheetDescription>
          </SheetHeader>
          <div className={sideDrawerFormClassName('gap-0')}>
            <PricingSidebar
              quotaTypeFilter={props.quotaTypeFilter}
              endpointTypeFilter={props.endpointTypeFilter}
              vendorFilter={props.vendorFilter}
              groupFilter={props.groupFilter}
              tagFilter={props.tagFilter}
              onQuotaTypeChange={props.onQuotaTypeChange}
              onEndpointTypeChange={props.onEndpointTypeChange}
              onVendorChange={props.onVendorChange}
              onGroupChange={props.onGroupChange}
              onTagChange={props.onTagChange}
              vendors={props.vendors}
              groups={props.groups}
              groupRatios={props.groupRatios}
              usableGroup={props.usableGroup}
              tags={props.tags}
              models={props.models}
              hasActiveFilters={props.hasActiveFilters}
              onClearFilters={props.onClearFilters}
              className='border-0 bg-transparent p-0 shadow-none'
            />
          </div>
        </SheetContent>
      </Sheet>
    </div>
  )
}
