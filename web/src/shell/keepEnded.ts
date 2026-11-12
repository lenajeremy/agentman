// The open agent's page stays when the agent ends, however it ended: quit
// in its terminal (Ctrl-C, /exit), crashed, or ended from the phone, as when
// it is ended here (confirmEnd). It reads as ended, to be resumed; a live
// agent under the same id, once it is back, comes first
// (AgentsStore.keepEndedPage).

import { endedSession } from "../labels";
import { getRoute } from "../nav";
import { onSessionsLeft } from "../store";
import { getShell, setShell } from "./state";

onSessionsLeft((before, now) => {
  const route = getRoute();
  if (route.kind !== "agent") return;
  const id = route.id;
  const shell = getShell();
  if (now.some((s) => s.id === id) || shell.pastSessions.some((s) => s.id === id)) return;
  const was = before.find((s) => s.id === id);
  if (!was || was.state === "ended") return;
  const ended = endedSession(was);
  // It quit, so it can be resumed, unless it was ended here from its page
  // and still runs in the terminal it was opened in: then it only left the
  // list, which ending it said, and that stands.
  const stillRunning = shell.stillRunning.includes(id) ? shell.stillRunning : shell.stillRunning.filter((x) => x !== ended.id);
  setShell({ foundSessions: { ...shell.foundSessions, [id]: ended, [ended.id]: ended }, stillRunning });
});
