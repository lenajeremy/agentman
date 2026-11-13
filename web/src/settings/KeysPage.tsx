// Keys (Views/KeysSettings.swift): Agentman's own API keys first, then the
// keys it hands to agents, their names, what they are for and when Agentman
// may give one without asking; values go to am serve's keychain and are
// never shown again. After them, Agentman's own sign-in for voice prompts
// (SettingsView.swift VoiceSection).

import { useEffect, useState } from "react";
import type { Key } from "@shared/protocol";
import { CircleMinus, Plus } from "lucide-react";
import { keptWhere } from "../host";
import { useServerSystem } from "../shell/system";
import { Icon } from "../ui";
import { KeySection } from "./KeySection";
import { LinkButton, PopUp, PushButton, TextField, lastPart } from "./controls";
import { Row, Section, useTestScroll } from "./layout";
import { pick } from "./native";
import { Sheet } from "./Sheet";
import { deleteKey, loadKeys, saveKey, settingsState, useSettingsState, voiceAccount, type VoiceReply } from "./state";

/** Who gets a key, in a few words. */
export function ruleText(key: Key): string {
  if (key.rule === "ask") return "Asks you each time";
  if (key.rule === "projects") return "Only " + (key.projects ?? []).map(lastPart).join(", ");
  return "Given to any agent";
}

interface KeyDraft {
  name: string;
  value: string;
  description: string;
  rule: string;
  projects: string[];
  original?: Key;
}

const newDraft = (): KeyDraft => ({ name: "", value: "", description: "", rule: "auto", projects: [] });
const editDraft = (key: Key): KeyDraft => ({ name: key.name, value: "", description: key.description ?? "", rule: key.rule, projects: key.projects ?? [], original: key });

const query = new URLSearchParams(location.search);

export function KeysPage() {
  const { keys, settings } = useSettingsState();
  const system = useServerSystem();
  // A fresh page asks am serve first; until it answers, a key is neither
  // saved nor missing.
  const loaded = !!settings;
  const anthropic = settings?.anthropicKeySet ?? settings?.keySet === true;
  const gemini = settings?.geminiKeySet === true;
  const [editing, setEditing] = useState<KeyDraft>();
  const scroll = useTestScroll();

  useEffect(() => {
    void loadKeys().then(() => {
      // For comparisons: sheet=key, or key:<NAME> to edit one.
      const sheet = query.get("sheet");
      if (sheet === "key") setEditing(newDraft());
      else if (sheet?.startsWith("key:")) {
        const key = settingsState().keys.find((k) => k.name === sheet.slice(4));
        if (key) setEditing(editDraft(key));
      }
    });
  }, []);

  return (
    <div className="settings-scroll" ref={scroll}>
      <div className="settings-page tight">
        <div className="page-head">
          <div className="page-head-text">
            <h1 className="settings-display">Keys</h1>
            <div className="page-intro">
              Agentman's own API keys, and the keys it hands to your agents. {system === "darwin" ? "Values go into your Mac's Keychain" : system ? `Values are ${keptWhere(system)}` : "Values are kept on this computer"} and are never shown again.
            </div>
          </div>
          <span className="page-head-gap" />
          <PushButton icon={Plus} sf="plus" onClick={() => setEditing(newDraft())}>
            Add key
          </PushButton>
        </div>

        {/* Agentman's own keys first: they used to be in General. */}
        <KeySection provider="anthropic" saved={loaded ? anthropic : undefined} />
        <KeySection provider="gemini" saved={loaded ? gemini : undefined} />

        <div className="keys-for-agents">For your agents</div>
        {keys.length === 0 ? (
          <div className="card keys-empty">No keys yet</div>
        ) : (
          <Section>
            {keys.map((key, index) => (
              <Row
                key={key.name}
                title={key.name}
                detail={[key.description, ruleText(key)].filter((s) => s).join(" · ")}
                divider={index < keys.length - 1}
              >
                <div className="hstack" style={{ gap: 10 }}>
                  {!key.set && <span className="caption needs-you-text">No value</span>}
                  <PushButton onClick={() => setEditing(editDraft(key))}>Edit</PushButton>
                </div>
              </Row>
            ))}
          </Section>
        )}
        <div className="keys-note">They ask for one through Agentman, or Agentman gives one when you ask. It goes into the project's .env, and only when git ignores that file.</div>

        {/* Agentman's own sign-in for voice prompts, apart from the keys it
            hands to agents. */}
        <VoiceSection />
      </div>
      {editing && <KeyEditor initial={editing} close={() => setEditing(undefined)} />}
    </div>
  );
}

