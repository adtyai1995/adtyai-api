---
name: ADTY AI
description: One API host for Claude, GPT, Gemini and the rest, in white and ADTY logo blue.
colors:
  adty-blue: "oklch(0.553 0.221 260.8)"
  adty-blue-ink: "oklch(0.465 0.188 260.9)"
  blue-wash: "oklch(0.959 0.019 260.2)"
  paper: "oklch(1 0 0)"
  ink: "oklch(0.183 0.031 263.4)"
  slate-text: "oklch(0.502 0.029 263.2)"
  tile-gray: "oklch(0.972 0.005 258.3)"
  hairline: "oklch(0.925 0.012 258)"
  field-line: "oklch(0.9 0.014 258)"
  quiet-neutral: "oklch(0.66 0.022 260)"
  console-frame: "oklch(0.972 0.006 258)"
  map-dot: "#c4ccd8"
  signal-red: "oklch(0.577 0.245 27.325)"
  signal-green: "oklch(0.627 0.194 149.214)"
  signal-amber: "oklch(0.666 0.179 58.318)"
  night: "oklch(0.16 0.012 262)"
  night-raised: "oklch(0.205 0.014 262)"
  night-text: "oklch(0.97 0.004 258)"
  night-blue: "oklch(0.58 0.205 260.5)"
  terminal-well: "oklch(0.13 0.01 262)"
typography:
  display:
    fontFamily: "'Public Sans Variable', ui-sans-serif, system-ui, 'PingFang SC', 'Microsoft YaHei', sans-serif"
    fontSize: "clamp(2.1rem, 5.6vw, 5.2rem)"
    fontWeight: 800
    lineHeight: 1.08
    letterSpacing: "-0.035em"
  headline:
    fontFamily: "'Public Sans Variable', ui-sans-serif, system-ui, 'PingFang SC', 'Microsoft YaHei', sans-serif"
    fontSize: "clamp(2.1rem, 4.4vw, 3.6rem)"
    fontWeight: 800
    lineHeight: 1.08
    letterSpacing: "-0.03em"
  statement:
    fontFamily: "'Public Sans Variable', ui-sans-serif, system-ui, 'PingFang SC', 'Microsoft YaHei', sans-serif"
    fontSize: "clamp(1.6rem, 2.8vw, 2.4rem)"
    fontWeight: 800
    letterSpacing: "-0.025em"
  title:
    fontFamily: "'Public Sans Variable', ui-sans-serif, system-ui, 'PingFang SC', 'Microsoft YaHei', sans-serif"
    fontSize: "1.6rem"
    fontWeight: 800
    lineHeight: 1.25
    letterSpacing: "-0.02em"
  lead:
    fontFamily: "ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, 'PingFang SC', 'Microsoft YaHei', sans-serif"
    fontSize: "17px"
    fontWeight: 400
    lineHeight: 1.75
  body:
    fontFamily: "ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, 'PingFang SC', 'Microsoft YaHei', sans-serif"
    fontSize: "15px"
    fontWeight: 400
    lineHeight: 1.7
  label:
    fontFamily: "ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, 'PingFang SC', 'Microsoft YaHei', sans-serif"
    fontSize: "14px"
    fontWeight: 500
    lineHeight: 1.43
  console:
    fontFamily: "ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, 'PingFang SC', 'Microsoft YaHei', sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.43
  mono:
    fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace"
    fontSize: "13px"
    fontWeight: 400
    lineHeight: 2
rounded:
  sm: "7.2px"
  md: "9.6px"
  lg: "12px"
  xl: "16.8px"
  2xl: "21.6px"
  tile: "24px"
  feature: "22px"
  stage: "28px"
  pill: "9999px"
spacing:
  gutter-sm: "20px"
  gutter-md: "32px"
  gutter-lg: "40px"
  tile-pad: "28px"
  tile-pad-lg: "32px"
  bento-gap: "20px"
  section: "96px"
  section-lg: "128px"
  container: "1320px"
