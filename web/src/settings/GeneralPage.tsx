// General (Views/SettingsView.swift GeneralPage): the keys Agentman runs
// on, the look, the editor, the models it uses, briefs, and which app and
// am serve this is.

import { useState } from "react";
import { keptWhere } from "../host";
import { useServerSystem } from "../shell/system";
import pkg from "../../package.json";
import type { ManagerSettings } from "@shared/protocol";
import { usePref } from "../prefs";
import { appearance, setAppearance, type AppearanceChoice } from "./appearance";
import { PopUp, PushButton, Segmented, TextField, Toggle } from "./controls";
import { Row, Section, SettingsPage } from "./layout";
import { copyText } from "./native";
import { providerOf, setKey, useSettingsState } from "./state";

export const MODELS: { id: string; name: string }[] = [
  { id: "claude-opus-5-5", name: "Claude Opus 5.5" },
  { id: "claude-sonnet-5-5", name: "Claude Sonnet 5.5" },
  { id: "claude-haiku-5-5", name: "Claude Haiku 5.5" },
  { id: "claude-fable-5-1", name: "Claude Fable 5.1" },
  { id: "gemini-4-argon", name: "Gemini 4 Argon" },
  { id: "gemini-3.8-flash", name: "Gemini 3.8 Flash" },
  { id: "gemini-3.1-pro-preview", name: "Gemini 3.1 Pro (preview)" },
  { id: "gemini-3.5-flash-lite", name: "Gemini 3.5 Flash-Lite" },
];
const EFFORTS = ["low", "medium", "high", "xhigh", "max"];

/** This app's version, where the Mac shows CFBundleShortVersionString;
 *  the build is the same number, as build/config.yml writes both. */
const APP_VERSION = pkg.version;
const APP_BUILD = pkg.version;

interface Props {
  draft?: ManagerSettings;
  setDraft: (next: ManagerSettings) => void;
}

export function GeneralPage({ draft, setDraft }: Props) {
  const { settings } = useSettingsState();
  const anthropic = settings?.anthropicKeySet ?? settings?.keySet === true;
  const gemini = settings?.geminiKeySet === true;
  // A fresh page asks am serve first; until it answers, a key is neither
  // saved nor missing.
  const loaded = !!settings;

  // A model on an engine without its key cannot run; say so under the
  // pickers rather than when the first reply fails.
  const missing = (d: ManagerSettings) => {
    const keys = { anthropic, gemini };
    const engines = [...new Set([d.chatModel, d.briefModel].map(providerOf))].filter((e) => !keys[e]).sort();
    if (!engines.length) return undefined;
    return `Add your ${engines[0] === "gemini" ? "Gemini" : "Anthropic"} API key above to use these models.`;
  };

  return (
    <SettingsPage title="General">
      <KeySection provider="anthropic" saved={loaded ? anthropic : undefined} />
      <KeySection provider="gemini" saved={loaded ? gemini : undefined} />
      <AppearanceSection />
      <EditorSection />
      {draft && (
        <>
          <Section title="Models" footer={missing(draft)}>
            <Row title="Conversation" detail="Answers you and acts on your agents.">
              <ModelPicker value={draft.chatModel} onChange={(chatModel) => setDraft({ ...draft, chatModel })} />
            </Row>
            <Row title="Briefs" detail="Summarises each agent after its turns.">
              <ModelPicker value={draft.briefModel} onChange={(briefModel) => setDraft({ ...draft, briefModel })} />
            </Row>
            <Row title="Effort" detail="How hard the conversation model thinks." divider={false}>
              <PopUp
                value={draft.effort}
                options={EFFORTS.map((e) => ({ value: e, label: e === "xhigh" ? "Extra high" : e[0].toUpperCase() + e.slice(1) }))}
                onChange={(effort) => setDraft({ ...draft, effort })}
              />
            </Row>
          </Section>
          <Section title="Briefs">
            <Row title="Brief me on each agent" detail="A short summary after an agent's turns, so you don't have to read them all." divider={false}>
              <Toggle on={draft.briefs} onChange={(briefs) => setDraft({ ...draft, briefs })} />
            </Row>
          </Section>
        </>
      )}
      <AboutSection />
    </SettingsPage>
  );
}

function ModelPicker({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  return (
    <PopUp
      value={value}
      sections={[
        { title: "Claude", options: MODELS.filter((m) => !m.id.startsWith("gemini-")).map((m) => ({ value: m.id, label: m.name })) },
        { title: "Gemini", options: MODELS.filter((m) => m.id.startsWith("gemini-")).map((m) => ({ value: m.id, label: m.name })) },
      ]}
      onChange={onChange}
    />
  );
}

/** One engine's API key: whether it is saved, and a field to save one. */
const capitalized = (text: string) => text.charAt(0).toUpperCase() + text.slice(1);

/** `saved`: undefined until am serve has said. */
function KeySection({ provider, saved }: { provider: "anthropic" | "gemini"; saved: boolean | undefined }) {
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

/** Light, dark, or as the Mac is set. */
function AppearanceSection() {
  const [choice, setChoice] = usePref<string>("appearance", appearance());
  return (
    <Section title="Appearance">
      <Row title="Look" divider={false}>
        <Segmented<AppearanceChoice>
          value={(choice === "light" || choice === "dark" ? choice : "system") as AppearanceChoice}
          options={[
            { value: "system", label: "Match the Mac" },
            { value: "light", label: "Light" },
            { value: "dark", label: "Dark" },
          ]}
          onChange={(next) => {
            setAppearance(next);
            setChoice(next);
          }}
        />
      </Row>
    </Section>
  );
}

/** How the code editor takes keys. */
function EditorSection() {
  const [vim, setVim] = usePref("editorVim", false);
  return (
    <Section title="Editor">
      <Row title="Vim mode" detail="Vim's keys in the code editor and its file tree. Press ? in the tree to see its keys." divider={false}>
        <Toggle on={vim} onChange={setVim} />
      </Row>
    </Section>
  );
}

/** Which app and which am serve, and whether a newer one is out. */
function AboutSection() {
  const { daemonInfo: info } = useSettingsState();
  const [copied, setCopied] = useState(false);
  const behind = (info?.behind ?? 0) > 0;
  const detail = !info ? undefined : info.latest && behind ? `${info.latest} is out. Run ${info.upgrade ?? "the upgrade"} in a terminal.` : info.latest ? "Up to date." : undefined;
  return (
    <Section title="About">
      <Row title="Agentman for Mac">
        <span className="row-value">
          {APP_VERSION} ({APP_BUILD})
        </span>
      </Row>
      <Row title="am serve" detail={detail} divider={false}>
        <div className="hstack" style={{ gap: 8 }}>
          <span className="row-value">{info?.version ?? "Not connected"}</span>
          {info?.upgrade && behind && (
            <PushButton
              onClick={() => {
                void copyText(info.upgrade!);
                setCopied(true);
              }}
            >
              {copied ? "Copied" : "Copy upgrade command"}
            </PushButton>
          )}
        </div>
      </Row>
    </Section>
  );
}
