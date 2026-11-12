// Agentman's own API key for one engine (SettingsView.swift KeySection):
// whether it is saved, and a field to save one. On the Keys page, ahead of
// the keys Agentman hands to agents.

import { useState } from "react";
import { keptWhere } from "../host";
import { useServerSystem } from "../shell/system";
import { PushButton, TextField } from "./controls";
import { Row, Section } from "./layout";
import { setKey } from "./state";

const capitalized = (text: string) => text.charAt(0).toUpperCase() + text.slice(1);

/** `saved`: undefined until am serve has said. */
export function KeySection({ provider, saved }: { provider: "anthropic" | "gemini"; saved: boolean | undefined }) {
  const [key, setKeyText] = useState("");
  const [message, setMessage] = useState<string>();
  const gemini = provider === "gemini";
  const system = useServerSystem();
  const save = async (value: string) => {
    const problem = await setKey(value.trim(), provider);
    if (problem) {
      setMessage(problem);
    } else {
      setKeyText("");
      setMessage(value ? "Key saved." : "Key removed.");
    }
  };
  return (
    <Section
      title={gemini ? "Gemini API key" : "Anthropic API key"}
      footer={message ?? (system === null ? "\u00a0" : `${capitalized(keptWhere(system))} and only ever sent to ${gemini ? "Google" : "Anthropic"}.`)}
    >
      {/* Blank, its height kept, until am serve has said: not words that
          change a moment later. */}
      <Row
        title={saved === undefined || (saved && system === null) ? "\u00a0" : saved ? (system === "darwin" ? "Saved in your Keychain" : "Saved") : "Not set yet"}
        detail={saved === undefined ? "\u00a0" : saved ? "Paste a new key below to replace it." : gemini ? "For running Agentman on Gemini models." : "For running Agentman on Claude models."}
      >
        {saved && (
          <PushButton destructive onClick={() => void save("")}>
            Remove
          </PushButton>
        )}
      </Row>
      <Row title="Key" divider={false}>
        <div className="hstack" style={{ gap: 8 }}>
          <TextField
            secure
            mono
            className="key-field"
            value={key}
            placeholder={gemini ? "Gemini API key" : "sk-ant-…"}
            onChange={setKeyText}
            onSubmit={() => void save(key)}
          />
          <PushButton disabled={!key.trim()} onClick={() => void save(key)}>
            Save
          </PushButton>
        </div>
      </Row>
    </Section>
  );
}

