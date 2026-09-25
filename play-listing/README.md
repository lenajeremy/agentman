# Google Play listing assets

Copy the text from `short-description.txt` and `full-description.txt` into **Grow users → Store presence → Main store listing**. Upload the PNG files in `assets/` under Graphics. The YouTube video is optional and omitted here.

The phone images reuse the titanium iPhone frame from the landing page's “Your phone” panel. `render.py` accepts any portrait screenshot and places it inside that frame:

```sh
python3 play-listing/render.py phone screenshot.png output.png --headline "Review what changed"
```

For a screenshot that excludes the system status bar, add `--content-only`; the renderer supplies a status bar and home indicator. The included phone images use the three demo views already prepared for the landing page. Run `python3 play-listing/render.py build` to regenerate the complete asset pack. The script requires Pillow, which is also used by `mobile/scripts/make-icon.py`.

The included views are marketing mock screens, not captures from the Android build. Google asks screenshots to reflect the actual in-app experience. Before publishing, replace the demo images with current app screenshots using the same renderer. The first three drafts show the agents list, a live session, and a permission question.