function KeyEditor({ initial, close }: { initial: KeyDraft; close: () => void }) {
  const [draft, setDraft] = useState(initial);
  const [problem, setProblem] = useState<string>();
  const [busy, setBusy] = useState(false);
  const act = async (work: () => Promise<string | undefined>) => {
    setBusy(true);
    const p = await work();
    setProblem(p);
    setBusy(false);
    if (!p) close();
  };
  const canSave = !busy && !!draft.name && !(draft.original === undefined && !draft.value);
  const save = () => {
    if (!canSave) return;
    const key: Key = {
      name: draft.name,
      description: draft.description,
      rule: draft.rule,
      projects: draft.rule === "projects" ? draft.projects : undefined,
      set: draft.original?.set ?? false,
      updatedAt: 0,
    };
    void act(() => saveKey(key, draft.value.trim()));
  };
  const addFolder = async () => {
    const paths = await pick({ folders: true, multiple: true });
    const projects = [...draft.projects];
    for (const p of paths) if (!projects.includes(p)) projects.push(p);
    setDraft({ ...draft, projects });
  };
  return (
    <Sheet width={460} onCancel={close} onDefault={save}>
      <div className="key-editor">
        <div className="form-pane">
        <div className="form-group">
          <div className="form-row tall">
            <span className="form-label">Name</span>
            <TextField
              className="form-field"
              mono
              align="right"
              value={draft.name}
              placeholder="ELEVENLABS_API_KEY"
              disabled={!!draft.original}
              autoFocus={!draft.original}
              onChange={(name) => setDraft({ ...draft, name })}
            />
          </div>
          <div className="form-row tall">
            <span className="form-label">Value</span>
            <TextField
              className="form-field"
              mono
              secure
              align="right"
              value={draft.value}
              placeholder={draft.original?.set ? "Leave empty to keep the current one" : "Paste the key"}
              onChange={(value) => setDraft({ ...draft, value })}
            />
          </div>
          <div className="form-row">
            <span className="form-label">What it's for</span>
            <TextField
              className="form-field"
              align="right"
              value={draft.description}
              placeholder="Voices for the launch videos"
              onChange={(description) => setDraft({ ...draft, description })}
            />
          </div>
          <div className="form-row">
            <span className="form-label">Give it</span>
            <PopUp
              className="borderless"
              value={draft.rule}
              options={[
                { value: "auto", label: "To any agent that asks" },
                { value: "projects", label: "Only to agents in these folders" },
                { value: "ask", label: "Ask me every time" },
              ]}
              onChange={(rule) => setDraft({ ...draft, rule })}
            />
          </div>
          {draft.rule === "projects" && (
            <>
              {draft.projects.map((folder) => (
                <div className="form-row" key={folder}>
                  <span className="form-folder">{folder}</span>
                  <button className="form-remove" aria-label="Remove" onClick={() => setDraft({ ...draft, projects: draft.projects.filter((f) => f !== folder) })}>
                    <Icon icon={CircleMinus} sf="minus.circle" size={13} />
                  </button>
                </div>
              ))}
              <div className="form-row">
                <PushButton onClick={() => void addFolder()}>Add a folder…</PushButton>
              </div>
            </>
          )}
          {problem && (
            <div className="form-row">
              <span className="caption error-text">{problem}</span>
            </div>
          )}
        </div>
        </div>
        <div className="sheet-buttons">
          {draft.original && (
            <PushButton destructive onClick={() => void act(() => deleteKey(draft.original!))}>
              Delete
            </PushButton>
          )}
          <span className="row-spacer" />
          <PushButton onClick={close}>Cancel</PushButton>
          <PushButton prominent disabled={!canSave} onClick={save}>
            Save
          </PushButton>
        </div>
      </div>
    </Sheet>
  );
}

