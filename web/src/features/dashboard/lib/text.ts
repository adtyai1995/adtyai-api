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

const NON_TEXT_NOTIFICATION_SOURCES = new Set([
  'channel_watcher',
  'ticket_notice',
])
const HAN_CHAR_REGEX = /\p{Script=Han}/u
const ASCII_LATIN_REGEX = /[A-Za-z]/

const WRAPPER_TAGS = new Set(['p', 'div', 'span', 'small', 'em', 'i'])
const CONTAINER_TAGS = new Set(['p', 'div'])
const OPEN_BRACKETS = '([（【'
const CLOSE_BRACKETS = ')]）】'
const LEADING_MARKER_PUNCTUATION = '([（【-—–/|·•'
const TRAILING_MARKER_PUNCTUATION = ')]）】.。!！'
const RULE_LINE_CHARS = '-_*—–'
const INLINE_SINGLE_SEPARATORS = '|/—–·•'
const MAX_CLOSING_TAG_LEN = 12
const MAX_BREAK_OR_RULE_TAG_LEN = 16

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
  title_translation_status?: string
  content?: string
  content_en?: string
  english?: string
  source?: string
  translation_status?: string
  translation_notice?: string
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

function isHorizontalSpace(ch: string): boolean {
  return ch === ' ' || ch === '\t'
}

function isWhitespaceChar(ch: string): boolean {
  return (
    ch === ' ' ||
    ch === '\t' ||
    ch === '\n' ||
    ch === '\r' ||
    ch === '\f' ||
    ch === '\v' ||
    ch.trim() === ''
  )
}

function skipHorizontalSpacesBackward(text: string, pos: number): number {
  let cursor = pos
  while (cursor > 0 && isHorizontalSpace(text[cursor - 1])) {
    cursor -= 1
  }
  return cursor
}

function skipWhitespaceBackward(text: string, pos: number): number {
  let cursor = pos
  while (cursor > 0 && isWhitespaceChar(text[cursor - 1])) {
    cursor -= 1
  }
  return cursor
}

function skipSpacesAndNewlinesBackward(text: string, pos: number): number {
  let cursor = pos
  while (
    cursor > 0 &&
    (text[cursor - 1] === ' ' ||
      text[cursor - 1] === '\t' ||
      text[cursor - 1] === '\n')
  ) {
    cursor -= 1
  }
  return cursor
}

function trimMarkerPunctuation(text: string): string {
  let start = 0
  let end = text.length
  while (
    start < end &&
    (isWhitespaceChar(text[start]) ||
      LEADING_MARKER_PUNCTUATION.includes(text[start]))
  ) {
    start += 1
  }
  while (
    end > start &&
    (isWhitespaceChar(text[end - 1]) ||
      TRAILING_MARKER_PUNCTUATION.includes(text[end - 1]))
  ) {
    end -= 1
  }
  return text.slice(start, end)
}

export function isTranslationNotReadyMarker(text?: string | null): boolean {
  if (!text) return false
  const plain = trimMarkerPunctuation(extractAnnouncementPlainText(text))
    .trim()
    .toLowerCase()
  return TRANSLATION_NOT_READY_MARKER_SET.has(plain)
}