components:
  button-home-primary:
    backgroundColor: "{colors.adty-blue}"
    textColor: "{colors.paper}"
    rounded: "{rounded.pill}"
    padding: "0 28px"
    height: "48px"
  button-home-outline:
    backgroundColor: "transparent"
    textColor: "{colors.ink}"
    rounded: "{rounded.pill}"
    padding: "0 28px"
    height: "48px"
  button-console:
    backgroundColor: "{colors.adty-blue}"
    textColor: "{colors.paper}"
    rounded: "{rounded.lg}"
    padding: "0 10px"
    height: "32px"
  input-console:
    backgroundColor: "transparent"
    textColor: "{colors.ink}"
    rounded: "{rounded.lg}"
    padding: "4px 10px"
    height: "32px"
  card-console:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    rounded: "{rounded.xl}"
    padding: "16px"
  tile-gray:
    backgroundColor: "{colors.tile-gray}"
    textColor: "{colors.ink}"
    rounded: "{rounded.tile}"
    padding: "{spacing.tile-pad}"
  tile-blue:
    backgroundColor: "{colors.adty-blue}"
    textColor: "{colors.paper}"
    rounded: "{rounded.tile}"
    padding: "{spacing.tile-pad}"
  chip-protocol:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    rounded: "{rounded.pill}"
    padding: "6px 16px 6px 10px"
  nav-pill-scrolled:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    rounded: "{rounded.pill}"
    height: "56px"
  sidebar-item-active:
    backgroundColor: "{colors.blue-wash}"
    textColor: "{colors.adty-blue-ink}"
    rounded: "{rounded.md}"
    padding: "8px"
  terminal-card:
    backgroundColor: "{colors.night}"
    textColor: "{colors.night-text}"
    rounded: "{rounded.stage}"
    padding: "64px 48px"
---

# Design System: ADTY AI

## Overview

**Creative North Star: "The Route Map on White"**

ADTY AI is a white page with one blue line drawn across it. The ground is pure white, the text is a blue-tinted ink, and the logo blue is the only colour that speaks: it marks the word that matters in a headline, the primary action, the routes on the dotted world map and one solid tile. Everything else is gray tile, hairline and type. The public home follows openlux.ai's section-by-section structure (asymmetric map hero, vendor wall, numbered cards, an oversized billing statement, a bento of controls, FAQ, one dark terminal card), rebuilt in ADTY blue; the console keeps its existing component system and takes this world through tokens, not through a second UI.

Density runs in two registers. Public pages are spacious: 1320px container, 96 to 128px between sections, display type at up to 83px, large 24px-radius gray tiles. The console is compact: 14px system text, 32px controls, 12px radius, hairline rings. Both share the same palette, the same blue, the same ink, so a visitor moving from the home page into the console never feels a brand change.

The build carries a factual tone into its visuals: illustrations are product mechanisms (a diff of the base URL, a hold/settle/return ledger, a 24-slot probe strip), labeled as examples, never invented metrics. User-selectable theme presets (`theme-presets.css`) can still override the palette per user; a subsite may only swap name, logo and favicon, never colours or taglines.

**Key Characteristics:**
- White-dominant: white page, white cards, a white console work surface on a barely tinted frame; pale gray tiles carry grouping.
- One accent: ADTY logo blue for emphasis, primary actions, map arcs and one solid tile per bento.
- Display voice in self-hosted Public Sans at weight 800 with tight tracking; Chinese glyphs fall through to PingFang / YaHei.
- Mono is a data voice, not a decoration: URLs, auth headers, code and amounts only.
- One near-black surface per page: the closing terminal card.
- Motion is one-shot and calm: arcs draw once with an expo ease-out; the vendor wall drifts and pauses on hover; everything is gated by reduced motion.

## Colors

A white-and-ink palette with a single saturated blue; every neutral is tinted faintly toward that blue (hue 258 to 263) so gray never reads as dead gray.

