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
import { useMutation } from '@tanstack/react-query'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  sideDrawerContentClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import { NativeSelect } from '@/components/ui/native-select'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { handleServerError } from '@/lib/handle-server-error'

import {
  replaceEpayChannel,
  type EpayChannel,
  type EpayChannelInput,
} from '../api'
import {
  epayChannelHasUsableCredentials,
  epayInputNeedsCredentials,
  toReplacementChannelInput,
} from '../channel-form-helpers'
import { ChannelFormFields } from './channel-form-fields'

type ReplaceMode = 'new' | 'existing'

type ReplaceChannelDrawerProps = {
  open: boolean
  channel: EpayChannel | null
  channels: EpayChannel[]
  onOpenChange: (open: boolean) => void
  onReplaced: () => void
}

/** Replace an Epay channel with a new or existing channel while migrating payment scenes. */
export function ReplaceChannelDrawer(props: ReplaceChannelDrawerProps) {
  const { t } = useTranslation()
  const candidates = useMemo(
    () =>
      props.channels.filter(
        (item) =>
          item.id !== props.channel?.id && epayChannelHasUsableCredentials(item)
      ),
    [props.channels, props.channel?.id]
  )

  const [mode, setMode] = useState<ReplaceMode>('new')
  const [targetId, setTargetId] = useState<string>('')
  const [newInput, setNewInput] = useState<EpayChannelInput>(() =>
    toReplacementChannelInput(props.channel)
  )
  const [enableTarget, setEnableTarget] = useState<boolean>(true)
  const [deleteOld, setDeleteOld] = useState<boolean>(false)

  useEffect(() => {
    if (!props.open || !props.channel) return
    setMode('new')
    setTargetId(candidates[0] ? String(candidates[0].id) : '')
    setNewInput(toReplacementChannelInput(props.channel))
    setEnableTarget(props.channel.status === 'enabled')
    setDeleteOld(false)
  }, [props.open, props.channel, candidates])

  const replace = useMutation({
    mutationFn: async () => {
      if (!props.channel) return null
      if (mode === 'existing') {
        return replaceEpayChannel(props.channel.id, {
          target_channel_id: Number(targetId),
          delete_old: deleteOld,
          enable_target: enableTarget,
        })
      }
      return replaceEpayChannel(props.channel.id, {
        new_channel: newInput,
        delete_old: deleteOld,
        enable_target: enableTarget,
      })
    },
    onSuccess: (res) => {
      if (!res) return
      toast.success(
        res.updated_scenes > 0
          ? t(
              'Replaced {{oldName}} with {{newName}} and updated {{scenes}} payment scenes',
              {
                oldName: res.old_name,
                newName: res.new_name,
                scenes: res.updated_scenes,
              }
            )
          : t('Replaced {{oldName}} with {{newName}}', {
              oldName: res.old_name,
              newName: res.new_name,
            })
      )
      props.onReplaced()
      props.onOpenChange(false)
    },
    onError: (error) => handleServerError(error),
  })

  const sceneNames = props.channel?.scene_names ?? []
  const newChannelInvalid =
    mode === 'new' &&
    (newInput.name.trim() === '' ||
      newInput.gateway_url.trim() === '' ||
      newInput.merchant_id.trim() === '' ||
      !newInput.methods.some((m) => m.enabled) ||
      epayInputNeedsCredentials(newInput, null))
  const existingInvalid = mode === 'existing' && !(Number(targetId) > 0)

  return (
    <Sheet open={props.open} onOpenChange={props.onOpenChange}>
      <SheetContent className={sideDrawerContentClassName()}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>
            {t('Replace payment channel: {{name}}', {
              name: props.channel?.name ?? '',
            })}
          </SheetTitle>
          <SheetDescription>
            {t(
              'Switch payment scene bindings to a replacement Epay channel and disable or delete the old one without losing historical order records.'
            )}
          </SheetDescription>
        </SheetHeader>
        <form
          className={sideDrawerFormClassName()}
          onSubmit={(event) => {
            event.preventDefault()
            replace.mutate()
          }}
        >
          {sceneNames.length > 0 && (
            <Alert>
              <AlertDescription>
                {t(
                  'Referenced payment scenes will be migrated automatically: {{scenes}}',
                  { scenes: sceneNames.join(', ') }
                )}
              </AlertDescription>
            </Alert>
          )}

          <div className='space-y-2'>
            <Label>{t('Replacement method')}</Label>
            <RadioGroup
              value={mode}
              onValueChange={(value) => setMode(value as ReplaceMode)}
              className='grid grid-cols-1 gap-2 sm:grid-cols-2'
            >
              <label
                htmlFor='epay-replace-mode-new'
                className='flex cursor-pointer items-center gap-2 rounded-lg border p-3 text-sm font-medium'
              >
                <RadioGroupItem value='new' id='epay-replace-mode-new' />
                {t('Create new channel')}
              </label>
              <label
                htmlFor='epay-replace-mode-existing'
                className='flex cursor-pointer items-center gap-2 rounded-lg border p-3 text-sm font-medium aria-disabled:cursor-not-allowed aria-disabled:opacity-50'
                aria-disabled={candidates.length === 0}
              >
                <RadioGroupItem
                  value='existing'
                  id='epay-replace-mode-existing'
                  disabled={candidates.length === 0}
                />
                {t('Use existing channel')}
              </label>
            </RadioGroup>
          </div>

          {mode === 'existing' ? (
            <div className='space-y-2'>
              <Label htmlFor='epay-replace-target'>
                {t('Replacement channel')}
              </Label>
              <NativeSelect
                id='epay-replace-target'
                value={targetId}
                onChange={(e) => setTargetId(e.target.value)}
              >
                {candidates.map((item) => (
                  <option key={item.id} value={String(item.id)}>
                    {item.name} ({item.sign_type === 'RSA' ? 'RSA' : 'MD5'})
                  </option>
                ))}
              </NativeSelect>
            </div>
          ) : (
            <ChannelFormFields
              idPrefix='epay-replace'
              input={newInput}
              existingChannel={null}
              onChange={setNewInput}
            />
          )}

          <div className='space-y-3 rounded-lg border p-3'>
            <label className='flex items-start gap-2 text-sm'>
              <Checkbox
                checked={enableTarget}
                onCheckedChange={(checked) => setEnableTarget(checked === true)}
                className='mt-0.5'
              />
              <span>
                <span className='block font-medium'>
                  {t('Enable replacement channel immediately')}
                </span>
                <span className='text-muted-foreground block text-xs'>
                  {t(
                    'Turn this off if you want to run a test order before enabling the replacement channel.'
                  )}
                </span>
              </span>
            </label>
            <label className='flex items-start gap-2 text-sm'>
              <Checkbox
                checked={deleteOld}
                onCheckedChange={(checked) => setDeleteOld(checked === true)}
                className='mt-0.5'
              />
              <span>
                <span className='block font-medium'>
                  {t('Delete old channel after replacing')}
                </span>
                <span className='text-muted-foreground block text-xs'>
                  {t(
                    'When unchecked, the old channel is disabled and kept for pending callbacks. Historical orders always retain the old channel name.'
                  )}
                </span>
              </span>
            </label>
          </div>

          <div className='flex justify-end gap-2'>
            <Button
              type='button'
              variant='outline'
              onClick={() => props.onOpenChange(false)}
            >
              {t('Cancel')}
            </Button>
            <Button
              variant='brand'
              type='submit'
              disabled={newChannelInvalid || existingInvalid || replace.isPending}
            >
              {t('Confirm replace')}
            </Button>
          </div>
        </form>
      </SheetContent>
    </Sheet>
  )
}
