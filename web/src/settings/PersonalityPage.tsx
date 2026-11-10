// Personality (Views/SettingsView.swift PersonalityPage): how Agentman
// talks, and anything else it should know.

import type { ManagerSettings } from "@shared/protocol";
import { LinkButton, TextArea } from "./controls";
import { Section, SettingsPage } from "./layout";

const DEFAULT_STYLE =
  "Calm and concise. Lead with what matters, in as few words as it takes. " +
  "No preamble, no filler, no exclamation marks. Name agents by name. " +
  "When nothing needs the user, say so in one line.";

interface Props {
  draft?: ManagerSettings;
  setDraft: (next: ManagerSettings) => void;
}

export function PersonalityPage({ draft, setDraft }: Props) {
  return (
    <SettingsPage title="Personality">
      {draft && (
        <>
          <div className="style-group">
            <Section title="How Agentman talks">
              <TextArea className="personality-editor" style={{ height: 96 }} value={draft.style} onChange={(style) => setDraft({ ...draft, style })} />
            </Section>
            <LinkButton className="caption default-style" onClick={() => setDraft({ ...draft, style: DEFAULT_STYLE })}>
              Use the default style
            </LinkButton>
          </div>
          <Section title="Anything else Agentman should know" footer="Both go into Agentman's prompt on every message. Notes for your agents belong in Knowledge.">
            <TextArea
              className="personality-editor"
              style={{ height: 150 }}
              value={draft.instructions}
              onChange={(instructions) => setDraft({ ...draft, instructions })}
            />
          </Section>
        </>
      )}
    </SettingsPage>
  );
}
