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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  sideDrawerContentClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { handleServerError } from '@/lib/handle-server-error'

import {
  saveEpayChannel,
  type EpayChannel,
  type EpayChannelInput,
} from '../api'
import {
  epayInputNeedsCredentials,
  toEpayChannelInput,
} from '../channel-form-helpers'
import { ChannelFormFields } from './channel-form-fields'

type ChannelDrawerProps = {
  open: boolean
  channel: EpayChannel | null
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}

/** Add or edit an Epay channel (root only). All fields are editable; empty key keeps existing credentials on active channels. */
export function ChannelDrawer(props: ChannelDrawerProps) {
  const { t } = useTranslation()
  const [input, setInput] = useState<EpayChannelInput>(() =>
    toEpayChannelInput(props.channel)
  )
  useEffect(() => {
    if (props.open) setInput(toEpayChannelInput(props.channel))
  }, [props.open, props.channel])

  const save = useMutation({
    mutationFn: () => saveEpayChannel(props.channel?.id ?? null, input),
    onSuccess: () => {
      toast.success(t('Payment channel saved'))
      props.onSaved()
      props.onOpenChange(false)
    },
    onError: (error) => handleServerError(error),
  })

  const anyMethod = input.methods.some((m) => m.enabled)
  const needsKey = epayInputNeedsCredentials(input, props.channel)
  const missingBasic =
    input.name.trim() === '' ||
    input.gateway_url.trim() === '' ||
    input.merchant_id.trim() === ''
  const isRevoked = props.channel?.status === 'revoked'
  const hasOrders = (props.channel?.order_count ?? 0) > 0

  return (
    <Sheet open={props.open} onOpenChange={props.onOpenChange}>
      <SheetContent className={sideDrawerContentClassName()}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>
            {props.channel
              ? t('Edit payment channel')
              : t('Add payment channel')}
          </SheetTitle>
          <SheetDescription>
            {t('Only the super administrator can change channels and keys.')}
          </SheetDescription>
        </SheetHeader>
        <form
          className={sideDrawerFormClassName()}
          onSubmit={(event) => {
            event.preventDefault()
            save.mutate()
          }}
        >
          {isRevoked && (
            <Alert>
              <AlertDescription>
                {t(
                  'This channel was revoked. Enter valid credentials and save to restore it to Disabled so you can test and enable it again.'
                )}
              </AlertDescription>
            </Alert>
          )}
          {!isRevoked && hasOrders && (
            <Alert>
              <AlertDescription>
                {t(
                  'This channel has {{count}} historical orders. You can edit the gateway address, merchant ID, keys, and all other settings; historical orders keep their original channel snapshot, and changes apply to new orders.',
                  { count: props.channel?.order_count ?? 0 }
                )}
              </AlertDescription>
            </Alert>
          )}
          <ChannelFormFields
            idPrefix='epay'
            input={input}
            existingChannel={props.channel}
            onChange={setInput}
          />
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
              disabled={!anyMethod || needsKey || missingBasic || save.isPending}
            >
              {t('Save')}
            </Button>
          </div>
        </form>
      </SheetContent>
    </Sheet>
  )
}
