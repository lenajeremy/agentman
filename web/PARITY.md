# The web app against Agentman for Mac

What the Mac app does that the web app doesn't yet, checked on 2026-11-10
against today's Mac app (0.43.0): the daemon requests the Mac sends and the
web never does, the Mac's screens with no web counterpart, and the Mac's
changes since the last check (`crossplatform/PARITY.md`, 2026-10-31, the
older line-by-line list). Features come first; looks wait until these are
done. One at a time, in this order.

1. **Muse in New Agent.** The Mac offers Muse with the other agents
   (NewAgentView.swift `kinds`); the web's list stops at Antigravity.
2. **"Agentman isn't connected" banner.** Losing am serve says so across
   the window (ConnectionBanner.swift). In a browser, Restart can't reach a
   stopped am serve: it says how to start it instead.
3. **The Board** (BoardView.swift, BoardActions.swift): it replaced
   Agentman's tab on the Mac. Cards with each agent's brief and git
   buttons, offers answered within a time limit, Agentman's check of the
   work (`manager_verifications`), shipping finished agents together
   (`manager_ship`), work lined up for tonight (`manager_queue`,
   `manager_queue_add`, `manager_queue_remove`), missions
   (`manager_missions`), teams, search beside the headline, agents
   coloured by state, project or folder, stuck or colliding agents.
4. **Usage and Limits** (UsageView.swift, LimitsView.swift; `usage`,
   `limits`): what the agents spent over six months, a year or all of it,
   input, cache and output apart, fast mode and advising; what is left of
   each plan.
5. **Take Over and Open in Terminal** (`handoff_session`): an ended or
   watched thread says so and offers Take Over.
6. **Find in a conversation** (FindBar.swift, ⌘F).
7. **Settings › Devices** (DevicesSettings.swift; `devices`, `pair_code`):
   pairing a phone on the page.
8. **Thread details:** how long the agent worked ("Worked for"), code
   blocks that copy, a folder's logo in place of its initials.
9. **Editor additions:** History and Diff tabs as in the main window, an
   agent's changes in a tab of their own, terminals that follow between the
   editor and the agent's page, Usage and ⌘K in the editor.
10. **Settings switches:** Agentman's standing answers, learning from
    corrections.

Not for the web app: the setup screen, installing the am command, Sparkle
updates and the catch-up prompt (am serve serves the web app itself, so the
two always match).

Known browser limits: ⌘N, ⌘T, ⌘W and ⌘Q stay the browser's; files copied in
the editor's tree go on the clipboard as paths.
