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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { adtySiteQueryKey } from '@/features/adty-site/api'
import { useDraftNavigation } from '@/features/system-settings/hooks/use-draft-navigation'
import {
  CONTENT_LANG_LABELS,
  CONTENT_LANGS,
  type ContentLang,
} from '@/lib/adty-lang'
import { handleServerError } from '@/lib/handle-server-error'
import { STATUS_QUERY_KEY, STATUS_STORAGE_KEY } from '@/lib/status-query'

import { saveOwnerSite, siteOwnerKeys } from '../api'
import type { OwnerSite, SiteLegalDocs } from '../types'

type LegalDocKey = 'user_agreement' | 'privacy_policy' | 'refund_policy'

const LEGAL_DOC_KEYS: ReadonlyArray<LegalDocKey> = [
  'user_agreement',
  'privacy_policy',
  'refund_policy',
]

function normalizeLangMap(
  input?: Record<string, string>
): Record<string, string> {
  const out: Record<string, string> = {}
  for (const lang of CONTENT_LANGS) {
    const trimmed = input?.[lang]?.trim() ?? ''
    if (trimmed) {
      out[lang] = trimmed
    }
  }
  return out
}

function normalizeLegalDocs(input?: SiteLegalDocs): SiteLegalDocs {
  return {
    user_agreement: normalizeLangMap(input?.user_agreement),
    privacy_policy: normalizeLangMap(input?.privacy_policy),
    refund_policy: normalizeLangMap(input?.refund_policy),
    refund_policy_enabled: Boolean(input?.refund_policy_enabled),
  }
}

function serializeLegalDocs(input?: SiteLegalDocs): string {
  return JSON.stringify(normalizeLegalDocs(input))
}

/** Per-subsite multilingual legal documents card (User Agreement, Privacy Policy, Refund Policy). */
export function SiteLegalCard(props: { site: OwnerSite }) {
  const { t } = useTranslation()
  const fieldId = useId()
  const queryClient = useQueryClient()

  const savedNormalized = normalizeLegalDocs(props.site.legal_docs)
  const savedKey = serializeLegalDocs(savedNormalized)

  const [draft, setDraft] = useState<SiteLegalDocs>(() => savedNormalized)
  const previousSavedKey = useRef(savedKey)

  useEffect(() => {
    if (previousSavedKey.current !== savedKey) {
      const nextSaved = normalizeLegalDocs(props.site.legal_docs)
      setDraft((current) =>
        serializeLegalDocs(current) === previousSavedKey.current
          ? nextSaved
          : current
      )
      previousSavedKey.current = savedKey
    }
  }, [savedKey, props.site.legal_docs])

  const dirty = serializeLegalDocs(draft) !== savedKey
  useDraftNavigation(dirty)

  const closing = props.site.status === 'closing'

  const docLabels: Record<LegalDocKey, string> = {
    user_agreement: t('User Agreement'),
    privacy_policy: t('Privacy Policy'),
    refund_policy: t('Refund Policy'),
  }

  const save = useMutation({
    mutationFn: () =>
      saveOwnerSite({ legal_docs: normalizeLegalDocs(draft) }),
    onSuccess: (updated) => {
      queryClient.setQueryData(siteOwnerKeys.site, updated)
      const nextSaved = normalizeLegalDocs(updated.legal_docs)
      previousSavedKey.current = serializeLegalDocs(nextSaved)
      setDraft(nextSaved)
      toast.success(t('Saved successfully'))
      void queryClient.invalidateQueries({ queryKey: siteOwnerKeys.all })
      void queryClient.invalidateQueries({ queryKey: adtySiteQueryKey })
      void queryClient.invalidateQueries({ queryKey: STATUS_QUERY_KEY })
      void queryClient.invalidateQueries({ queryKey: ['legal'] })
      try {
        window.localStorage.removeItem(STATUS_STORAGE_KEY)
      } catch {
        /* Storage may be disabled. */
      }
    },
    onError: (err) => handleServerError(err),
  })

  const updateDoc = (doc: LegalDocKey, lang: ContentLang, value: string) => {
    setDraft((current) => ({
      ...current,
      [doc]: {
        ...current[doc],
        [lang]: value,
      },
    }))
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Multilingual Legal Pages')}</CardTitle>
        <CardDescription>
          {t(
            'Configure your subsite legal documents independently from the main site. Readers see their language, falling back to English or Simplified Chinese when empty. Filling in the user agreement or privacy policy enables sign-up consent and footer links.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='grid gap-4'>
        <div className='flex items-center justify-between gap-4 rounded-lg border p-3'>
          <div className='space-y-0.5'>
            <Label htmlFor={`${fieldId}-refund-enabled`}>
              {t('Show the refund policy')}
            </Label>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Adds the refund policy page, its footer link and the sign-up consent.'
              )}
            </p>
          </div>
          <Switch
            id={`${fieldId}-refund-enabled`}
            checked={draft.refund_policy_enabled}
            disabled={closing || save.isPending}
            onCheckedChange={(checked) =>
              setDraft((current) => ({
                ...current,
                refund_policy_enabled: checked,
              }))
            }
          />
        </div>

        <Tabs defaultValue='en' className='gap-4'>
          <div className='overflow-x-auto pb-1'>
            <TabsList className='w-full min-w-max'>
              {CONTENT_LANGS.map((lang) => (
                <TabsTrigger key={lang} value={lang} className='px-3'>
                  {CONTENT_LANG_LABELS[lang]}
                </TabsTrigger>
              ))}
            </TabsList>
          </div>

          {CONTENT_LANGS.map((lang) => (
            <TabsContent key={lang} value={lang} className='space-y-4'>
              {LEGAL_DOC_KEYS.map((doc) => {
                const inputId = `${fieldId}-${doc}-${lang}`
                return (
                  <div key={doc} className='space-y-2'>
                    <Label htmlFor={inputId}>
                      {t('{{document}} in {{language}} (Markdown)', {
                        document: docLabels[doc],
                        language: CONTENT_LANG_LABELS[lang],
                      })}
                    </Label>
                    <Textarea
                      id={inputId}
                      rows={6}
                      className='font-mono text-xs'
                      disabled={closing || save.isPending}
                      value={draft[doc]?.[lang] ?? ''}
                      placeholder={t(
                        'Leave empty to hide this document, or enter Markdown, HTML, or an https:// URL'
                      )}
                      onChange={(event) =>
                        updateDoc(doc, lang, event.target.value)
                      }
                    />
                  </div>
                )
              })}
            </TabsContent>
          ))}
        </Tabs>

        <div className='flex flex-wrap gap-2'>
          <Button
            type='button'
            variant='brand'
            disabled={closing || !dirty || save.isPending}
            onClick={() => save.mutate()}
          >
            {t('Save legal pages')}
          </Button>
          {dirty && (
            <Button
              type='button'
              variant='outline'
              disabled={save.isPending}
              onClick={() => setDraft(savedNormalized)}
            >
              {t('Reset')}
            </Button>
          )}
        </div>
      </CardContent>
    </Card>
  )
}
