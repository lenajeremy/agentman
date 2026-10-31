// Agentman's mascot: the app's icon come to life. The black square is its body, the chevron a wink, the orange dot its
// other eye; it stands on two orange feet, an antenna on top, and wobbles while it keeps an eye on your agents.
import { brand, ellipse, onFace, pill, poly, rect, spark } from './fun'
import { look, sparkle, wobble } from './motions'
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

// the logo's chevron as a thick arm bent at its tip: a winking eye
const WINK = poly(
  [[-89.6, -65.2], [-75.2, -84.4], [0, -28], [-75.2, 28.4], [-89.6, 9.2], [-40, -28]],
  [11, 11, 7, 11, 11, 3],
)
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
        { d: WINK, fill: 'white', depth: 0.3 },
        { d: ellipse(52, -50, 34), fill: 'main', depth: 0.35 },
      ],
    },
    { id: 'eye', parent: 'face', shapes: [{ d: ellipse(52, -50, 34), fill: 'main', line: false }] },
    { parent: 'face', clip: 'eye', m: { ...look, k: 1.4 }, shapes: [{ d: ellipse(56, -46, 14), fill: 'ink', line: false }] },
    { id: 'mouth', parent: 'face', shapes: [{ d: MOUTH, fill: 'white', depth: 0.2 }] },
    { parent: 'face', clip: 'mouth', shapes: [{ d: ellipse(-4, 78, 22, 14), fill: 'hot', line: false }] },
    glow('bot', [156, -150], 26, sparkle(18)),
    glow('bot', [-170, -60], 16, sparkle(94)),
  ],
}
