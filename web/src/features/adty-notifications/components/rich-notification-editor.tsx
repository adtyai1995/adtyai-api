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
import { Bold, Palette } from 'lucide-react'
import { useCallback, useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { cn } from '@/lib/utils'

import {
  getNotificationPlainLength,
  MAX_NOTIFICATION_PLAIN_LENGTH,
  NOTIFICATION_COLOR_PRESETS,
  NOTIFICATION_FONT_SIZES,
  NOTIFICATION_HEADINGS,
  sanitizeCssColor,
  sanitizeNotificationHtml,
  type NotificationHeadingTag,
} from '../lib/rich-text'

export interface RichNotificationEditorProps {
  id?: string
  value: string
  onChange: (html: string) => void
  disabled?: boolean
  invalid?: boolean
  placeholder?: string
}

const HEADING_LABELS: Record<NotificationHeadingTag, string> = {
  p: 'Normal',
  h1: 'Heading 1',
  h2: 'Heading 2',
  h3: 'Heading 3',
}

function getSelectionRangeInside(root: HTMLElement | null): Range | null {
  if (!root || typeof window === 'undefined') return null
  const selection = window.getSelection()
  if (!selection || selection.rangeCount === 0) return null
  const range = selection.getRangeAt(0)
  if (!root.contains(range.commonAncestorContainer)) return null
  return range.cloneRange()
}

function restoreSelectionRange(
  root: HTMLElement | null,
  savedRange: Range | null
): Range | null {
  if (!root || typeof window === 'undefined') return null
  root.focus()
  const selection = window.getSelection()
  if (!selection) return null
  if (savedRange && root.contains(savedRange.commonAncestorContainer)) {
    selection.removeAllRanges()
    selection.addRange(savedRange)
    return savedRange
  }
  const range = document.createRange()
  range.selectNodeContents(root)
  range.collapse(false)
  selection.removeAllRanges()
  selection.addRange(range)
  return range
}

function detectCurrentHeading(root: HTMLElement | null): NotificationHeadingTag {
  if (!root || typeof window === 'undefined') return 'p'
  const selection = window.getSelection()
  if (!selection || selection.rangeCount === 0) return 'p'
  let node: Node | null = selection.anchorNode
  while (node && node !== root) {
    if (node instanceof HTMLElement) {
      const tag = node.tagName.toLowerCase()
      if (tag === 'h1' || tag === 'h2' || tag === 'h3') {
        return tag
      }
    }
    node = node.parentNode
  }
  return 'p'
}

export function RichNotificationEditor(props: RichNotificationEditorProps) {
  const { t } = useTranslation()
  const generatedId = useId()
  const editorId = props.id ?? generatedId
  const editorRef = useRef<HTMLDivElement>(null)
  const savedRangeRef = useRef<Range | null>(null)
  const lastEmittedRef = useRef<string>(props.value)

  const [heading, setHeading] = useState<NotificationHeadingTag>('p')
  const [fontSize, setFontSize] = useState<string>('')
  const [isBold, setIsBold] = useState(false)
  const [currentColor, setCurrentColor] = useState('#111827')
  const [colorPopoverOpen, setColorPopoverOpen] = useState(false)

  const plainLength = getNotificationPlainLength(props.value)
  const isOverLimit = plainLength > MAX_NOTIFICATION_PLAIN_LENGTH

  useEffect(() => {
    const el = editorRef.current
    if (!el) return
    if (props.value !== lastEmittedRef.current) {
      el.innerHTML = sanitizeNotificationHtml(props.value)
      lastEmittedRef.current = props.value
    } else if (el.innerHTML === '' && props.value !== '') {
      el.innerHTML = sanitizeNotificationHtml(props.value)
    }
  }, [props.value])

  const syncToolbarState = useCallback(() => {
    const el = editorRef.current
    const range = getSelectionRangeInside(el)
    if (range) {
      savedRangeRef.current = range
    }
    setHeading(detectCurrentHeading(el))
    if (typeof document.queryCommandState === 'function') {
      try {
        setIsBold(Boolean(document.queryCommandState('bold')))
      } catch {
        setIsBold(false)
      }
    }
  }, [])

  const emitCurrentHtml = useCallback(() => {
    const el = editorRef.current
    if (!el) return
    const sanitized = sanitizeNotificationHtml(el.innerHTML)
    lastEmittedRef.current = sanitized
    props.onChange(sanitized)
  }, [props])

  const applyHeading = (nextHeading: NotificationHeadingTag) => {
    if (props.disabled) return
    const el = editorRef.current
    restoreSelectionRange(el, savedRangeRef.current)
    let executed = false
    if (typeof document.execCommand === 'function') {
      try {
        executed = document.execCommand('formatBlock', false, `<${nextHeading}>`)
        if (!executed) {
          executed = document.execCommand('formatBlock', false, nextHeading)
        }
      } catch {
        executed = false
      }
    }
    if (!executed && el) {
      const range = getSelectionRangeInside(el)
      if (range && !range.collapsed) {
        const block = document.createElement(nextHeading)
        block.append(range.extractContents())
        range.insertNode(block)
      }
    }
    setHeading(nextHeading)
    emitCurrentHtml()
    syncToolbarState()
  }

  const applyBold = () => {
    if (props.disabled) return
    const el = editorRef.current
    restoreSelectionRange(el, savedRangeRef.current)
    let executed = false
    if (typeof document.execCommand === 'function') {
      try {
        executed = document.execCommand('bold', false)
      } catch {
        executed = false
      }
    }
    if (!executed && el) {
      const range = getSelectionRangeInside(el)
      if (range && !range.collapsed) {
        const strong = document.createElement('strong')
        strong.append(range.extractContents())
        range.insertNode(strong)
      }
    }
    emitCurrentHtml()
    syncToolbarState()
  }

  const applyFontSize = (nextSize: string) => {
    setFontSize(nextSize)
    if (props.disabled || !nextSize) return
    const el = editorRef.current
    const range = restoreSelectionRange(el, savedRangeRef.current)
    if (!el || !range) return

    let applied = false
    if (!range.collapsed && typeof document.execCommand === 'function') {
      try {
        if (document.execCommand('fontSize', false, '7')) {
          const generatedFonts = el.querySelectorAll('font[size="7"]')
          if (generatedFonts.length > 0) {
            generatedFonts.forEach((fontEl) => {
              const span = document.createElement('span')
              span.style.fontSize = nextSize
              while (fontEl.firstChild) {
                span.append(fontEl.firstChild)
              }
              fontEl.replaceWith(span)
            })
            applied = true
          }
        }
      } catch {
        applied = false
      }
    }

    if (!applied) {
      const span = document.createElement('span')
      span.style.fontSize = nextSize
      if (range.collapsed) {
        span.textContent = '\u200B'
        range.insertNode(span)
        const nextRange = document.createRange()
        nextRange.setStart(span, 1)
        nextRange.collapse(true)
        const selection = window.getSelection()
        selection?.removeAllRanges()
        selection?.addRange(nextRange)
        savedRangeRef.current = nextRange.cloneRange()
      } else {
        span.append(range.extractContents())
        range.insertNode(span)
      }
    }

    emitCurrentHtml()
    syncToolbarState()
  }

  const applyTextColor = (rawColor: string) => {
    if (props.disabled) return
    const el = editorRef.current
    const range = restoreSelectionRange(el, savedRangeRef.current)
    if (!el || !range) return

    if (rawColor === '') {
      if (typeof document.execCommand === 'function') {
        try {
          document.execCommand('removeFormat', false)
        } catch {
          // Ignore unsupported command in test environments
        }
      }
      setCurrentColor('#111827')
      setColorPopoverOpen(false)
      emitCurrentHtml()
      syncToolbarState()
      return
    }

    const safeColor = sanitizeCssColor(rawColor)
    if (!safeColor) return
    setCurrentColor(safeColor)

    let applied = false
    if (!range.collapsed && typeof document.execCommand === 'function') {
      try {
        applied = document.execCommand('foreColor', false, safeColor)
      } catch {
        applied = false
      }
    }

    if (!applied) {
      const span = document.createElement('span')
      span.style.color = safeColor
      if (range.collapsed) {
        span.textContent = '\u200B'
        range.insertNode(span)
        const nextRange = document.createRange()
        nextRange.setStart(span, 1)
        nextRange.collapse(true)
        const selection = window.getSelection()
        selection?.removeAllRanges()
        selection?.addRange(nextRange)
        savedRangeRef.current = nextRange.cloneRange()
      } else {
        span.append(range.extractContents())
        range.insertNode(span)
      }
    }

    setColorPopoverOpen(false)
    emitCurrentHtml()
    syncToolbarState()
  }

  const handlePaste = (event: React.ClipboardEvent<HTMLDivElement>) => {
    if (props.disabled) return
    event.preventDefault()
    const plain = event.clipboardData.getData('text/plain')
    if (!plain) return
    let inserted = false
    if (typeof document.execCommand === 'function') {
      try {
        inserted = document.execCommand('insertText', false, plain)
      } catch {
        inserted = false
      }
    }
    if (!inserted) {
      const range = getSelectionRangeInside(editorRef.current)
      if (range) {
        range.deleteContents()
        const textNode = document.createTextNode(plain)
        range.insertNode(textNode)
        range.setStartAfter(textNode)
        range.collapse(true)
      } else if (editorRef.current) {
        editorRef.current.append(document.createTextNode(plain))
      }
    }
    emitCurrentHtml()
    syncToolbarState()
  }

  return (
    <div
      className={cn(
        'border-input bg-background overflow-hidden rounded-lg border',
        (props.invalid || isOverLimit) && 'border-destructive',
        props.disabled && 'opacity-60'
      )}
    >
      <div
        role='toolbar'
        aria-label={t('Text formatting')}
        className='bg-muted/40 flex flex-wrap items-center gap-1.5 border-b px-2 py-1.5'
      >
        <NativeSelect
          aria-label={t('Text style')}
          disabled={props.disabled}
          value={heading}
          className='h-8 text-xs'
          onChange={(event) =>
            applyHeading(event.target.value as NotificationHeadingTag)
          }
        >
          {NOTIFICATION_HEADINGS.map((tag) => (
            <NativeSelectOption key={tag} value={tag}>
              {t(HEADING_LABELS[tag])}
            </NativeSelectOption>
          ))}
        </NativeSelect>

        <NativeSelect
          aria-label={t('Font size')}
          disabled={props.disabled}
          value={fontSize}
          className='h-8 text-xs'
          onChange={(event) => applyFontSize(event.target.value)}
        >
          <NativeSelectOption value=''>{t('Font size')}</NativeSelectOption>
          {NOTIFICATION_FONT_SIZES.map((size) => (
            <NativeSelectOption key={size} value={size}>
              {size}
            </NativeSelectOption>
          ))}
        </NativeSelect>

        <Button
          type='button'
          size='sm'
          variant={isBold ? 'secondary' : 'outline'}
          aria-label={t('Bold')}
          aria-pressed={isBold}
          disabled={props.disabled}
          onMouseDown={(event) => event.preventDefault()}
          onClick={applyBold}
        >
          <Bold className='size-3.5' aria-hidden='true' />
          <span className='sr-only'>{t('Bold')}</span>
        </Button>

        <Popover open={colorPopoverOpen} onOpenChange={setColorPopoverOpen}>
          <PopoverTrigger
            render={
              <Button
                type='button'
                size='sm'
                variant='outline'
                aria-label={t('Text color')}
                disabled={props.disabled}
                onMouseDown={(event) => {
                  // Save range before popover focus change
                  const range = getSelectionRangeInside(editorRef.current)
                  if (range) savedRangeRef.current = range
                  event.preventDefault()
                }}
              />
            }
          >
            <Palette className='size-3.5' aria-hidden='true' />
            <span
              className='border-border inline-block size-3 rounded-xs border'
              style={{ backgroundColor: currentColor }}
              aria-hidden='true'
            />
          </PopoverTrigger>
          <PopoverContent align='start' className='w-56 space-y-2.5 p-3'>
            <div className='text-xs font-medium'>{t('Text color')}</div>
            <div className='grid grid-cols-4 gap-1.5'>
              {NOTIFICATION_COLOR_PRESETS.map((preset) => (
                <button
                  key={preset.value}
                  type='button'
                  aria-label={`${t('Text color')} ${preset.value}`}
                  className={cn(
                    'border-border flex h-7 w-full items-center justify-center rounded-md border transition-transform hover:scale-105 focus-visible:ring-2 focus-visible:outline-none',
                    currentColor === preset.value && 'ring-primary ring-2'
                  )}
                  style={{ backgroundColor: preset.value }}
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={() => applyTextColor(preset.value)}
                />
              ))}
            </div>
            <div className='flex items-center justify-between gap-2 border-t pt-2'>
              <label className='flex items-center gap-2 text-xs'>
                <span>{t('Custom text color')}</span>
                <input
                  type='color'
                  aria-label={t('Custom text color')}
                  value={currentColor.startsWith('#') ? currentColor : '#111827'}
                  onChange={(event) => applyTextColor(event.target.value)}
                  className='h-6 w-8 cursor-pointer rounded border bg-transparent p-0'
                />
              </label>
              <Button
                type='button'
                size='xs'
                variant='ghost'
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => applyTextColor('')}
              >
                {t('Default color')}
              </Button>
            </div>
          </PopoverContent>
        </Popover>
      </div>

      <div
        id={editorId}
        ref={editorRef}
        role='textbox'
        tabIndex={props.disabled ? -1 : 0}
        aria-multiline='true'
        aria-invalid={props.invalid || isOverLimit}
        aria-disabled={props.disabled}
        contentEditable={!props.disabled}
        suppressContentEditableWarning
        data-placeholder={props.placeholder ?? ''}
        onInput={() => {
          emitCurrentHtml()
          syncToolbarState()
        }}
        onKeyUp={syncToolbarState}
        onMouseUp={syncToolbarState}
        onSelect={syncToolbarState}
        onPaste={handlePaste}
        className={cn(
          'min-h-40 max-h-80 overflow-y-auto px-3 py-2 text-sm leading-relaxed break-words outline-none',
          '[&_h1]:mt-2 [&_h1]:mb-1 [&_h1]:text-2xl [&_h1]:font-bold',
          '[&_h2]:mt-2 [&_h2]:mb-1 [&_h2]:text-xl [&_h2]:font-semibold',
          '[&_h3]:mt-1.5 [&_h3]:mb-1 [&_h3]:text-lg [&_h3]:font-semibold',
          '[&_p]:my-1 [&_strong]:font-bold [&_b]:font-bold',
          '[&_ul]:list-disc [&_ul]:pl-5 [&_ol]:list-decimal [&_ol]:pl-5',
          'empty:before:text-muted-foreground empty:before:pointer-events-none empty:before:content-[attr(data-placeholder)]'
        )}
      />

      <div className='bg-muted/20 flex items-center justify-between border-t px-3 py-1.5 text-xs'>
        <span className='text-muted-foreground'>
          {isOverLimit ? t('Content cannot exceed 2000 characters') : ''}
        </span>
        <span
          data-testid='rich-notification-char-count'
          className={cn(
            'tabular-nums',
            isOverLimit ? 'text-destructive font-medium' : 'text-muted-foreground'
          )}
        >
          {plainLength} / {MAX_NOTIFICATION_PLAIN_LENGTH}
        </span>
      </div>
    </div>
  )
}
