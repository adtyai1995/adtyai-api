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
import { useQueryClient } from '@tanstack/react-query'
import { HelpCircle } from 'lucide-react'
import { memo, useCallback, useState } from 'react'
import { useWatch, type UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import {
  sideDrawerContentClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { Button } from '@/components/ui/button'
import { Form } from '@/components/ui/form'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'

import { SettingsPageActionsPortal } from '../components/settings-page-context'
import { GroupRatioVisualEditor } from './group-ratio-visual-editor'

type GroupFormValues = {
  GroupRatio: string
  UserUsableGroups: string
}

type GroupRatioFormProps = {
  form: UseFormReturn<GroupFormValues>
  onSave: (values: GroupFormValues) => Promise<void>
  isSaving: boolean
}

export const GroupRatioForm = memo(function GroupRatioForm({
  form,
  onSave,
  isSaving,
}: GroupRatioFormProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const values = useWatch({ control: form.control })
  const [guideOpen, setGuideOpen] = useState(false)

  const handleFieldChange = useCallback(
    (field: keyof GroupFormValues, value: string) => {
      form.setValue(field, value, {
        shouldValidate: true,
        shouldDirty: true,
      })
    },
    [form]
  )

  const handleSave = useCallback(
    async (submitted: GroupFormValues) => {
      await onSave(submitted)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['groups'] }),
        queryClient.invalidateQueries({ queryKey: ['user-groups'] }),
      ])
    },
    [onSave, queryClient]
  )

  return (
    <div className='space-y-6'>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <p className='text-muted-foreground text-sm'>
          {t('Manage pricing ratios and descriptions for your groups.')}
        </p>
        <div className='flex shrink-0 gap-2'>
          <Button
            variant='outline'
            size='sm'
            onClick={() => setGuideOpen(true)}
          >
            <HelpCircle className='mr-2 h-4 w-4' />
            {t('Usage guide')}
          </Button>
        </div>
      </div>

      <GroupPricingGuide open={guideOpen} onOpenChange={setGuideOpen} />

      <Form {...form}>
        <SettingsPageActionsPortal>
          <Button
            variant='brand'
            type='button'
            size='sm'
            onClick={form.handleSubmit(handleSave)}
            disabled={isSaving}
          >
            {isSaving ? t('Saving...') : t('Save group settings')}
          </Button>
        </SettingsPageActionsPortal>
        <GroupRatioVisualEditor
          groupRatio={values.GroupRatio ?? ''}
          userUsableGroups={values.UserUsableGroups ?? ''}
          onChange={(field, value) =>
            handleFieldChange(field as keyof GroupFormValues, value)
          }
        />
      </Form>
    </div>
  )
})

type GroupPricingGuideProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

function GuideCodeBlock({ children }: { children: string }) {
  return (
    <pre className='bg-muted/60 overflow-x-auto rounded-lg border px-3 py-2 text-xs leading-6 whitespace-pre-wrap'>
      {children}
    </pre>
  )
}

function GroupPricingGuide({ open, onOpenChange }: GroupPricingGuideProps) {
  const { t } = useTranslation()

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side='right'
        className={sideDrawerContentClassName('sm:max-w-2xl')}
      >
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{t('Group pricing usage guide')}</SheetTitle>
          <SheetDescription>
            {t(
              'Use the pricing group table to manage each group ratio and description.'
            )}
          </SheetDescription>
        </SheetHeader>

        <div className={sideDrawerFormClassName('gap-5')}>
          <section className='space-y-3'>
            <h3 className='text-sm font-semibold'>
              {t('Pricing group example')}
            </h3>
            <p className='text-muted-foreground text-sm leading-6'>
              {t(
                'Use the pricing group table to manage each group ratio and description.'
              )}
            </p>
            <GuideCodeBlock>
              {`${t('Group name')}   ${t('Ratio')}   ${t('Description')}
standard     1.0     ${t('Standard price')}
premium      0.5     ${t('Premium plan, half price')}`}
            </GuideCodeBlock>
          </section>
        </div>
      </SheetContent>
    </Sheet>
  )
}
