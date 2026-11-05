// The website's live drawings: every <canvas data-mascot> plays the mascot, and every <canvas data-sticker="name">
// the sticker of that name, in the brand's colours. Built into site/mascot.js with
//   bun build brand/mascot/web.ts --minify --target browser --outfile site/mascot.js
import { play } from './fun'
import { AGENTMAN, mascot } from './icons'
import { SITE, STICKERS } from './stickers'

for (const canvas of document.querySelectorAll<HTMLCanvasElement>('canvas[data-mascot]')) {
  play(canvas, mascot, { palette: AGENTMAN, enter: Number(canvas.dataset.enter ?? 0.15) })
}
for (const canvas of document.querySelectorAll<HTMLCanvasElement>('canvas[data-sticker]')) {
  const icon = STICKERS[canvas.dataset.sticker ?? '']
  if (icon) play(canvas, icon, { palette: SITE, enter: Number(canvas.dataset.enter ?? 0.2), offset: Number(canvas.dataset.offset ?? 0) })
}
