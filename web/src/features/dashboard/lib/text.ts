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
export const TRANSLATION_NOT_READY_MARKERS = [
  '英文译文未就绪',
  'English is not ready',
] as const

const TRANSLATION_NOT_READY_MARKER_SET = new Set(
  TRANSLATION_NOT_READY_MARKERS.map((marker) => marker.toLowerCase())
)

const NON_TEXT_NOTIFICATION_SOURCES = new Set(['channel_watcher'])

const MARKER_CORE_EXPR = '(?:英文译文未就绪|English is not ready)[.。]?'
const WRAPPED_MARKER_EXPR = `(?:[(（\\[【]\\s*${MARKER_CORE_EXPR}\\s*[)）\\]】]|${MARKER_CORE_EXPR})`
const HTML_WRAPPED_MARKER_EXPR = `(?:<(?:p|div|span|small|em|i)\\b[^>]*>\\s*${WRAPPED_MARKER_EXPR}\\s*</(?:p|div|span|small|em|i)>|${WRAPPED_MARKER_EXPR})`

const TRAILING_NOT_READY_PATTERNS = [
  new RegExp(
    `(?:[ \\t]*\\n+[ \\t]*)+(?:(?:[-_*—–]{3,}|<hr\\s*/?>)(?:[ \\t]*\\n+[ \\t]*)+)?${HTML_WRAPPED_MARKER_EXPR}[ \\t]*$`,
    'i'
  ),
  new RegExp(
    `(?:[ \\t]*(?:<br\\s*/?>|<hr\\s*/?>)[ \\t]*)+${HTML_WRAPPED_MARKER_EXPR}[ \\t]*$`,
    'i'
  ),
  new RegExp(
    `<(p|div|small|span|em|i)\\b[^>]*>\\s*${WRAPPED_MARKER_EXPR}\\s*</\\1>[ \\t]*$`,
    'i'
  ),
  new RegExp(
    `(?:(?:[ \\t]*\\n+[ \\t]*)+|(?:[ \\t]*<br\\s*/?>[ \\t]*)+)${WRAPPED_MARKER_EXPR}(?=[ \\t]*</(?:p|div)>\\s*$)`,
    'i'
  ),
  new RegExp(
    `[ \\t]*(?:[|/—–·•]|--+| - )[ \\t]*${WRAPPED_MARKER_EXPR}[ \\t]*$`,
    'i'
  ),
  new RegExp(
    `[ \\t]*[(（\\[【]\\s*${MARKER_CORE_EXPR}\\s*[)）\\]】][ \\t]*$`,
    'i'
  ),
]

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
  id?: number | string
  title?: string
  title_en?: string
  content?: string
  content_en?: string
  english?: string
  source?: string
  translation_status?: string
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

export function isTranslationNotReadyMarker(text?: string | null): boolean {
  if (!text) return false
  const plain = extractAnnouncementPlainText(text)
    .replaceAll(/^[([（【\s\-—–/|·•]+|[)\]）】\s.。!！]+$/g, '')
    .trim()
    .toLowerCase()
  return TRANSLATION_NOT_READY_MARKER_SET.has(plain)
}

function stripTrailingSection(
  content: string,
  suffix: string,
  caseInsensitive = false
): string {
  const trimmedContent = content.trim()
  const trimmedSuffix = suffix.trim()
  if (!trimmedContent || !trimmedSuffix) return trimmedContent

  const normalizedContent = trimmedContent
    .replaceAll('\r\n', '\n')
    .replaceAll('\r', '\n')
  const normalizedSuffix = trimmedSuffix
    .replaceAll('\r\n', '\n')
    .replaceAll('\r', '\n')
  const doubleNewlineMarker = `\n\n${normalizedSuffix}`
  if (
    caseInsensitive
      ? normalizedContent
          .toLowerCase()
          .endsWith(doubleNewlineMarker.toLowerCase())
      : normalizedContent.endsWith(doubleNewlineMarker)
  ) {
    return normalizedContent
      .slice(0, normalizedContent.length - doubleNewlineMarker.length)
      .trim()
  }

  let splitStart = -1
  let splitEnd = -1
  for (const match of normalizedContent.matchAll(/\n[ \t]*\n/g)) {
    if (match.index !== undefined) {
      splitStart = match.index
      splitEnd = match.index + match[0].length
    }
  }
  if (splitStart > 0) {
    const head = normalizedContent.slice(0, splitStart).trim()
    const tail = normalizedContent.slice(splitEnd).trim()
    if (head && tail) {
      const tailPlain = extractAnnouncementPlainText(tail)
      const suffixPlain = extractAnnouncementPlainText(normalizedSuffix)
      const matched = caseInsensitive
        ? tailPlain.toLowerCase() === suffixPlain.toLowerCase()
        : tailPlain === suffixPlain
      if (matched) {
        return head
      }
    }
  }

  return trimmedContent
}