/** Voice prompts: Celery writes out what the user says. Agentman signs in to
 *  their Celery account as a device of its own, with a code Celery emails. */
function VoiceSection() {
  const [account, setAccount] = useState<VoiceReply>();
  const [email, setEmail] = useState("");
  const [code, setCode] = useState("");
  const [requestId, setRequestId] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string>();

  useEffect(() => {
    void voiceAccount().then((found) => {
      if (found.account) setAccount(found.account);
      // For comparisons: voice=code or signedin.
      const voice = query.get("voice");
      if (voice === "code") {
        setEmail("you@example.com");
        setRequestId("test");
      } else if (voice === "signedin") {
        setAccount({ signedIn: true, email: "you@example.com", plan: "pro" });
      }
    });
  }, []);

  const act = async (type: string, step?: { email?: string; requestId?: string; code?: string }) => {
    setBusy(true);
    setProblem(undefined);
    const reply = await voiceAccount(type, step);
    if (reply.account) {
      if (reply.account.requestId && type === "voice_signin_start") {
        setRequestId(reply.account.requestId);
      } else {
        setAccount(reply.account);
        setRequestId(undefined);
        setCode("");
      }
    } else {
      setProblem(reply.problem);
    }
    setBusy(false);
  };
  const start = () => {
    if (email.includes("@")) void act("voice_signin_start", { email: email.trim() });
  };
  const verify = (id: string) => void act("voice_signin_verify", { requestId: id, code: code.trim() });
  const signedIn = () => {
    const who = account?.email ? `Signed in as ${account.email}` : "Signed in";
    const plan = account?.plan;
    return plan ? `${who} · ${plan[0].toUpperCase()}${plan.slice(1)} plan` : who;
  };

  return (
    <Section
      title="Voice prompts"
      footer="Speak a message with the mic beside any message box, here or on your phone. Celery writes it out: the ums and false starts go, and nothing is reworded."
    >
      {account?.signedIn ? (
        <Row title="Celery" detail={signedIn()} divider={false}>
          <PushButton disabled={busy} onClick={() => void act("voice_signout")}>
            Sign Out
          </PushButton>
        </Row>
      ) : requestId ? (
        <Row title="Code" detail={`Celery emailed a code to ${email}.`} divider={false}>
          <div className="hstack" style={{ gap: 8 }}>
            <TextField className="code-field" value={code} placeholder="Code" onChange={setCode} onSubmit={() => verify(requestId)} />
            <PushButton disabled={!code.trim() || busy} onClick={() => verify(requestId)}>
              Sign In
            </PushButton>
            <LinkButton
              onClick={() => {
                setRequestId(undefined);
                setCode("");
              }}
            >
              Back
            </LinkButton>
          </div>
        </Row>
      ) : (
        <Row title="Celery" detail="Sign in to your Celery account to speak your messages." divider={false}>
          <div className="hstack" style={{ gap: 8 }}>
            <TextField className="email-field" value={email} placeholder="you@example.com" onChange={setEmail} onSubmit={start} />
            <PushButton disabled={!email.includes("@") || busy} onClick={start}>
              Send Code
            </PushButton>
          </div>
        </Row>
      )}
      {problem && <div className="voice-problem">{problem}</div>}
    </Section>
  );
}
