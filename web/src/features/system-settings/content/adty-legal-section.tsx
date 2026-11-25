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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import {
  CONTENT_LANG_LABELS,
  CONTENT_LANGS,
  STORED_CONTENT_LANGS,
  type StoredContentLang,
} from '@/lib/adty-lang'
import { STATUS_QUERY_KEY, STATUS_STORAGE_KEY } from '@/lib/status-query'

import { DraftNavigationProvider } from '../components/draft-navigation-provider'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useDraftNavigation } from '../hooks/use-draft-navigation'
import { useUpdateOption } from '../hooks/use-update-option'

export type AdtyLegalField =
  | 'user_agreement'
  | 'privacy_policy'
  | 'refund_policy'

/** Saved option values: each is a JSON object of content language → Markdown. */
export type AdtyLegalDefaults = Record<AdtyLegalField, string> & {
  site_customer_terms?: string
  site_customer_terms_version?: number
}

type LegalTexts = Partial<Record<StoredContentLang, string>>

const FIELDS: AdtyLegalField[] = [
  'user_agreement',
  'privacy_policy',
  'refund_policy',
]

function parseTexts(raw: string): LegalTexts {
  try {
    const parsed: unknown = JSON.parse(raw || '{}')
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed as LegalTexts
    }
  } catch {
    /* A broken value is shown as empty and replaced on save. */
  }
  return {}
}

/** Saved form: trimmed, without empty languages, keys in a stable order. */
function serializeTexts(texts: LegalTexts): string {
  const out: LegalTexts = {}
  // Legacy keys are preserved so an old translation is not erased by a save.
  for (const lang of STORED_CONTENT_LANGS) {
    const value = texts[lang]?.trim()
    if (value) out[lang] = texts[lang]
  }
  return JSON.stringify(out)
}

function toValues(saved: AdtyLegalDefaults): Record<AdtyLegalField, string> {
  return {
    user_agreement: serializeTexts(parseTexts(saved.user_agreement)),
    privacy_policy: serializeTexts(parseTexts(saved.privacy_policy)),
    refund_policy: serializeTexts(parseTexts(saved.refund_policy)),
  }
}

function normalizeValue(value: string) {
  return serializeTexts(parseTexts(value))
}

function LegalEditor(props: { defaultValues: AdtyLegalDefaults }) {
  const { t } = useTranslation()
  const labels: Record<AdtyLegalField, string> = {
    user_agreement: t('User Agreement'),
    privacy_policy: t('Privacy Policy'),
    refund_policy: t('Refund Policy'),
  }
  const updateOption = useUpdateOption()
  const queryClient = useQueryClient()
  const [field, setField] = useState<AdtyLegalField>('user_agreement')
  const [editor, setEditor] = useState(() => ({
    values: toValues(props.defaultValues),
    baseline: toValues(props.defaultValues),
  }))
  const [saving, setSaving] = useState(false)
  const dirty = FIELDS.some(
    (key) => normalizeValue(editor.values[key]) !== editor.baseline[key]
  )
  useDraftNavigation(dirty)

  const savedKey = JSON.stringify({
    user_agreement: props.defaultValues.user_agreement,
    privacy_policy: props.defaultValues.privacy_policy,
    refund_policy: props.defaultValues.refund_policy,
  })
  useEffect(() => {
    if (saving) return
    const next = toValues(JSON.parse(savedKey))
    setEditor((current) => {
      const values = { ...current.values }
      for (const key of FIELDS) {
        if (normalizeValue(values[key]) === current.baseline[key]) {
          values[key] = next[key]
        }
      }
      return { values, baseline: next }
    })
  }, [savedKey, saving])

  const save = async () => {
    if (saving) return
    const dirtyFields = FIELDS.map((key) => ({
      key,
      value: normalizeValue(editor.values[key]),
    })).filter(({ key, value }) => value !== editor.baseline[key])
    if (dirtyFields.length === 0) return

    setSaving(true)
    let shouldInvalidateStatus = false
    try {
      for (const { key, value } of dirtyFields) {
        await updateOption.mutateAsync({
          key: `adty_legal_setting.${key}`,
          value,
        })
        if (key === 'user_agreement' || key === 'privacy_policy') {
          shouldInvalidateStatus = true
        }
        setEditor((current) => ({
          ...current,
          baseline: {
            ...current.baseline,
            [key]: value,
          },
        }))
      }
    } catch {
      // The mutation reports the error; remaining drafts stay available to retry.
    } finally {
      if (shouldInvalidateStatus) {
        void queryClient.invalidateQueries({ queryKey: STATUS_QUERY_KEY })
        try {
          window.localStorage.removeItem(STATUS_STORAGE_KEY)
        } catch {
          /* Storage may be disabled. */
        }
      }
      setSaving(false)
    }
  }

  return (
    <SettingsSection
      title={t('Multilingual Legal Pages')}
      bodyClassName='space-y-5'
    >
      <SettingsPageFormActions onSave={save} isSaving={saving} />
      <p className='text-dim max-w-[80ch] text-[13px] leading-relaxed'>
        {t(
          'Readers see the text in their language, falling back to English or Simplified Chinese when empty. Filling in the user agreement or privacy policy automatically enables sign-up consent and footer links. The refund policy is shown only when enabled in site settings.'
        )}
      </p>
      <div className='max-w-3xl space-y-4'>
        <div className='max-w-xs space-y-2'>
          <Label htmlFor='adty-legal-doc'>{t('Document')}</Label>
          <NativeSelect
            id='adty-legal-doc'
            value={field}
            onChange={(event) => setField(event.target.value as AdtyLegalField)}
          >
            {FIELDS.map((item) => (
              <NativeSelectOption key={item} value={item}>
                {labels[item]}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
        <Tabs defaultValue='en' className='gap-4'>
          <div className='overflow-x-auto pb-1'>
            <TabsList className='w-full min-w-max'>
              {CONTENT_LANGS.map((lang) => (
                <TabsTrigger key={lang} value={lang} className='px-2'>
                  {CONTENT_LANG_LABELS[lang]}
                </TabsTrigger>
              ))}
            </TabsList>
          </div>
          {CONTENT_LANGS.map((lang) => (
            <TabsContent key={lang} value={lang} className='space-y-2'>
              <Label htmlFor={`adty-legal-${lang}`}>
                {t('{{document}} in {{language}} (Markdown)', {
                  document: labels[field],
                  language: CONTENT_LANG_LABELS[lang],
                })}
              </Label>
              <Textarea
                id={`adty-legal-${lang}`}
                rows={14}
                className='font-mono text-xs'
                value={parseTexts(editor.values[field])[lang] ?? ''}
                onChange={(event) =>
                  setEditor((current) => ({
                    ...current,
                    values: {
                      ...current.values,
                      [field]: JSON.stringify({
                        ...parseTexts(current.values[field]),
                        [lang]: event.target.value,
                      }),
                    },
                  }))
                }
              />
            </TabsContent>
          ))}
        </Tabs>
      </div>
    </SettingsSection>
  )
}

/** Multilingual legal documents (user agreement, privacy policy, refund policy). */
export function AdtyLegalSection(props: { defaultValues: AdtyLegalDefaults }) {
  return (
    <DraftNavigationProvider>
      <LegalEditor {...props} />
    </DraftNavigationProvider>
  )
}
