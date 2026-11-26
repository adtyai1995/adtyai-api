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
import DOMPurify from 'dompurify'

export const MAX_NOTIFICATION_PLAIN_LENGTH = 2000
export const MAX_NOTIFICATION_RAW_BYTES = 32 * 1024

export const NOTIFICATION_HEADINGS = ['p', 'h1', 'h2', 'h3'] as const
export type NotificationHeadingTag = (typeof NOTIFICATION_HEADINGS)[number]

export const NOTIFICATION_FONT_SIZES = [
  '12px',
  '14px',
  '16px',
  '18px',
  '20px',
  '24px',
] as const
export type NotificationFontSize = (typeof NOTIFICATION_FONT_SIZES)[number]

export const NOTIFICATION_COLOR_PRESETS = [
  { value: '#111827', label: 'Ink' },
  { value: '#4b5563', label: 'Slate' },
  { value: '#dc2626', label: 'Red' },
  { value: '#ea580c', label: 'Orange' },
  { value: '#d97706', label: 'Amber' },
  { value: '#16a34a', label: 'Green' },
  { value: '#2563eb', label: 'Blue' },
  { value: '#7c3aed', label: 'Purple' },
] as const

const ALLOWED_NOTIFICATION_TAGS = [
  'h1',
  'h2',
  'h3',
  'p',
  'div',
  'br',
  'strong',
  'b',
  'em',
  'i',
  'u',
  'span',
  'font',
  'ul',
  'ol',
  'li',
  'blockquote',
]

const FONT_SIZE_LEGACY_MAP: Record<string, NotificationFontSize> = {
  '1': '12px',
  '2': '14px',
  '3': '16px',
  '4': '18px',
  '5': '20px',
  '6': '24px',
  '7': '24px',
}

const HEX_COLOR_RE = /^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/
const RGB_COLOR_RE =
  /^rgb\(\s*(\d{1,3})\s*,\s*(\d{1,3})\s*,\s*(\d{1,3})\s*\)$/i
const FONT_SIZE_RE = /^(\d+(?:\.\d+)?)(px|rem|em)$/i

export function sanitizeCssColor(raw: string | null | undefined): string | null {
  const value = (raw ?? '').trim()
  if (!value) return null
  if (HEX_COLOR_RE.test(value)) {
    return value.toLowerCase()
  }
  const rgbMatch = RGB_COLOR_RE.exec(value)
  if (rgbMatch) {
    const r = Number(rgbMatch[1])
    const g = Number(rgbMatch[2])
    const b = Number(rgbMatch[3])
    if (r <= 255 && g <= 255 && b <= 255) {
      return `rgb(${r}, ${g}, ${b})`
    }
  }
  return null
}

export function sanitizeCssFontSize(
  raw: string | null | undefined
): string | null {
  const value = (raw ?? '').trim().toLowerCase()
  if (!value) return null
  const match = FONT_SIZE_RE.exec(value)
  if (!match) return null
  const num = Number(match[1])
  if (!Number.isFinite(num)) return null
  const unit = match[2]
  const px = unit === 'rem' || unit === 'em' ? num * 16 : num
  if (px < 10 || px > 32) return null
  return `${match[1]}${unit}`
}

const ALLOWED_FONT_WEIGHTS = new Set([
  'normal',
  'bold',
  '400',
  '500',
  '600',
  '700',
])

function sanitizeInlineStyle(styleValue: string): string {
  const declarations = styleValue.split(';')
  const order: string[] = []
  const values = new Map<string, string>()
  for (const decl of declarations) {
    const colonIndex = decl.indexOf(':')
    if (colonIndex <= 0) continue
    const prop = decl.slice(0, colonIndex).trim().toLowerCase()
    const rawVal = decl.slice(colonIndex + 1).trim()
    let cleaned: string | null = null
    if (prop === 'color') {
      cleaned = sanitizeCssColor(rawVal)
    } else if (prop === 'font-size') {
      cleaned = sanitizeCssFontSize(rawVal)
    } else if (prop === 'font-weight') {
      const lower = rawVal.toLowerCase()
      if (ALLOWED_FONT_WEIGHTS.has(lower)) {
        cleaned = lower
      }
    }
    if (!cleaned) continue
    if (!values.has(prop)) {
      order.push(prop)
    }
    values.set(prop, cleaned)
  }
  return order.map((prop) => `${prop}: ${values.get(prop)}`).join('; ')
}

