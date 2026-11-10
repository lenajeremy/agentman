// The right panel at the window's edge (Views/RightPanel.swift,
// Views/HistoryPanel.swift): Diff, Files, History and Terminal tabs. To be
// built: PARITY.md §6.

import type { Session } from "@shared/protocol";

export function RightPanel({ session, onClose }: { session: Session; onClose: () => void }) {
  void session;
  void onClose;
  return <aside className="right-panel" />;
}
