// The mascot on the website: every <canvas data-mascot> plays it live, in
// the brand palette. Built into site/mascot.js with
//   bun build brand/mascot/web.ts --minify --outfile site/mascot.js
import { play } from './fun'
import { AGENTMAN, mascot } from './icons'

for (const canvas of document.querySelectorAll<HTMLCanvasElement>('canvas[data-mascot]')) {
  play(canvas, mascot, { palette: AGENTMAN, enter: Number(canvas.dataset.enter ?? 0.15) })
}
