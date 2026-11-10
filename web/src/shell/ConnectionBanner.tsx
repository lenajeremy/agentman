// Across the top of the page while it can't reach am serve
// (ConnectionBanner.swift): what that means, and the way back. In the
// browser the way back is a terminal: a page can't start am serve, and
// when am serve has forgotten the page's sign-in, am web signs it in again.

import { TriangleAlert } from "lucide-react";
import { daemon } from "../daemon";
import { useStatus } from "../store";
import { Icon } from "../ui";

export function ConnectionBanner() {
  const { status, detail } = useStatus();
  if (status !== "offline") return null;
  const explanation = daemon.signedOut
    ? "am serve is running but has signed this page out. Run am web in a terminal to sign in again, and this page picks up where it was."
    : "Your agents keep running, but you can't see or message them from here. Start it again with am serve in a terminal.";
  return (
    <div className="connection-banner" role="status">
      <span className="connection-banner-icon"><Icon icon={TriangleAlert} sf="exclamationmark.triangle.fill" size={13} /></span>
      <div className="connection-banner-text">
        <div className="connection-banner-title">Agentman isn't connected</div>
        <div className="connection-banner-explanation">{explanation}</div>
        {detail && <div className="connection-banner-reason" title={detail}>{detail}</div>}
      </div>
    </div>
  );
}