function findLastBlankLineBreak(
  text: string
): { start: number; end: number } | null {
  let i = text.length - 1
  while (i > 0) {
    if (text[i] !== '\n') {
      i -= 1
      continue
    }
    let j = i - 1
    while (j >= 0 && isHorizontalSpace(text[j])) {
      j -= 1
    }
    if (j >= 0 && text[j] === '\n') {
      return { start: j, end: i + 1 }
    }
    i = j
  }
  return null
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

  const lastBreak = findLastBlankLineBreak(normalizedContent)
  if (lastBreak && lastBreak.start > 0) {
    const head = normalizedContent.slice(0, lastBreak.start).trim()
    const tail = normalizedContent.slice(lastBreak.end).trim()
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

function matchMarkerCoreEndingAt(text: string, endPos: number): number {
  let pos = endPos
  if (pos > 0 && (text[pos - 1] === '.' || text[pos - 1] === '。')) {
    pos -= 1
  }
  for (const marker of TRANSLATION_NOT_READY_MARKERS) {
    const start = pos - marker.length
    if (start < 0) continue
    if (text.slice(start, pos).toLowerCase() === marker.toLowerCase()) {
      return start
    }
  }
  return -1
}

function matchWrappedMarkerEndingAt(
  text: string,
  endPos: number
): { start: number; hasBrackets: boolean } | null {
  if (endPos <= 0) return null
  if (CLOSE_BRACKETS.includes(text[endPos - 1])) {
    const innerEnd = skipWhitespaceBackward(text, endPos - 1)
    const coreStart = matchMarkerCoreEndingAt(text, innerEnd)
    if (coreStart < 0) return null
    const beforeCore = skipWhitespaceBackward(text, coreStart)
    if (beforeCore > 0 && OPEN_BRACKETS.includes(text[beforeCore - 1])) {
      return { start: beforeCore - 1, hasBrackets: true }
    }
    return null
  }
  const coreStart = matchMarkerCoreEndingAt(text, endPos)
  if (coreStart < 0) return null
  return { start: coreStart, hasBrackets: false }
}

function readTrailingClosingTag(
  text: string,
  endPos: number
): { tag: string; start: number } | null {
  if (endPos <= 3 || text[endPos - 1] !== '>') return null
  const minLt = Math.max(0, endPos - MAX_CLOSING_TAG_LEN)
  let lt = -1
  for (let i = endPos - 2; i >= minLt; i -= 1) {
    const ch = text[i]
    if (ch === '>') return null
    if (ch === '<') {
      lt = i
      break
    }
  }
  if (lt < 0 || text[lt + 1] !== '/') return null
  const tag = text.slice(lt + 2, endPos - 1).toLowerCase()
  if (!WRAPPER_TAGS.has(tag)) return null
  return { tag, start: lt }
}

function readTrailingOpenWrapperTag(
  text: string,
  endPos: number,
  expectedTag: string
): number {
  if (endPos <= expectedTag.length + 1 || text[endPos - 1] !== '>') return -1
  let lt = -1
  for (let i = endPos - 2; i >= 0; i -= 1) {
    const ch = text[i]
    if (ch === '>') return -1
    if (ch === '<') {
      lt = i
      break
    }
  }
  if (lt < 0) return -1
  const nameStart = lt + 1
  const nameEnd = nameStart + expectedTag.length
  if (nameEnd >= endPos) return -1
  if (text.slice(nameStart, nameEnd).toLowerCase() !== expectedTag) return -1
  const boundary = text[nameEnd]
  if (boundary !== '>' && !isWhitespaceChar(boundary)) return -1
  return lt
}

function matchHtmlWrappedMarkerEndingAt(
  text: string,
  endPos: number
): { start: number; htmlWrapped: boolean; hasBrackets: boolean } | null {
  const closing = readTrailingClosingTag(text, endPos)
  if (closing) {
    const innerEnd = skipWhitespaceBackward(text, closing.start)
    const wrapped = matchWrappedMarkerEndingAt(text, innerEnd)
    if (!wrapped) return null
    const openEnd = skipWhitespaceBackward(text, wrapped.start)
    const openStart = readTrailingOpenWrapperTag(text, openEnd, closing.tag)
    if (openStart < 0) return null
    return {
      start: openStart,
      htmlWrapped: true,
      hasBrackets: wrapped.hasBrackets,
    }
  }
  const wrapped = matchWrappedMarkerEndingAt(text, endPos)
  if (!wrapped) return null
  return {
    start: wrapped.start,
    htmlWrapped: false,
    hasBrackets: wrapped.hasBrackets,
  }
}

function readTrailingBreakOrRuleTag(
  text: string,
  endPos: number
): { tag: 'br' | 'hr'; start: number } | null {
  if (endPos <= 3 || text[endPos - 1] !== '>') return null
  const minLt = Math.max(0, endPos - MAX_BREAK_OR_RULE_TAG_LEN)
  let lt = -1
  for (let i = endPos - 2; i >= minLt; i -= 1) {
    const ch = text[i]
    if (ch === '>') return null
    if (ch === '<') {
      lt = i
      break
    }
  }
  if (lt < 0 || lt + 3 >= endPos) return null
  const tag = text.slice(lt + 1, lt + 3).toLowerCase()
  if (tag !== 'br' && tag !== 'hr') return null
  let cursor = lt + 3
  while (cursor < endPos - 1 && isWhitespaceChar(text[cursor])) {
    cursor += 1
  }
  if (cursor < endPos - 1 && text[cursor] === '/') {
    cursor += 1
  }
  if (cursor !== endPos - 1) return null
  return { tag, start: lt }
}

function consumePrecedingRuleLine(text: string, pos: number): number | null {
  const hr = readTrailingBreakOrRuleTag(text, pos)
  if (hr && hr.tag === 'hr') {
    const beforeRule = skipHorizontalSpacesBackward(text, hr.start)
    if (beforeRule > 0 && text[beforeRule - 1] === '\n') {
      return skipSpacesAndNewlinesBackward(text, beforeRule)
    }
    return null
  }
  let cursor = pos
  while (cursor > 0 && RULE_LINE_CHARS.includes(text[cursor - 1])) {
    cursor -= 1
  }
  if (pos - cursor < 3) return null
  const beforeRule = skipHorizontalSpacesBackward(text, cursor)
  if (beforeRule > 0 && text[beforeRule - 1] === '\n') {
    return skipSpacesAndNewlinesBackward(text, beforeRule)
  }
  return null
}

function consumeLeadingNotReadySeparators(
  text: string,
  startPos: number,
  options: {
    allowInlineSeparators: boolean
    htmlWrapped: boolean
    hasBrackets: boolean
  }
): number | null {
  const cursor = skipHorizontalSpacesBackward(text, startPos)

  // Pattern 2: one or more <br> / <hr> tags immediately preceding the marker block.
  let breakOrRuleCount = 0
  let probe = cursor
  while (probe > 0) {
    const tag = readTrailingBreakOrRuleTag(text, probe)
    if (!tag) break
    if (!options.allowInlineSeparators && tag.tag !== 'br') break
    breakOrRuleCount += 1
    probe = skipHorizontalSpacesBackward(text, tag.start)
  }
  if (breakOrRuleCount > 0) {
    return probe
  }

  // Pattern 1: newline separator (with optional standalone rule line or <hr> above).
  if (cursor > 0 && text[cursor - 1] === '\n') {
    const afterLines = skipSpacesAndNewlinesBackward(text, cursor)
    if (options.allowInlineSeparators) {
      const withRule = consumePrecedingRuleLine(text, afterLines)
      if (withRule !== null) {
        return withRule
      }
    }
    return afterLines
  }

  if (!options.allowInlineSeparators) {
    return null
  }

  // Pattern 3: standalone matched HTML wrapper block (e.g. <p>...</p>, <span>...</span>).
  if (options.htmlWrapped) {
    return cursor
  }

  // Pattern 5: inline separator before bare/bracketed marker.
  if (cursor > 0 && INLINE_SINGLE_SEPARATORS.includes(text[cursor - 1])) {
    return skipHorizontalSpacesBackward(text, cursor - 1)
  }
  if (cursor >= 2 && text[cursor - 1] === '-' && text[cursor - 2] === '-') {
    let dashStart = cursor - 2
    while (dashStart > 0 && text[dashStart - 1] === '-') {
      dashStart -= 1
    }
    return skipHorizontalSpacesBackward(text, dashStart)
  }
  if (
    cursor > 1 &&
    text[cursor - 1] === '-' &&
    text[cursor - 2] === ' ' &&
    cursor < startPos &&
    text[cursor] === ' '
  ) {
    return skipHorizontalSpacesBackward(text, cursor - 2)
  }

  // Pattern 6: trailing bracketed marker without explicit separator.
  if (options.hasBrackets) {
    return cursor
  }

  return null
}

function stripOneTrailingNotReadyBlock(text: string): string {
  const end = skipHorizontalSpacesBackward(text, text.length)
  if (end === 0) return text

  // Direct trailing marker block (bare, bracketed, or matched HTML wrapper).
  const directBlock = matchHtmlWrappedMarkerEndingAt(text, end)
  if (directBlock) {
    const cutPos = consumeLeadingNotReadySeparators(text, directBlock.start, {
      allowInlineSeparators: true,
      htmlWrapped: directBlock.htmlWrapped,
      hasBrackets: directBlock.hasBrackets,
    })
    if (cutPos !== null) {
      return text.slice(0, cutPos).trim()
    }
  }

  // Pattern 4: trailing marker nested at the end of a </p> or </div> container.
  const containerClose = readTrailingClosingTag(text, end)
  if (containerClose && CONTAINER_TAGS.has(containerClose.tag)) {
    const innerEnd = skipHorizontalSpacesBackward(text, containerClose.start)
    const innerBlock = matchHtmlWrappedMarkerEndingAt(text, innerEnd)
    if (innerBlock && !innerBlock.htmlWrapped) {
      const cutPos = consumeLeadingNotReadySeparators(text, innerBlock.start, {
        allowInlineSeparators: false,
        htmlWrapped: false,
        hasBrackets: innerBlock.hasBrackets,
      })
      if (cutPos !== null) {
        return (
          text.slice(0, cutPos) + text.slice(containerClose.start)
        ).trim()
      }
    }
  }

  return text
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
    next = stripOneTrailingNotReadyBlock(next)
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

function isNonTextNotification(item: LocalizedAnnouncementFields): boolean {
  const rawSource = item.source?.trim().toLowerCase() ?? ''
  if (NON_TEXT_NOTIFICATION_SOURCES.has(rawSource)) {
    return true
  }
  const idStr = item.id !== undefined && item.id !== null ? String(item.id) : ''
  return idStr.startsWith('watcher:') || idStr.startsWith('ticket-notice:')
}

function titleHasBilingualEnglish(title: string): boolean {
  const sepIndex = title.indexOf(' / ')
  if (sepIndex < 0) return false
  const after = title.slice(sepIndex + 3).trim()
  return (
    after !== '' &&
    !HAN_CHAR_REGEX.test(after) &&
    ASCII_LATIN_REGEX.test(after) &&
    !isTranslationNotReadyMarker(after)
  )
}

function resolveChineseSourceText(
  item: LocalizedAnnouncementFields,
  rawContent: string
): string {
  const rawSource = item.source?.trim() ?? ''
  if (!rawSource) return ''
  if (isNonTextNotification(item)) {
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
  translationNotReady: boolean
} {
  if (!item) {
    return { title: '', content: '', extra: '', translationNotReady: false }
  }
  const isEnglish = isEnglishLanguage(language)
  const titleZh = item.title?.trim() ?? ''
  const rawTitleEn = item.title_en?.trim() ?? ''
  const titleHasNotReadyMarker = isTranslationNotReadyMarker(rawTitleEn)
  const titleEn = titleHasNotReadyMarker ? '' : rawTitleEn
  const title = isEnglish ? titleEn || titleZh : titleZh || titleEn

  const rawContent = item.content?.trim() ?? ''
  const rawContentEn = item.content_en?.trim() || item.english?.trim() || ''
  const contentEnHasNotReadyMarker = isTranslationNotReadyMarker(rawContentEn)
  const contentEn = contentEnHasNotReadyMarker ? '' : rawContentEn
  const rawContentNormalized = rawContent
    .replaceAll('\r\n', '\n')
    .replaceAll('\r', '\n')
  const contentHadNotReadyMarker =
    rawContentNormalized !== '' &&
    stripTranslationNotReadyMarkers(rawContentNormalized) !==
      rawContentNormalized
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

  const translationStatus = item.translation_status?.trim().toLowerCase() ?? ''
  const hasUnreadyStatus =
    translationStatus !== '' && translationStatus !== 'ready'
  const titleTranslationStatus =
    item.title_translation_status?.trim().toLowerCase() ?? ''
  const hasUnreadyTitleStatus =
    titleTranslationStatus !== '' && titleTranslationStatus !== 'ready'
  const hasNotReadyNotice =
    (item.translation_notice?.trim() ?? '') !== '' ||
    isTranslationNotReadyMarker(item.translation_notice)
  const titleHasChinese =
    titleZh !== '' &&
    HAN_CHAR_REGEX.test(titleZh) &&
    !titleHasBilingualEnglish(titleZh)
  const titleNotReady =
    titleEn === '' &&
    (titleHasNotReadyMarker ||
      hasUnreadyTitleStatus ||
      (!isNonTextNotification(item) &&
        titleHasChinese &&
        titleTranslationStatus !== 'ready'))
  const contentNotReady =
    contentEn === '' &&
    (contentEnHasNotReadyMarker || contentHadNotReadyMarker)
  const translationNotReady =
    isEnglish &&
    (titleNotReady ||
      contentNotReady ||
      hasUnreadyStatus ||
      hasNotReadyNotice)

  const rawExtra = item.extra?.trim() ?? ''
  const bilingualTitle = titleZh && titleEn ? `${titleZh} / ${titleEn}` : ''
  const isDuplicateTitleExtra =
    rawExtra !== '' &&
    (rawExtra === title ||
      rawExtra === titleZh ||
      rawExtra === titleEn ||
      (bilingualTitle !== '' && rawExtra === bilingualTitle))
  const extra = isDuplicateTitleExtra ? '' : rawExtra

  return { title, content, extra, translationNotReady }
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