### Primary
- **ADTY Logo Blue** (adty-blue, ≈ #1366f0): the logo's blue and the one accent. Primary buttons, the highlighted word in a headline ("API"), active public nav link, map arcs and endpoints, the second billing statement, the solid support tile, ring/focus colour, chart series 1. Also aliased as `brand`, `info`, `ring` and `sidebar-primary`.
- **Deep Logo Blue** (adty-blue-ink, ≈ #0b4fc0): blue text on blue wash, used for the active sidebar item where full blue on wash would be too light for small text.
- **Blue Wash** (blue-wash, ≈ #eaf2ff): the `accent` surface: active sidebar row, hovered menu rows, brand-soft backgrounds.

### Neutral
- **Paper** (paper, #ffffff): page, card, popover and sidebar ground in light mode.
- **Route Ink** (ink, ≈ #0b1220): all primary text and headings; the base for `foreground/NN` steps used for quieter text (85, 75, 70, 60 percent).
- **Slate Text** (slate-text, ≈ #5b6475): secondary text, descriptions, captions (about 5.9:1 on white).
- **Tile Gray** (tile-gray, ≈ #f4f6f9): the `muted` / `secondary` surface; every gray tile, feature panel, FAQ item and affiliate band.
- **Hairline** (hairline, ≈ #e1e7ee): borders, dividers, table row lines.
- **Field Line** (field-line, ≈ #d8dee7): input borders.
- **Quiet Neutral** (quiet-neutral, ≈ #8a93a0): neutral status badge fill.
- **Console Frame** (console-frame, ≈ #f5f7fa): the console header and sidebar ground, one step off white so the white inset page reads as the work surface. Never used inside a page.
- **Map Dot** (map-dot, #c4ccd8): the dots of `adty-world-dots.svg`; never used as text.

### Signal
- **Signal Red** (signal-red), **Signal Green** (signal-green), **Signal Amber** (signal-amber): destructive, success and warning, each with a `-soft` wash at 12 to 14 percent on white. Green means a measured success; it never stands in for "no data".

### Night (dark theme and the terminal card)
- **Night** (night, ≈ #0a0d13): dark-theme page ground and the terminal card on the light home page (scoped with the `dark` class).
- **Night Raised** (night-raised, ≈ #14171e): dark-theme cards and popovers.
- **Night Text** (night-text, ≈ #f3f5f8): dark-theme foreground.
- **Night Blue** (night-blue, ≈ #2772f1): the logo blue lifted for dark ground.
- **Terminal Well** (terminal-well, ≈ #06070b): the terminal window inside the terminal card, one step darker than Night.

### Named Rules
**The One Blue Rule.** ADTY logo blue is the only accent. It marks one word in a headline, the primary action, and at most one solid tile per bento. Chart and overview accents (teal, green, violet, rose) live only inside charts and data badges.

**The Locked Palette Rule.** A subsite swaps its name, logo and favicon only. It never changes colours or taglines; the palette above is the platform's, not the site's. Per-user theme presets are the only sanctioned palette override.

**The Honest Signal Rule.** Status visuals show only measured values. An hour, group or probe with no sample shows a dash or a dashed empty slot in neutral, never a green bar and never 100%.

## Typography

**Display Font:** Public Sans Variable (self-hosted via `@fontsource-variable/public-sans`), falling through to the system UI stack for CJK (PingFang SC, Microsoft YaHei).
**Body Font:** the system UI stack (`ui-sans-serif, system-ui, -apple-system, Segoe UI, Roboto, Helvetica Neue, Arial, PingFang SC, Microsoft YaHei`).
**Mono Font:** the system mono stack (`ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas`).

**Character:** a heavy, tightly tracked geometric grotesque for statements over a native system body, so headlines feel built and Chinese body text renders in the reader's own OS face.

### Hierarchy
- **Display** (800, clamp(2.1rem, 5.6vw, 5.2rem), 1.08, −0.035em): the hero headline only, three set lines, one word in blue.
- **Headline** (800, clamp(2.1rem, 4.4vw, 3.6rem), 1.08, −0.03em, balanced): section titles on public pages.
- **Statement** (800, clamp(1.6rem, 2.8vw, 2.4rem), −0.025em): ruled one-line claims, as in the billing pair.
- **Title** (800, 1.6rem, 1.25, −0.02em): tile and card headings on public pages.
- **Lead** (400, 17px, 1.75, max 34 to 38rem): the paragraph under a display or headline.
- **Body** (400, 15px, 1.7): tile copy, FAQ answers (15px / 1.8), footer links.
- **Label** (500, 14px): chips, pills, nav (public nav at 15px / 500).
- **Console** (400, 14px): the console's default text size from the shared primitives.
- **Mono** (400, 12.5 to 13px, line-height 2): endpoint paths, base URLs, auth headers, code snippets, example amounts.

### Named Rules
**The Data Voice Rule.** Mono is reserved for URLs, auth headers, code (including model identifiers) and inline amounts in tables and ledgers. Amounts and counts also use tabular numerals. Prose, labels and headings are never mono.

**The Figure Rule.** A headline figure (a KPI value of 18px and up: balance, month spend, request count) is set in Public Sans 800 with tabular numerals and -0.02em tracking, not in mono; mono at display size reads as a terminal, not a number.

**The Keep-All Rule.** Chinese headline lines use `word-break: keep-all` (display, headline and the terminal-card heading), with explicit line spans where the line break is part of the composition, so a CJK word never splits across lines.

**The Sentence Case Rule.** English UI copy is sentence case. No all-caps labels, no letter-spaced uppercase as decoration. "ADTY AI" is never translated.

**The No Kicker Rule.** No eyebrow or kicker label above a heading. A heading stands on its own; context goes in the lead below it.

## Layout

Public pages sit in a 1320px container with 20px / 32px / 40px side gutters (mobile / sm / md). Sections breathe at 96px vertical padding, 128px from md up. Compositions are asymmetric two-column splits on lg (5:7, 4:8 or 1fr:auto), never a centred headline over three equal cards; the FAQ is the one centred title, followed by two independent accordion columns.

The hero fills the viewport (`min-height: 100svh`) with content anchored to the bottom: dotted world map across the upper area, headline bottom-left, protocol pills and actions bottom-right. On mobile the map widens to 170% and shifts left so the Atlantic-to-Asia arcs stay in frame.

The bento is a 12-column grid on lg (6 on md) with a 20px gap; tiles span 3 or 6 columns and two span two rows. Tile padding is 28px, 32px from md.

The console keeps its existing layout system: 48px app header, sidebar, compact 32px controls. Header and sidebar sit on the Console Frame tint; the page itself is a white inset surface (16px radius, hairline ring at 70%, a soft two-layer shadow) floating 8px off the frame, so the white work area is always the brightest thing on screen. Section pages open with a 24px Public Sans 800 title and a 13.5px slate description.

Breakpoints are Tailwind defaults: 640px, 768px, 1024px, 1280px.

## Elevation & Depth

Flat by default; depth comes from tonal layering (white page, gray tile, white specimen inside a tile) and hairlines. Shadows are soft, negative-spread and tinted with ink or blue, used for four roles only. No hard offset shadows.

### Shadow Vocabulary
- **Blue glow** (`box-shadow: 0 10px 24px -10px` in adty-blue at 70%): under the home page's primary pill button only.
- **Specimen lift** (`box-shadow: 0 6px 16px -12px` in ink at 30%): white product-UI specimens resting on a gray feature tile.
- **Floating nav** (`box-shadow: 0 12px 32px -16px rgba(11,18,32,0.22)`, with a 1px hairline ring and backdrop blur): the public nav once it becomes a pill on scroll.
- **Terminal drop** (`box-shadow: 0 30px 60px -30px rgba(0,0,0,0.6)`): the terminal window inside the terminal card.

### Named Rules
**The One Night Rule.** A page has at most one near-black surface: on the home page, the closing terminal card (with its terminal window inside it). Everything else is white or tile gray.

**The No Nested Cards Rule.** A card never contains another card. A tile may hold one product-UI specimen (a code diff, a ledger, a probe strip, a terminal) that has no heading of its own; it illustrates, it does not group.

## Shapes

Corners scale from one 12px base (`--radius: 0.75rem`): 7.2px, 9.6px, 12px, 16.8px, 21.6px for the shared primitives. Public pages use larger, softer containers: 22px feature panels, 24px bento tiles, 28px stage blocks (terminal card, affiliate band). Every action, chip and nav container on public pages is a full pill; the console keeps 12px buttons and inputs. Borders are 1px hairlines; status pills and chips are pills with a hairline. The map, the arcs and the endpoint dots (3.6px core, 9px blue halo at 12%) are the only illustrative geometry.

## Components

### Buttons
- **Shape:** pill on public pages (9999px), gently rounded in the console (12px).
- **Home primary:** ADTY blue fill, white 15px / 600 text, 48px tall, 28px horizontal padding, blue glow underneath.
- **Home outline:** transparent with a 1px ink border at 70%, ink text, same size; on the terminal card the border is white at 25% and hover fills white at 10%.
- **Console:** shared `Button`: 32px default (36px lg), 14px / 500, blue fill, hover to 80% blue on links; outline is white with a hairline, hover to tile gray. Focus is a 3px ring in blue at 50%. Pressed nudges down 1px. A disabled primary drops its blue entirely (tile gray fill, hairline border, slate text) instead of fading to a pale blue slab. Buttons keep their natural width on desktop; a form's primary action sits at the end of the form, left-aligned.

### Chips
- **Protocol pill:** white at 85% with backdrop blur, hairline border, 14px label, leading 12px blue ring (2.5px stroke).
- **Limit chip:** white pill on a gray tile, hairline border, 14px.
- **Dark chip:** hairline in white at 15%, white text at 80%, on the terminal card only.
- **Badge (console):** pill (`rounded-4xl`), 20px tall, 12px / 500.

### Cards / Containers
- **Console card:** white, 16.8px radius, 1px ring in ink at 10%, 16px vertical padding, no shadow; its title is 15px / 600 with tight tracking.
- **Console section (unframed):** the default grouping inside a console page. A 15px / 600 heading, an optional 12.5px slate line, filters or actions on the right, and the content (usually a table) directly below. Sections stack 32px apart. Implemented as `ContentSection`.
- **Settings frame:** a form or settings group sits in one hairline-bordered 14px frame with a header row (15px / 600 title over a 12.5px slate description, hairline below) and a 20 to 24px body. Implemented as `SettingsSection`; tables never go inside it.
- **Stat grid (console):** one 14px-radius shell whose cells are joined by 1px hairline gaps; each cell is a 12px slate label over a Public Sans 800 figure (22px for summaries, 28px for the overview KPIs). Columns follow the item count (3 or 4 on desktop, 2 on mobile) and any empty trailing cell stays white. A stat grid is never wrapped in another card, and a data table sits directly on the page, not inside a card.
- **Task chips (console):** an outline button per pending task with its count in a pill, blue when work is due and tile gray at zero; section shortcuts beside them are ghost buttons with a small up-right arrow.
- **Bento tile:** tile gray, 24px radius, 28 to 32px padding, Title heading followed by a 40px hairline rule; one tile per bento may be solid blue with white text.
- **Feature panel:** tile gray, 22px radius, at least 220px tall, centring one specimen.
- **FAQ item:** tile gray, 21.6px radius, 16px question at 500 weight, blue chevron.

### Inputs / Fields
- **Style:** transparent ground, 1px field-line border, 12px radius, 32px tall, 14px text on md and up.
- **Width follows the data:** amounts, ratios, counts and IDs take 8 to 12rem; names and emails up to 24rem; URLs, hosts and addresses up to 36rem; groups of short fields share a two-column grid. Only textareas and long rich text run the full width of a frame.
- **Focus:** border turns blue with a 3px inset ring in blue at 50%.
- **Error / Disabled:** red border and 20% red ring; disabled drops to 50% opacity with a field-line wash.

### Navigation
- **Public header:** transparent 64px bar at rest; after scroll it becomes a floating white pill (56px tall, white at 85%, backdrop blur, hairline ring, floating-nav shadow), easing over 700ms on `cubic-bezier(0.16, 1, 0.3, 1)`. Links are 15px / 500 ink at 70%, hover to full ink, the active link blue at 600. Mobile opens a full-screen white sheet.
- **Console sidebar:** Console Frame ground, no border (the inset page edge does the dividing); group labels 12px / 500 slate, no tracking; items 14px with 9.6px radius, hover to tile gray, active on blue wash with deep logo blue text.
- **Tabs (line variant):** transparent list, active tab marked by a 2px underline.
- **Footer:** white with a hairline top border, logo, a short line of copy, legal links only (15px ink at 85%, hover blue), copyright at 12px.

### Terminal Card (signature)
The page's one night surface: Night ground scoped with the `dark` class, 28px radius, 64px / 48px padding on md, a faint dot grid (22px, 7% foreground) fading from the top-right corner. Left: Headline in white with the second line in lifted blue, lead in white at 65%, dark chips, actions. Right: a terminal window on Terminal Well with a hairline in white at 10%, three muted window dots, line tabs, and rows of base URL (in pale cyan) plus auth headers in mono, each with a pill copy button.

### World Map Hero (signature)
`/adty-world-dots.svg` (1440 × 536, generated from Natural Earth 110m land) in map-dot gray, at 40% opacity in dark mode. Eight quadratic arcs in blue at 70% (1.6px, round caps) draw once on load over 1.8s, staggered 110ms, `cubic-bezier(0.16, 1, 0.3, 1)`; endpoints are a solid blue dot in a 12% blue halo. A white gradient rises from the bottom 52% so the headline reads over the map. Under reduced motion the arcs are simply drawn.

### Vendor Wall
Three rows of vendor marks (28px icon, 15px / 500 name) that drift in alternating directions (46s and 52s linear loops), fade at both edges with a mask, and pause on hover. A screen-reader list carries the same names; the drifting rows are `aria-hidden`.

## Do's and Don'ts

### Do:
- **Do** keep pages white-dominant: white ground, tile gray (≈ #f4f6f9) for grouping, hairlines (≈ #e1e7ee) for division.
- **Do** spend ADTY logo blue (≈ #1366f0) on one headline word, the primary action, the routes and at most one solid tile per bento.
- **Do** set display, headline, statement and title type in Public Sans at 800 with negative tracking, and every Chinese headline line with `word-break: keep-all`.
- **Do** reserve mono for URLs, auth headers, code and amounts, with tabular numerals for amounts.
- **Do** illustrate with product mechanisms (base-URL diff, hold/settle/return ledger, probe strip) labeled as examples.
- **Do** show a dash or a dashed neutral slot where a probe has no sample.
- **Do** let the console inherit this world through tokens first and layouts second, reusing the existing primitives.
- **Do** gate every loop and draw animation behind reduced motion, and start from the finished state when motion is off.

### Don't:
- **Don't** put an eyebrow or kicker label above a heading.
- **Don't** use mono for prose, labels or headings.
- **Don't** add a second near-black surface to a page; the terminal card is the one.
- **Don't** nest a card inside a card.
- **Don't** show green, a full bar or 100% for a status that has no sample.
- **Don't** set English in all caps or title case; use sentence case.
- **Don't** let a subsite change colours or taglines; it swaps name, logo and favicon only.
- **Don't** build a second UI kit for the console; restyle through tokens and the existing components.
- **Don't** use hard offset shadows; shadows here are soft and negative-spread.
