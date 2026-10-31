# Agentman's mascot

The app icon come to life: the black square is its body, the chevron one eye and the orange dot the other. It stands on
two orange feet with an antenna on top and wobbles while it keeps an eye on your agents; once a loop the chevron winks,
shut to a hyphen and sprung back. Made with the fun-isometric skill.

- `icons.ts`: the mascot, drawn as slabs and moving on the kit's `wobble` (body), a lagging wobble (antenna), `look`
  (its orange eye), a wink (the chevron) and sparkles. `AGENTMAN` is the brand palette, Agentman orange `#FF6A2B` and
  near black `#101010`.
- `fun.ts`, `motions.ts`, `Sticker.tsx`: the kit's runtime, copied in. No dependency, no Lottie player.
- `export/`: the loop at 1200px: `mascot.webm` (VP9 with alpha), `mascot.mp4` (HEVC with alpha, for Safari),
  `mascot-on-white.mp4` (H.264 on white, for chats and social posts, where transparent video can show black) and
  `mascot.png` (its still frame, the poster and the reduced-motion fallback).

## Using it

As a video, transparent on any background:

```html
<video autoplay loop muted playsinline poster="mascot.png" width="200">
  <source src="mascot.mp4" type='video/mp4; codecs="hvc1"'>
  <source src="mascot.webm" type="video/webm">
</video>
```

Live, in React:

```tsx
import { Sticker } from './brand/mascot/Sticker'
import { AGENTMAN, mascot } from './brand/mascot/icons'

<Sticker icon={mascot} palette={AGENTMAN} style={{ width: 160 }} />
```

Without React: `play(canvas, mascot, { palette: AGENTMAN })` from `fun.ts`.

To change it, edit `icons.ts`, then from this folder (the skill installed in `~/.claude/skills/fun-isometric`):

```
bun ~/.claude/skills/fun-isometric/bin/fi.mjs check icons.ts
bun ~/.claude/skills/fun-isometric/bin/fi.mjs sheet icons.ts mascot --brand '#FF6A2B,#101010'
bun ~/.claude/skills/fun-isometric/bin/fi.mjs export icons.ts mascot --px 1200 --brand '#FF6A2B,#101010' --out export
```

The files in `export/` were encoded at higher quality than `fi export` uses (VP9 at CRF 12 rather than 30, HEVC at
12 Mbps with full alpha quality rather than 800 kbps, H.264 at CRF 12), from a copy of `fi.mjs` with those three numbers
changed; the frames are the same.

Made with fun-isometric by Kelvin Adithya (x.com/KelvinAdithya), heavily inspired by Slush (slush.app), more at
skills.klvn.dev/fun-isometric.