export function extractPlainText(html: string): string {
  if (!html) return ''
  if (typeof document !== 'undefined') {
    const template = document.createElement('template')
    template.innerHTML = DOMPurify.sanitize(html, {
      ALLOWED_TAGS: ALLOWED_NOTIFICATION_TAGS,
      ALLOWED_ATTR: [],
    })
    template.content.querySelectorAll('br').forEach((br) => {
      br.replaceWith('\n')
    })
    const blockTags = 'p,div,h1,h2,h3,li'
    template.content.querySelectorAll(blockTags).forEach((el, idx) => {
      if (idx > 0 && el.previousSibling) {
        el.before('\n')
      }
    })
    const raw = (template.content.textContent ?? '')
      .replaceAll('\u200B', '')
      .replaceAll('\u00A0', ' ')
      .replaceAll('\r\n', '\n')
      .replaceAll('\r', '\n')
    return raw.trim()
  }
  return html
    .replaceAll('\u200B', '')
    .replaceAll(/<br\s*\/?>/gi, '\n')
    .replaceAll(/<\/(?:p|div|h[1-6]|li)>/gi, '\n')
    .replaceAll(/<[^>]+>/g, '')
    .replaceAll('&nbsp;', ' ')
    .replaceAll('&amp;', '&')
    .replaceAll('&lt;', '<')
    .replaceAll('&gt;', '>')
    .replaceAll('&quot;', '"')
    .trim()
}

export function getNotificationPlainLength(html: string): number {
  const plain = extractPlainText(html)
  if (!plain) return 0
  return [...plain].length
}

export function sanitizeNotificationHtml(rawHtml: string): string {
  const trimmed = rawHtml.trim()
  if (!trimmed) return ''

  const purified = DOMPurify.sanitize(trimmed, {
    ALLOWED_TAGS: ALLOWED_NOTIFICATION_TAGS,
    ALLOWED_ATTR: ['style', 'color', 'size'],
  })

  if (typeof document === 'undefined') {
    return getNotificationPlainLength(purified) === 0 ? '' : purified
  }

  const template = document.createElement('template')
  template.innerHTML = purified

  // Normalize legacy <font color="..." size="..."> tags generated by execCommand
  const fonts = [...template.content.querySelectorAll('font')]
  for (const fontEl of fonts) {
    const span = document.createElement('span')
    const styles: string[] = []
    const existingStyle = fontEl.getAttribute('style')
    if (existingStyle) {
      const cleaned = sanitizeInlineStyle(existingStyle)
      if (cleaned) styles.push(cleaned)
    }
    const safeColor = sanitizeCssColor(fontEl.getAttribute('color'))
    if (safeColor) styles.push(`color: ${safeColor}`)
    const legacySize = fontEl.getAttribute('size')
    if (legacySize && Object.hasOwn(FONT_SIZE_LEGACY_MAP, legacySize)) {
      styles.push(`font-size: ${FONT_SIZE_LEGACY_MAP[legacySize]}`)
    }
    if (styles.length > 0) {
      span.setAttribute('style', styles.join('; '))
    }
    while (fontEl.firstChild) {
      span.append(fontEl.firstChild)
    }
    fontEl.replaceWith(span)
  }

  // Filter every element's inline style down to safe color / font-size / font-weight
  template.content.querySelectorAll('*').forEach((el) => {
    el.removeAttribute('color')
    el.removeAttribute('size')
    const styleAttr = el.getAttribute('style')
    if (styleAttr !== null) {
      const safe = sanitizeInlineStyle(styleAttr)
      if (safe) {
        el.setAttribute('style', safe)
      } else {
        el.removeAttribute('style')
      }
    }
  })

  const normalized = template.innerHTML.trim()
  if (getNotificationPlainLength(normalized) === 0) {
    return ''
  }
  return normalized
}

export function isHtmlNotificationContent(
  content: string,
  contentFormat?: string
): boolean {
  if (contentFormat === 'html') return true
  if (!content) return false
  return /^\s*<(?:h[1-6]|p|div|span|strong|b|em|i|u|ul|ol|li|br|blockquote)\b/i.test(
    content
  )
}

function escapeHtml(text: string): string {
  return text
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
}

export function toInitialEditorHtml(
  content: string,
  contentFormat?: string
): string {
  const trimmed = content.trim()
  if (!trimmed) return ''
  if (isHtmlNotificationContent(trimmed, contentFormat)) {
    return sanitizeNotificationHtml(trimmed)
  }
  const paragraphs = trimmed.split(/\r?\n/)
  const html = paragraphs
    .map((line) => (line ? `<p>${escapeHtml(line)}</p>` : '<p><br></p>'))
    .join('')
  return sanitizeNotificationHtml(html)
}
