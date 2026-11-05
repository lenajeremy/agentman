// The website's stickers, in Agentman's colours: a terminal drawn for Agentman, and some of the kit's set (kit-icons.ts)
// recoloured. Played live by web.ts on every <canvas data-sticker="name">.
import { VIEWS, ellipse, line, poly, rect, spark } from './fun'
import { SNAP, POP, erase, hop, sparkle, sway, twinkle } from './motions'
import { AGENTMAN } from './icons'
import type { Icon, Layer, Motion, Palette, Vec } from './fun'

import { bell, chat, heart, notes, padlock, phone, toggle } from './kit-icons'

export { bell, chat, heart, notes, padlock, phone, toggle }

/**
 * The stickers' palette: Agentman orange on bodies, bells and checks, a pale orange tint, a deeper orange for hearts,
 * and the near black. brand() with a black second colour turns the gold and ok slots grey, which these icons use for
 * their main parts.
 */
export const SITE: Palette = { ...AGENTMAN, gold: '#FF6A2B', ok: '#FF6A2B', tint: '#FFE7DA', hot: '#E5480A' }

const LINE = 5
const { tilt: FACE, extrude: EXT } = VIEWS.right

const glow = (parent: string, p: Vec, r: number, m: Motion | ReadonlyArray<Motion>, extra: { ip?: number; op?: number } = {}): Layer => ({
  parent,
  a: p,
  p,
  m,
  ...extra,
  shapes: [{ d: spark(p[0], p[1], r), fill: 'white' }],
})

// the prompt on the screen: Agentman's chevron, small
const PROMPT = poly([[-106, -46], [-98, -54], [-70, -28], [-98, -2], [-106, -10], [-87, -28]], [3, 3, 2, 3, 3, 1])

/**
 * A terminal: the screen wipes, three lines type out one after another, and when the last one is done a check hops
 * onto the window's corner with a sparkle. The window sways a little the whole time.
 */
export const terminal: Icon = {
  name: 'terminal',
  frames: 180,
  still: 120,
  line: LINE,
  extrude: EXT,
  layers: [
    { id: 'term', p: [240, 268], m: { ...sway, k: 0.7 }, shapes: [] },
    {
      parent: 'term',
      tilt: FACE,
      shapes: [
        { d: rect(-150, -118, 300, 236, 28), fill: 'deep', depth: 1 },
        ...[-118, -94, -70].map((x, i) => ({ d: ellipse(x, -92, 9), fill: (['main', 'tint', 'white'] as const)[i] })),
        { d: rect(-130, -68, 260, 166, 14), fill: 'white', depth: 0.2 },
        { d: PROMPT, fill: 'main' },
        // the three lines type out in turn after the wipe, the last one short
        ...[0, 1, 2].map((i) => ({ d: line([-46, -28 + i * 40], [i === 2 ? 30 : 100, -28 + i * 40]), trim: erase(i * 3, 22 + i * 16) })),
      ],
    },
    {
      id: 'check',
      parent: 'term',
      a: [128, -112],
      p: [128, -112],
      // gone with the wipe, back when the last line is typed, then the hop's landing
      s: [[0, [100, 100], SNAP], [10, [0, 0], 0], [70, [0, 0], POP], [82, [100, 100]]],
      m: { ...hop, at: 72 },
      shapes: [
        { d: ellipse(128, -112, 46, 42), fill: 'ok', depth: 0.8 },
        { d: line([106, -112], [122, -96], [150, -128]), round: true },
      ],
    },
    glow('term', [-176, -96], 22, sparkle(84)),
    glow('term', [186, -40], 26, twinkle(88, true), { ip: 88 }),
  ],
}

/** Every sticker the site plays, by the name a canvas asks for. */
export const STICKERS: Record<string, Icon> = { terminal, phone, heart, bell, padlock, toggle, chat, notes }
