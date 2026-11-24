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
import { useEffect, useState } from 'react'

import { useTheme } from '@/context/theme-provider'

import { resolveThemeColor } from './theme-color'

/**
 * The shared chart palette driven by the original new-api `--chart-1` …
 * `--chart-5` tokens in `styles/theme.css` (and overridden by active
 * `data-theme-preset` blocks in `styles/theme-presets.css`).
 */
export const CHART_PALETTE_VARS = [
  '--chart-1',
  '--chart-2',
  '--chart-3',
  '--chart-4',
  '--chart-5',
] as const

/**
 * Multi-hue fallback matching `styles/theme.css` `--chart-1` … `--chart-5`
 * (blue, teal, green, purple, rose), used when computed styles are unavailable
 * (SSR, jsdom).
 */
export const CHART_PALETTE_FALLBACK = {
  light: ['#2563eb', '#0d9488', '#16a34a', '#9333ea', '#e11d48'],
  dark: ['#60a5fa', '#2dd4bf', '#4ade80', '#c084fc', '#fb7185'],
} as const

const HEX_COLOUR = /^#(?:[0-9a-f]{3}|[0-9a-f]{6}|[0-9a-f]{8})$/i

function isDarkTheme(): boolean {
  if (typeof document === 'undefined') return false
  return document.documentElement.classList.contains('dark')
}

/**
 * Resolve one CSS token to a colour canvas charts accept. Reads from `<body>`
 * first so `data-theme-preset` overrides apply; hex values are used directly
 * and `oklch()` / `color-mix()` tokens are normalized through `resolveThemeColor`.
 */
function readPaletteEntry(token: string, fallback: string): string {
  if (typeof document === 'undefined') return fallback
  const target = document.body ?? document.documentElement
  const raw = getComputedStyle(target).getPropertyValue(token).trim()
  if (HEX_COLOUR.test(raw)) return raw
  if (!raw) return fallback
  return resolveThemeColor(token) ?? fallback
}

/** The palette for the active theme and preset, read from CSS variables. */
export function readChartPalette(): string[] {
  const fallback = isDarkTheme()
    ? CHART_PALETTE_FALLBACK.dark
    : CHART_PALETTE_FALLBACK.light
  return CHART_PALETTE_VARS.map((token, index) =>
    readPaletteEntry(token, fallback[index] ?? fallback[0])
  )
}

/** Repeats the theme palette to cover an ordinal scale of `length` series. */
export function chartPaletteRange(
  length: number,
  palette: readonly string[] = readChartPalette()
): string[] {
  if (length <= 0 || palette.length === 0) return []
  return Array.from(
    { length },
    (_, index) => palette[index % palette.length] as string
  )
}

/* Opacity lost per lap round the palette, and the floor it stops at. */
const REPEAT_ALPHA_STEP = 0.2
const MIN_REPEAT_ALPHA = 0.4

function withAlpha(colour: string, alpha: number): string {
  const trimmed = colour.trim()
  const hexMatch = /^#([0-9a-f]{6})$/i.exec(trimmed)
  if (hexMatch?.[1]) {
    const value = Number.parseInt(hexMatch[1], 16)
    const r = (value >> 16) & 0xff
    const g = (value >> 8) & 0xff
    const b = value & 0xff
    return `rgba(${r}, ${g}, ${b}, ${Number(alpha.toFixed(2))})`
  }
  const rgbMatch =
    /^rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)(?:\s*,\s*([\d.]+))?\s*\)$/i.exec(
      trimmed
    )
  if (rgbMatch) {
    const [, r, g, b, existingAlpha] = rgbMatch
    const baseAlpha = existingAlpha !== undefined ? Number(existingAlpha) : 1
    const nextAlpha = Number((baseAlpha * alpha).toFixed(2))
    return `rgba(${r}, ${g}, ${b}, ${nextAlpha})`
  }
  return colour
}

/**
 * `chartPaletteRange` for leaderboards where the series count exceeds five.
 * Each lap round the palette lowers opacity one step so series 6-10 stay
 * visually distinct from series 1-5 across both hex and resolved `rgba()`
 * theme colours.
 */
export function chartPaletteRangeFaded(
  length: number,
  palette: readonly string[] = readChartPalette()
): string[] {
  if (length <= 0 || palette.length === 0) return []
  const size = palette.length
  return Array.from({ length }, (_, index) => {
    const colour = palette[index % size] as string
    const lap = Math.floor(index / size)
    if (lap === 0) return colour
    const alpha = Math.max(MIN_REPEAT_ALPHA, 1 - lap * REPEAT_ALPHA_STEP)
    return withAlpha(colour, alpha)
  })
}

/**
 * The palette, re-read when the light/dark class or the theme preset changes.
 * Chart components remount on theme change, so this keeps the series colours
 * in sync with the CSS variables they came from.
 */
export function useChartPalette(): string[] {
  const { resolvedTheme } = useTheme()
  const [palette, setPalette] = useState<string[]>(readChartPalette)

  useEffect(() => {
    const update = () => setPalette(readChartPalette())
    update()

    const observer = new MutationObserver(update)
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['class'],
    })
    observer.observe(document.body, {
      attributes: true,
      attributeFilter: ['data-theme-preset'],
    })
    return () => observer.disconnect()
  }, [resolvedTheme])

  return palette
}
