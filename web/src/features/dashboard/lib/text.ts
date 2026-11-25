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
const TRANSLATION_NOT_READY_MARKER = 'English is not ready'

const NAMED_HTML_ENTITIES: Record<string, string> = {
  nbsp: ' ',
  amp: '&',
  lt: '<',
  gt: '>',
  quot: '"',
  apos: "'",
  mdash: '—',
  ndash: '–',
  hellip: '…',
  copy: '©',
  reg: '®',
  trade: '™',
  middot: '·',
  bull: '•',
  laquo: '«',
  raquo: '»',
  lsquo: '‘',
  rsquo: '’',
  ldquo: '“',
  rdquo: '”',
  times: '×',
  divide: '÷',
  plusmn: '±',
}

export interface LocalizedAnnouncementFields {
  title?: string
  title_en?: string
  content?: string
  content_en?: string
  content_format?: string
  extra?: string
}

export function isEnglishLanguage(language?: string): boolean {
  return !(language ?? '').toLowerCase().startsWith('zh')
}

export function decodeHtmlEntities(text: string): string {
  if (!text || !text.includes('&')) return text
  let decoder: HTMLTextAreaElement | null = null
  return text.replaceAll(
    /&(?:#x([0-9a-fA-F]+)|#(\d+)|([a-zA-Z][a-zA-Z0-9]+));/g,
    (entity, hex?: string, dec?: string, name?: string) => {
      if (hex) {
        const code = Number.parseInt(hex, 16)
        if (Number.isInteger(code) && code >= 0 && code <= 0x10ffff) {
          return String.fromCodePoint(code)
        }
        return entity
      }
      if (dec) {
        const code = Number.parseInt(dec, 10)
        if (Number.isInteger(code) && code >= 0 && code <= 0x10ffff) {
          return String.fromCodePoint(code)
        }
        return entity
      }
      if (name) {
        const lower = name.toLowerCase()
        if (Object.hasOwn(NAMED_HTML_ENTITIES, lower)) {
          return NAMED_HTML_ENTITIES[lower]
        }
        if (typeof document !== 'undefined') {
          if (!decoder) {
            decoder = document.createElement('textarea')
          }
          decoder.innerHTML = entity
          return decoder.value
        }
      }
      return entity
    }
  )
}

/**
 * Extract readable plain text from HTML or Markdown announcement content,
 * decoding HTML entities and preserving paragraph breaks.
 */
export function extractAnnouncementPlainText(content: string): string {
  if (!content) return ''
  const withoutTags = content
    .replaceAll(/<!--[\s\S]*?-->/g, '')
    .replaceAll(/<(script|style)\b[^>]*>[\s\S]*?<\/\1>/gi, '')
    .replaceAll(/<br\s*\/?>/gi, '\n')
    .replaceAll(
      /<\/?(?:p|div|h[1-6]|li|blockquote|tr|section|article|header|footer)\b[^>]*>/gi,
      '\n'
    )
    .replaceAll(/<\/?[a-zA-Z][^>]*>/g, '')

  const decoded = decodeHtmlEntities(withoutTags)
    .replaceAll('\u200B', '')
    .replaceAll('\u00A0', ' ')
    .replaceAll(/!\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replaceAll(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .replaceAll(/[#*_`~]/g, '')
    .replaceAll('\r\n', '\n')
    .replaceAll('\r', '\n')

  return decoded
    .split('\n')
    .map((line) => line.replaceAll(/[ \t]+/g, ' ').trim())
    .filter(Boolean)
    .join('\n')
}

function stripTrailingSection(content: string, suffix: string): string {
  const trimmedContent = content.trim()
  const trimmedSuffix = suffix.trim()
  if (!trimmedContent || !trimmedSuffix) return trimmedContent

  const normalizedContent = trimmedContent.replaceAll('\r\n', '\n')
  const normalizedSuffix = trimmedSuffix.replaceAll('\r\n', '\n')
  const doubleNewlineMarker = `\n\n${normalizedSuffix}`
  if (normalizedContent.endsWith(doubleNewlineMarker)) {
    return normalizedContent
      .slice(0, normalizedContent.length - doubleNewlineMarker.length)
      .trim()
  }

  const splitIndex = normalizedContent.lastIndexOf('\n\n')
  if (splitIndex > 0) {
    const head = normalizedContent.slice(0, splitIndex).trim()
    const tail = normalizedContent.slice(splitIndex + 2).trim()
    if (
      head &&
      tail &&
      extractAnnouncementPlainText(tail) ===
        extractAnnouncementPlainText(normalizedSuffix)
    ) {
      return head
    }
  }

  return trimmedContent
}

export function extractChineseAnnouncementContent(
  content: string,
  contentEn?: string
): string {
  let current = content.trim()
  if (!current) return ''
  if (contentEn?.trim()) {
    current = stripTrailingSection(current, contentEn)
  }
  current = stripTrailingSection(current, TRANSLATION_NOT_READY_MARKER)
  return current
}

export function resolveLocalizedAnnouncement(
  item: LocalizedAnnouncementFields | null | undefined,
  language?: string
): {
  title: string
  content: string
  extra: string
} {
  if (!item) {
    return { title: '', content: '', extra: '' }
  }
  const isEnglish = isEnglishLanguage(language)
  const titleZh = item.title?.trim() ?? ''
  const titleEn = item.title_en?.trim() ?? ''
  const title = isEnglish ? titleEn || titleZh : titleZh || titleEn

  const rawContent = item.content?.trim() ?? ''
  const contentEn = item.content_en?.trim() ?? ''
  const content = isEnglish
    ? contentEn || rawContent
    : extractChineseAnnouncementContent(rawContent, contentEn)

  const rawExtra = item.extra?.trim() ?? ''
  const bilingualTitle = titleZh && titleEn ? `${titleZh} / ${titleEn}` : ''
  const isDuplicateTitleExtra =
    rawExtra !== '' &&
    (rawExtra === title ||
      rawExtra === titleZh ||
      rawExtra === titleEn ||
      (bilingualTitle !== '' && rawExtra === bilingualTitle))
  const extra = isDuplicateTitleExtra ? '' : rawExtra

  return { title, content, extra }
}

/**
 * Get plain text preview (strip HTML tags and Markdown formatting,
 * decode HTML entities, and keep paragraph separation)
 */
export function getPreviewText(
  content: string,
  maxLength: number = 60
): string {
  if (!content) return ''
  const plainText = extractAnnouncementPlainText(content)
  const chars = [...plainText]
  return chars.length > maxLength
    ? `${chars.slice(0, maxLength).join('').trimEnd()}...`
    : plainText
}
