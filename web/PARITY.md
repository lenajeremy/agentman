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
10. ✓ (f384ae0) **Settings switches:** Agentman's standing answers, learning from
    corrections; with them the rest of the Mac's Permissions page the web
    lacked: when an agent finishes, while agents work, overnight, trust by
    folder.
11. ✓ (411747e) **Agentman's key prompt** (0.41): says what Agentman is and why it
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

Then what hadn't been tested, with stand-ins that cost nothing and publish
nothing: offers on the Board (faked in the page): a1295e2 an offer's answer
names the option, 662b4e8 it reads as Agentman's; Ship end to end through a
stand-in gh (commit, push to a local remote, the PR link); Agentman asked
from the Board and the editor, on a fake Anthropic API (ANTHROPIC_BASE_URL,
a fake key in the environment, never the Keychain); Muse started, messaged,
ended and resumed on Muse's echo provider: 347fae5 am serve no longer breaks
a fresh Muse with a settings file it can't read, f13b318 an ended Muse
session offers Resume; WebKit (Safari's engine, headless): thread, find,
palette, Board, Usage, Devices and its QR, the editor, the message box, no
errors once a56e75a answers the shell runtime's script. Also fixed: 6baf37c
the address follows the page, d85b182 a narrow editor window, 07a0232 ~ only
for the home folder. The Mac's AgentsStore.end reads the closed flag as the
web did before f13b318, so an ended Muse session there says it's still
running in a terminal too.
End to end: `npm run e2e` (web/teststack/e2e) runs 60 checks over every
screen, in Chromium and WebKit, on the test stack and its stand-ins; 120
passing on 2026-11-10. Writing it found 4b6502e (the picture viewer was a
placeholder; now the Mac's), 6759993 (keys typed as a palette opened over
the code went into the file), 3382cef (Escape while the commit sheet
reads), c74e136 (the keyboard back at once, not a frame later).
Not tested: Firefox (its headless build can't start inside the sandbox
these tests run in); the desktop shell running (it builds, vets and passes
its tests, but running it shows windows).

Not for the web app: the setup screen, installing the am command, Sparkle
updates and the catch-up prompt (am serve serves the web app itself, so the
two always match).

Seen while testing, not yet looked into: attaching an agent's own
terminal can type the terminal's answer to tmux's query ("1;2c0;276;0c")
into its shell. For the
looks pass: "Show in Files" where a Mac says Finder
(the platform label), and the iPad and phone icons alike in Lucide.

Known browser limits: ⌘N, ⌘T, ⌘W and ⌘Q stay the browser's; files copied in
the editor's tree go on the clipboard as paths.
