# Google Play listing assets

Copy the text from `short-description.txt` and `full-description.txt` into **Grow users → Store presence → Main store listing**. Upload the PNG files in `assets/` under Graphics. The YouTube video is optional and omitted here.

The default phone images now use the real iPhone frame from `sources/devicehub-pairing.png`. `render.py` accepts any portrait screenshot and places it inside that frame:

```sh
python3 play-listing/render.py phone screenshot.png output.png --headline "Review what changed"
```

For a screenshot that excludes the system status bar, add `--content-only`; the renderer retains the real frame's Dynamic Island and status icons, then places the content below them with space for the home indicator. It samples the screenshot's page background for the status and safe areas, so there are no white bands around a grey app screen. The included phone images use the three demo views already prepared for the landing page. Run `python3 play-listing/render.py build` to regenerate the complete asset pack. The script requires Pillow, which is also used by `mobile/scripts/make-icon.py`.

The included views are marketing mock screens, not captures from the Android build. Google asks screenshots to reflect the actual in-app experience. Before publishing, replace the demo images with current app screenshots using the same renderer. The first three drafts show the agents list, a live session, and a permission question.

For a Device Hub capture that includes the real phone bezel and actual app content, use the `devicehub` command. It removes the **entire exterior canvas** around the phone, including the thin strips along the sides and bottom, while leaving the phone and app pixels untouched:

```bash
python3 play-listing/render.py devicehub capture.png listing.png --headline "Connect to your Mac"
```

Use `python3 play-listing/render.py cutout capture.png phone.png` when you want a transparent phone PNG without the listing background. `assets/04-pairing-devicehub.png` and `assets/devicehub-pairing-cutout.png` are examples made from `sources/devicehub-pairing.png`. Device Hub's own Screenshot button captures only display pixels; take a macOS area screenshot around the whole phone to include its bezel. These examples show an iPhone, so use an Android emulator or device capture for Android-specific store screenshots.
