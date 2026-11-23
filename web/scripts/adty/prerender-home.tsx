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
// ADTY task 9.3: build-time prerender of the home page.
//
//   bun scripts/adty/prerender-home.tsx     (run by `bun run build` after rsbuild)
//
// Renders the landing page in the two official languages and writes
// dist/adty-home.json ({"en": "<html>", "zh-CN": "<html>"}). The Go
// server inserts the page's language before #root (router/adty_seo.go).
import { readFileSync, writeFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { JSDOM } from 'jsdom'
import { compile } from 'tailwindcss'

import { renderAllAdtyHomes } from '../../src/features/adty-home/prerender'

const dist = resolve(import.meta.dirname, '../../dist')
const pages = await renderAllAdtyHomes()
const sitePages = await renderAllAdtyHomes(true)

// Compile the actual first-screen classes, including responsive and dark
// variants, against the same tokens and preflight as the production bundle.
const classes = new Set<string>()
for (const page of [...Object.values(pages), ...Object.values(sitePages)]) {
  const fragment = JSDOM.fragment(page)
  for (const element of fragment.querySelectorAll(
    'header, header [class], #hero, #hero [class]'
  )) {
    for (const name of element.classList) classes.add(name)
  }
}
const tailwindSource = (path: string) =>
  readFileSync(new URL(import.meta.resolve(`tailwindcss/${path}`)), 'utf8')
const theme = readFileSync(
  resolve(import.meta.dirname, '../../src/styles/theme.css'),
  'utf8'
)
const compiler = await compile(`
  @layer theme, base, components, utilities;
  @layer theme { ${tailwindSource('theme.css')} }
  @layer base { ${tailwindSource('preflight.css')} }
  ${theme}
  @layer base {
    * { border-color: var(--border); }
    body { background: var(--background); color: var(--foreground); font-family: system-ui, sans-serif; }
  }
  @layer utilities { @tailwind utilities; }
  #adty-prerender ~ #root { display: none; }
  #adty-prerender button, #adty-prerender [role=tablist] { visibility: hidden; }
`)
const css = await Bun.build({
  entrypoints: ['home.css'],
  files: { 'home.css': compiler.build([...classes]) },
  minify: true,
})
if (!css.success) throw new Error(css.logs.join('\n'))

// Use byte-exact tags from Rsbuild so CDN prefixes, integrity and ordering
// survive Go's per-deployment analytics injection into the ordinary index.
const index = readFileSync(resolve(dist, 'index.html'), 'utf8')
const dom = new JSDOM(index, { includeNodeLocations: true })
const stylesheets = Array.from(
  dom.window.document.head.querySelectorAll('link[rel="stylesheet"][href]'),
  (link) => {
    const location = dom.nodeLocation(link)
    if (!location) {
      throw new Error('Entry stylesheet is missing from the built HTML')
    }
    return index.slice(location.startOffset, location.endOffset)
  }
)
if (!stylesheets.length) throw new Error('Missing Rsbuild entry stylesheet')

writeFileSync(resolve(dist, 'adty-home.json'), JSON.stringify(pages))
writeFileSync(resolve(dist, 'adty-site-home.json'), JSON.stringify(sitePages))
writeFileSync(
  resolve(dist, 'adty-home-styles.json'),
  JSON.stringify({
    css: await css.outputs[0].text(),
    stylesheets,
  })
)
console.log(
  `prerender-home: wrote ${Object.keys(pages).length} languages to dist/adty-home.json`
)
