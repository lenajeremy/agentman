// What shows over the whole main window (ContentView.swift's overlays):
// Agentman's toasts, the note suggestion, the command palette, the away
// summary, and the image lightbox. Each area adds its own here, one line
// each.

import { AwaySummary } from "./away/AwaySummary";
import { Lightbox } from "./LightboxView";
import { CommandPalette } from "./palette/CommandPalette";

export function Overlays() {
  return (
    <>
      <CommandPalette />
      <AwaySummary />
      <Lightbox />
    </>
  );
}