export function stripTranslationNotReadyMarkers(content: string): string {
  let current = content.trim().replaceAll('\r\n', '\n').replaceAll('\r', '\n')
  if (!current) return ''
  if (isTranslationNotReadyMarker(current)) {
    return ''
  }

  for (let pass = 0; pass < 4; pass += 1) {
    let next = current
    for (const marker of TRANSLATION_NOT_READY_MARKERS) {
      next = stripTrailingSection(next, marker, true)
    }
    for (const pattern of TRAILING_NOT_READY_PATTERNS) {
      next = next.replace(pattern, '').trim()
    }
    if (isTranslationNotReadyMarker(next)) {
      return ''
    }
    if (next === current) {
      break
    }
    current = next
  }

  return current
}

export function extractChineseAnnouncementContent(
  content: string,
  contentEn?: string
): string {
  let current = stripTranslationNotReadyMarkers(content)
  if (!current) return ''
  const trimmedEn = contentEn?.trim() ?? ''
  if (trimmedEn && !isTranslationNotReadyMarker(trimmedEn)) {
    current = stripTrailingSection(current, trimmedEn)
  }
  return stripTranslationNotReadyMarkers(current)
}

function resolveChineseSourceText(
  item: LocalizedAnnouncementFields,
  rawContent: string
): string {
  const rawSource = item.source?.trim() ?? ''
  if (!rawSource) return ''
  if (NON_TEXT_NOTIFICATION_SOURCES.has(rawSource.toLowerCase())) {
    return ''
  }
  const idStr = item.id !== undefined && item.id !== null ? String(item.id) : ''
  if (idStr.startsWith('watcher:')) {
    return ''
  }
  const hasTranslationMeta =
    typeof item.translation_status === 'string' ||
    typeof item.content_en === 'string' ||
    typeof item.english === 'string'
  if (!hasTranslationMeta) {
    return ''
  }
  const cleanedSource = stripTranslationNotReadyMarkers(rawSource)
  if (!cleanedSource) return ''
  const rawPlain = extractAnnouncementPlainText(rawContent)
  const srcPlain = extractAnnouncementPlainText(cleanedSource)
  if (rawPlain && srcPlain && !rawPlain.includes(srcPlain)) {
    return ''
  }
  return cleanedSource
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
  const rawTitleEn = item.title_en?.trim() ?? ''
  const titleEn = isTranslationNotReadyMarker(rawTitleEn) ? '' : rawTitleEn
  const title = isEnglish ? titleEn || titleZh : titleZh || titleEn

  const rawContent = item.content?.trim() ?? ''
  const rawContentEn = item.content_en?.trim() || item.english?.trim() || ''
  const contentEn = isTranslationNotReadyMarker(rawContentEn)
    ? ''
    : rawContentEn
  const extractedChinese = extractChineseAnnouncementContent(
    rawContent,
    contentEn
  )
  const sourceChinese = resolveChineseSourceText(item, rawContent)
  const chineseContent =
    sourceChinese &&
    (contentEn !== '' ||
      extractedChinese !== rawContent ||
      extractAnnouncementPlainText(rawContent).startsWith(
        extractAnnouncementPlainText(sourceChinese)
      ))
      ? sourceChinese
      : extractedChinese

  const content = isEnglish
    ? contentEn || chineseContent
    : chineseContent

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
