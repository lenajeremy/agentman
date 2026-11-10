# The web app against Agentman for Mac

What the Mac app does that the web app doesn't yet, checked on 2026-11-10
against today's Mac app (0.43.0): the daemon requests the Mac sends and the
web never does, the Mac's screens with no web counterpart, and the Mac's
changes since the last check (`crossplatform/PARITY.md`, 2026-10-31, the
older line-by-line list). Features come first; looks wait until these are
done. One at a time, in this order.

1. ✓ (ff9703a, with ending any session and typed answers in the message
   box) **Muse in New Agent.** The Mac offers Muse with the other agents
   (NewAgentView.swift `kinds`); the web's list stops at Antigravity.
2. ✓ (7c7d180; b4d29ef keeps sign-ins across restarts) **"Agentman isn't
   connected" banner.** Losing am serve says so across
   the window (ConnectionBanner.swift). In a browser, Restart can't reach a
   stopped am serve: it says how to start it instead.
3. ✓ **The Board** (BoardView.swift, BoardActions.swift), in five pieces:
   the page, columns, cards, ask box, chat beside, sidebar aside (71d8e82);
   git buttons and the commit sheet (773538e; 1c49c75 fixed committing a
   renamed file in am serve); checks and teams (c79ca19); Ship and Tonight
   (68513da); search, colours, Handled, folder trust (f24e95c). The card's title opens the agent;
   4e7237c's offer time limit is am serve's, which the web shares.
4. ✓ (bb53f60) **Usage and Limits** (UsageView.swift, LimitsView.swift; `usage`,
   `limits`): what the agents spent over six months, a year or all of it,
   input, cache and output apart, fast mode and advising; what is left of
   each plan.
5. ✓ (d759003) **Take Over and Open in Terminal** (`handoff_session`): an ended or
   watched thread says so and offers Take Over.
6. ✓ (4228fa3, with the sidebar's Needs you section) **Find in a conversation** (FindBar.swift, ⌘F), and opening a search
   hit at its message (ThreadJump); the sidebar's search of what was said
   too (the Board's is done).
7. ✓ (c5c2487) **Settings › Devices** (DevicesSettings.swift; `devices`, `pair_code`):
   pairing a phone on the page.
8. ✓ (6af544e, 636796f, 927de80) **Thread details:** how long the agent worked ("Worked for"), code
   blocks that copy, a folder's logo in place of its initials.
9. ✓ (d34220d, 23ff13f, c39334a; cdba078 and b55b8f5 fixed what testing
   found) **Editor additions:** History and Diff tabs as in the main window, an
   agent's changes in a tab of their own, terminals that follow between the
   editor and the agent's page (here: the window you're in gives a shared
   shell its size), Usage and ⌘K in the editor.
10. **Settings switches:** Agentman's standing answers, learning from
    corrections.
11. **Agentman's key prompt** (0.41): says what Agentman is and why it
    needs a key, and the box's note reads "Don't have one? Get an
    Anthropic key".
12. **⌘K finds what was said** (CommandPalette.swift `said`): messages that
    match what is typed, under the commands, opening at the message. Found
    while doing 9.

Gone back over 1–9 on 2026-11-10, past the main path of each (the keyboard
after each overlay, no data, no main window, every way in, dark mode):
1. 9ce2375 Muse and Cursor CLI named as on the Mac, Muse's logo; 7064563
   ending the agent you're on keeps its page; 2ac667f the message box keeps
   the keyboard after sending.
2. 9814c33 the footer and banner say what the Mac's do, am service restart.
3. b1c73fc the Board's sheets give the keyboard back; 7c982d9 its folder
   menu lists the folders (asked before the socket was up).
6. d20cf28 closing find gives the keyboard back; 42cc961 the cursor kept;
   80ed804 Escape clears the sidebar's search.
9. 9638da5, 45c4ddc palettes and the Usage popover give the keyboard back;
   cccc576 an editor opens a main tab when none is open.
4, 5, 7 and 8: nothing found. Also c33b677: npm test runs every test file.
Not tested anywhere: the desktop shell, Safari and Firefox, starting a real
Muse, offers on the Board, a real Ship.

Not for the web app: the setup screen, installing the am command, Sparkle
updates and the catch-up prompt (am serve serves the web app itself, so the
two always match).

Seen while testing, not yet looked into: attaching an agent's own
terminal can type the terminal's answer to tmux's query ("1;2c0;276;0c")
into its shell. The address bar doesn't follow the page (an agent opened, Usage), so
reloading or going Back returns to where the page was first opened; the
Mac has no address bar, so this is the browser's alone. An editor window
under about 1000 wide pushes the agent
panel past its right edge, its header buttons out of reach (the Mac keeps
the window from being that small; a browser window can be). For the
looks pass: "Show in Files" where a Mac says Finder
(the platform label), and the iPad and phone icons alike in Lucide.

Known browser limits: ⌘N, ⌘T, ⌘W and ⌘Q stay the browser's; files copied in
the editor's tree go on the clipboard as paths.
