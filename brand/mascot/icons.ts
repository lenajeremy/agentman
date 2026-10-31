// Agentman's mascot: the app's icon come to life. The black square is its body, the chevron one eye, which winks flat,
// the orange dot the other; it stands on two orange feet, an antenna on top, and wobbles while it keeps an eye on your agents.
import { brand, ellipse, onFace, pill, poly, rect, spark } from './fun'
import { SNAP, look, sparkle, twinkle, wobble } from './motions'
import type { Icon, Layer, Motion, Vec } from './fun'

/** The brand: Agentman orange on bodies and the eye, its near black on the square. */
export const AGENTMAN = brand('#FF6A2B', '#101010')

const LINE = 5
// the face leans back a little less than the house view, so the face reads first
const FACE: Vec = [-14, 28]
const EXT: Vec = [-4, 18]
// it rocks on its feet, placed so the body sits where it always does
const ROCK = onFace(FACE, [0, 152])

// a white sparkle riding a parent, popped by whatever motion it is given
const glow = (parent: string, p: Vec, r: number, m: Motion | ReadonlyArray<Motion>): Layer => ({
  parent,
  a: p,
  p,
  m,
  shapes: [{ d: spark(p[0], p[1], r), fill: 'white' }],
})

// the logo's chevron as a thick arm bent at its tip: the eye that winks
const CHEVRON = poly(
  [[-89.6, -65.2], [-75.2, -84.4], [0, -28], [-75.2, 28.4], [-89.6, 9.2], [-40, -28]],
  [11, 11, 7, 11, 11, 3],
)
// A wink: the eye squeezed flat on SNAP, held a beat, opened again, timed as slide() times a part's move.
const wink = (start: number, hold = 8, len = 7): Motion => ({
  s: [[start, [100, 100], SNAP], [start + len, [100, 20], 0], [start + len + hold, [100, 20], SNAP], [start + len * 2 + hold, [100, 100]]],
})
const EYE: Vec = [52, -50]
// the wink's beats: shut on frame SHUT, open again from OPEN
const WINKS = 98
const [SHUT, OPEN] = [WINKS + 6, WINKS + 6 + 14]
const winking = wink(WINKS, 14, 6)
// the chevron's middle, which it flattens about
const CODE: Vec = [-45, -28]

// an open smile, its tongue clipped inside
const MOUTH = 'M-42 50A38 28 0 0 0 34 50Z'

export const mascot: Icon = {
  name: 'mascot',
  frames: 168,
  line: LINE,
  extrude: EXT,
  still: 0,
  layers: [
    { id: 'bot', a: ROCK, p: [250 + ROCK[0], 262 + ROCK[1]], m: wobble, shapes: [] },
    // the antenna swings a beat behind the body, from where it sits on the head
    {
      parent: 'face',
      a: [0, -115],
      p: [0, -115],
      m: { ...wobble, at: 5, k: 1.4 },
      shapes: [
        { d: pill(-7, -176, 14, 66), fill: 'deep', depth: 0.5 },
        { d: ellipse(0, -186, 24), fill: 'main', depth: 0.6 },
      ],
    },
    {
      id: 'face',
      parent: 'bot',
      tilt: FACE,
      shapes: [
        { d: rect(-86, 100, 66, 52, 18), fill: 'main', depth: 0.9 },
        { d: rect(20, 100, 66, 52, 18), fill: 'main', depth: 0.9 },
        { d: rect(-118, -118, 236, 236, 48), fill: 'deep', side: 'main', depth: 1 },
      ],
    },
    // the chevron winks: squeezed about its middle, a flat line while it is shut, then sprung back
    { parent: 'face', a: CODE, p: CODE, m: winking, op: SHUT, shapes: [{ d: CHEVRON, fill: 'white', depth: 0.3 }] },
    // shut, it is a hyphen
    { parent: 'face', ip: SHUT, op: OPEN, shapes: [{ d: pill(CODE[0] - 30, CODE[1] - 8, 60, 16), fill: 'white', depth: 0.3 }] },
    { parent: 'face', a: CODE, p: CODE, m: winking, ip: OPEN, shapes: [{ d: CHEVRON, fill: 'white', depth: 0.3 }] },
    // the orange eye glances round
    { id: 'eyeball', parent: 'face', shapes: [{ d: ellipse(EYE[0], EYE[1], 34), fill: 'main', depth: 0.35 }] },
    { id: 'eye', parent: 'eyeball', shapes: [{ d: ellipse(EYE[0], EYE[1], 34), fill: 'main', line: false }] },
    { parent: 'eyeball', clip: 'eye', m: { ...look, k: 1.4 }, shapes: [{ d: ellipse(EYE[0] + 4, EYE[1] + 4, 14), fill: 'ink', line: false }] },
    { id: 'mouth', parent: 'face', shapes: [{ d: MOUTH, fill: 'white', depth: 0.2 }] },
    { parent: 'face', clip: 'mouth', shapes: [{ d: ellipse(-4, 78, 22, 14), fill: 'hot', line: false }] },
    glow('bot', [156, -150], 26, sparkle(18)),
    glow('bot', [176, 10], 16, sparkle(56)),
    // a twinkle beside the chevron as it winks
    glow('bot', [-176, -138], 18, [twinkle(SHUT), twinkle(OPEN + 6, true)]),
  ],
}
