// Permissions (Views/SettingsView.swift PermissionsPage): what Agentman may
// approve without asking, the user's own rules, a way to try a request
// against them, and what always waits for the user.

import { useEffect, useState } from "react";
import type { Autopilot, ManagerSettings, PermissionCheck } from "@shared/protocol";
import { Asterisk, CircleCheck, CircleMinus, FileSearch, Globe, Hand, Pencil, Plus, SquareTerminal, X, type LucideIcon } from "lucide-react";
import { Icon } from "../ui";
import { LinkButton, MenuButton, PopUp, PushButton, Segmented, TextField, Toggle, lastPart, tilde } from "./controls";
import { Divider, Row, Section, SettingsPage, keepLast } from "./layout";
import { pick, useHome } from "./native";
import { Sheet } from "./Sheet";
import { menuUnder } from "../thread/menu";
import { checkPermission, loadFolders, useSettingsState } from "./state";

type Rule = NonNullable<ManagerSettings["rules"]>[number];
type Kinds = NonNullable<ManagerSettings["approve"]>;
const ALL: Kinds = { reads: true, edits: true, commands: true, continue: true };

export const RuleKind = {
  all: ["command", "edit", "read", "network", "any"],
  title(kind: string) {
    return ({ command: "Commands", edit: "Changing files", read: "Reading files", network: "Network" } as Record<string, string>)[kind] ?? "Anything";
  },
  sf(kind: string) {
    return ({ command: "terminal", edit: "pencil", read: "doc.text.magnifyingglass", network: "globe" } as Record<string, string>)[kind] ?? "asterisk";
  },
  icon(kind: string): LucideIcon {
    return ({ command: SquareTerminal, edit: Pencil, read: FileSearch, network: Globe } as Record<string, LucideIcon>)[kind] ?? Asterisk;
  },
  placeholder(kind: string) {
    return ({ command: "npm run *", edit: "docs/*", read: "docs/*", network: "https://api.example.com/*" } as Record<string, string>)[kind] ?? "*.env";
  },
};

const ASKED = [
  "Deleting files",
  "Installing software",
  "Commits, pushes and anything else that changes git history",
  "Deploys and cloud changes",
  "Anything on the network",
  "Keys, tokens and credentials (no rule can allow these)",
  "Anything run as root (no rule can allow these)",
];

interface Props {
  draft?: ManagerSettings;
  setDraft: (next: ManagerSettings) => void;
}

const query = new URLSearchParams(location.search);

/** Every switch off, as Autopilot() starts (Protocol.swift). */
const AUTOPILOT_OFF: Autopilot = { verify: false, sendBack: false, learnRules: false, unstick: false, overlap: false, learnNotes: false, overnight: false };
/** Reviews of finished work a day on the user's key (dailyReviews). */
const DAILY_REVIEWS = 30;
/** How much Agentman does in a folder (FolderTrust.levels). */
const TRUST_LEVELS = [
  { value: "watch", label: "Watch" },
  { value: "suggest", label: "Suggest" },
  { value: "handle", label: "Handle" },
];
const HOURS = Array.from({ length: 24 }, (_, hour) => `${String(hour).padStart(2, "0")}:00`).map((h) => ({ value: h, label: h }));
type Switch = "verify" | "sendBack" | "learnRules" | "unstick" | "overlap" | "learnNotes" | "overnight" | "replyCommit" | "replyInstall";

export function PermissionsPage({ draft, setDraft }: Props) {
  // For comparisons: sheet=rule opens the sheet.
  const [adding, setAdding] = useState(query.get("sheet") === "rule");
  const { folders } = useSettingsState();
  useEffect(() => void loadFolders(), []);

  // What Agentman does by itself (SettingsView.swift's autopilotRow).
  const autopilot = draft?.autopilot ?? AUTOPILOT_OFF;
  const setAutopilot = (next: Partial<Autopilot>) => draft && setDraft({ ...draft, autopilot: { ...autopilot, ...next } });
  const autopilotRow = (title: string, detail: string | undefined, key: Switch, divider = true, disabled = false) => (
    <Row title={title} detail={detail} divider={divider} className={disabled ? "dimmed" : ""}>
      <Toggle on={!!autopilot[key]} disabled={disabled} onChange={(on) => setAutopilot({ [key]: on })} />
    </Row>
  );
  const hourRow = (title: string, key: "nightStart" | "nightEnd", fallback: string, divider: boolean) => (
    <Row title={title} divider={divider}>
      <PopUp value={autopilot[key] ?? fallback} options={HOURS} onChange={(value) => setAutopilot({ [key]: value })} />
    </Row>
  );
  const trust = draft?.trust ?? [];
  const setTrust = (next: { folder: string; level: string }[]) => draft && setDraft({ ...draft, trust: next });

  const kindRow = (title: string, key: keyof Kinds, divider = true) => (
    <Row title={title} divider={divider}>
      <Toggle
        on={(draft?.approve ?? ALL)[key]}
        disabled={!draft?.autoApprove}
        onChange={(on) => draft && setDraft({ ...draft, approve: { ...(draft.approve ?? ALL), [key]: on } })}
      />
    </Row>
  );

  return (
    <SettingsPage title="Permissions">
      {draft && (
        <>
          <Section>
            <Row title="Approve low-risk requests for me" detail="Agentman answers these the moment an agent asks, and tells you in a line." divider={false}>
              <Toggle on={draft.autoApprove} onChange={(autoApprove) => setDraft({ ...draft, autoApprove })} />
            </Row>
          </Section>
          <Section
            title="Agentman may approve"
            footer="Inside the agent's own folder, and never anything that touches keys or credentials."
            className={draft.autoApprove ? "" : "dimmed"}
          >
            {kindRow("Reading and searching files", "reads")}
            {kindRow("Changing files", "edits")}
            {kindRow("Tests, builds and read-only commands", "commands")}
            {kindRow("“Continue?” prompts", "continue", false)}
          </Section>
          <Section
            title="When an agent asks"
            footer="Only when that is all it's asking, in folders on Handle, at most twice for one request, and never while you're looking at it. Agentman sends a fixed yes and lists it under Handled. Pushing, deploying and merging always come to you."
          >
            {autopilotRow("Say yes to committing finished work", "The agent is told not to push.", "replyCommit")}
            {autopilotRow("Say yes to installing on my phone or Mac", "A finished build, on your own device.", "replyInstall", false)}
          </Section>
          <Section
            title="When an agent finishes"
            footer={`Has your Agentman model compare the change with what was asked, up to ${DAILY_REVIEWS} reviews a day on your API key. In folders on Handle it runs their tests first, and only there can failures go back.`}
          >
            {autopilotRow("Offer rules for what I keep approving", "After three approvals of the same safe thing. Never for pushes, deletes, deploys or keys.", "learnRules")}
            {autopilotRow("Check its work", "The card on the Board says whether it holds up.", "verify")}
            {autopilotRow("Send failing tests back to the agent", "In folders on Handle, at most twice for one request, and never while you're looking at it.", "sendBack", false, !autopilot.verify)}
          </Section>
          <Section
            title="While agents work"
            footer="Noticing is read from their transcripts, without using your API key. What happens next follows the folder's trust below."
          >
            {autopilotRow("Notice stuck agents", "The same thing failing, long silences, a full context or a usage limit.", "unstick")}
            {autopilotRow("Notice agents on the same files", "Two changing one file, or one changing what another has read.", "overlap")}
            {autopilotRow("Suggest notes for what I keep telling agents", "Once a day, on your API key. You approve each note.", "learnNotes", false)}
          </Section>
          <Section
            title="Overnight"
            footer="Only what you line up on the Board starts, a few at a time and one to a folder, and you get a report when the night ends."
          >
            {autopilotRow("Run my queue overnight", undefined, "overnight")}
            {hourRow("Starts", "nightStart", "23:00", true)}
            {hourRow("Ends", "nightEnd", "07:00", false)}
          </Section>
          <Section
            title="Trust by folder"
            footer="Watch reports and asks. Suggest marks the answer it would give and waits. Handle answers what the rules here allow. A folder without a level follows the switch at the top."
          >
            {trust.map((entry, index) => (
              <Row key={entry.folder} title={lastPart(entry.folder)} detail={entry.folder}>
                <PopUp value={entry.level} options={TRUST_LEVELS} onChange={(level) => setTrust(trust.map((t, i) => (i === index ? { ...t, level } : t)))} />
                <button className="trust-remove" title="Follow the switch at the top instead" aria-label={`Stop trusting ${lastPart(entry.folder)} by its own level`} onClick={() => setTrust(trust.filter((_, i) => i !== index))}>
                  <Icon icon={CircleMinus} sf="minus.circle" size={13} />
                </button>
              </Row>
            ))}
            <div className="card-line add-line">
              {/* A folder agents ran in, starting on Suggest (Menu "Add Folder"). */}
              <button
                className="mac-link"
                onClick={(e) => {
                  const open = folders.filter((f) => !trust.some((t) => t.folder === f.path)).slice(0, 20);
                  menuUnder(e.currentTarget, open.length
                    ? open.map((f) => ({ label: lastPart(f.path), action: () => setTrust([...trust, { folder: f.path, level: "suggest" }]) }))
                    : [{ label: "No other folders", disabled: true }]);
                }}
              >
                <Icon icon={Plus} sf="plus" size={13} />
                <span>Add Folder</span>
              </button>
            </div>
          </Section>
          <Section
            title="Your rules"
            footer="An “Always ask” rule wins over everything. No rule lets an agent touch keys and credentials or run anything as root. Use * for anything and ? for one character."
          >
            {(draft.rules ?? []).length === 0 && (
              <>
                <div className="card-line rules-empty">
                  <span>{keepLast("No rules yet. Allow something Agentman would ask you about, or make it always ask.")}</span>
                </div>
                <Divider />
              </>
            )}
            {(draft.rules ?? []).map((rule, index) => (
              <RuleLine key={index} rule={rule} remove={() => setDraft({ ...draft, rules: (draft.rules ?? []).filter((_, i) => i !== index) })} />
            ))}
            <div className="card-line add-rule">
              <LinkButton onClick={() => setAdding(true)}>
                <Icon icon={Plus} sf="plus" size={13} />
                <span>Add Rule…</span>
              </LinkButton>
            </div>
          </Section>
          {adding && (
            <NewRuleSheet
              close={() => setAdding(false)}
              add={(rule) => setDraft({ ...draft, rules: [...(draft.rules ?? []), rule] })}
            />
          )}
        </>
      )}
      <TryPermission />
      <Section title="Always waits for you" footer="Unless one of your rules allows it. Anything Agentman doesn't recognise waits for you too, as a card.">
        {ASKED.map((item, index) => (
          <div key={item}>
            <div className="card-line asked">
              <span className="asked-icon">
                <Icon icon={Hand} sf="hand.raised" size={12} weight="medium" />
              </span>
              <span>{item}</span>
            </div>
            {index < ASKED.length - 1 && <Divider inset={42} />}
          </div>
        ))}
      </Section>
    </SettingsPage>
  );
}

/** A rule as a line: what it does, to what, and where. */
function RuleLine({ rule, remove }: { rule: Rule; remove: () => void }) {
  const allow = rule.action === "allow";
  return (
    <div>
      <div className="card-line rule-line">
        <span className={`rule-badge ${allow ? "allow" : "ask"}`}>{allow ? "Allow" : "Always ask"}</span>
        <span className="rule-kind" title={RuleKind.title(rule.kind)}>
          <Icon icon={RuleKind.icon(rule.kind)} sf={RuleKind.sf(rule.kind)} size={11} />
        </span>
        <span className="rule-pattern">{rule.pattern}</span>
        <span className="rule-where" title={rule.folder ?? "Every agent"}>
          {rule.folder ? `in ${lastPart(rule.folder)}` : "everywhere"}
        </span>
        <span className="row-spacer" />
        <button className="rule-remove" title="Remove the rule" aria-label="Remove the rule" onClick={remove}>
          <Icon icon={X} sf="xmark" size={9} weight="bold" />
        </button>
      </div>
      <Divider />
    </div>
  );
}

/** Adding a rule: allow or always ask, for what, matching what, and where. */
function NewRuleSheet({ add, close }: { add: (rule: Rule) => void; close: () => void }) {
  const [action, setAction] = useState<"allow" | "ask">("allow");
  const [kind, setKind] = useState("command");
  const [pattern, setPattern] = useState("");
  const [folder, setFolder] = useState<string>();
  const home = useHome();
  const trimmed = pattern.trim();
  const problem = !trimmed ? "" : action === "allow" && !trimmed.replace(/[*? ]/g, "") ? "That would allow everything. Name something more specific." : undefined;
  const hint =
    problem ||
    ({
      command: "Checked against each part of a command: `npm run *` covers npm run build.",
      edit: "A path inside the agent's folder, or a full one: `docs/*`, `*.md`.",
      read: "A path inside the agent's folder, or a full one: `docs/*`, `*.md`.",
      network: "The address the agent wants to open.",
    } as Record<string, string>)[kind] ||
    "Any request whose command, path or address matches.";
  const submit = () => {
    if (problem !== undefined) return;
    add({ action, kind, pattern: trimmed, ...(folder ? { folder } : {}) });
    close();
  };
  return (
    <Sheet width={460} onCancel={close} onDefault={submit}>
      <div className="rule-sheet">
        <div className="sheet-title">New Rule</div>
        <Segmented
          value={action}
          options={[
            { value: "allow", label: "Allow" },
            { value: "ask", label: "Always ask" },
          ]}
          onChange={setAction}
        />
        <div className="sheet-field">
          <div className="sheet-label">For</div>
          <PopUp
            value={kind}
            options={RuleKind.all.map((k) => ({ value: k, label: RuleKind.title(k), sf: RuleKind.sf(k), icon: RuleKind.icon(k) }))}
            onChange={setKind}
          />
        </div>
        <div className="sheet-field">
          <div className="sheet-label">{kind === "command" ? "Command" : kind === "network" ? "Address" : kind === "any" ? "Matching" : "Path"}</div>
          <TextField mono className="rule-pattern-field" value={pattern} placeholder={RuleKind.placeholder(kind)} onChange={setPattern} autoFocus />
          <div className={`sheet-hint ${problem !== undefined ? "problem" : ""}`}>
            {keepLast(hint)}
          </div>
        </div>
        <div className="sheet-field">
          <div className="sheet-label">Where</div>
          <div className="hstack" style={{ gap: 8 }}>
            <span className="sheet-where">{folder ? tilde(folder, home) : "Every agent, in any folder"}</span>
            <span className="row-spacer" />
            {folder && <PushButton onClick={() => setFolder(undefined)}>Everywhere</PushButton>}
            <PushButton
              onClick={() =>
                void pick({ folders: true, button: "Choose", start: folder ?? home }).then((paths) => {
                  if (paths[0]) setFolder(paths[0]);
                })
              }
            >
              Choose a Folder…
            </PushButton>
          </div>
        </div>
        <div className="sheet-buttons">
          <span className="row-spacer" />
          <PushButton onClick={close}>Cancel</PushButton>
          <PushButton prominent disabled={problem !== undefined} onClick={submit}>
            Add Rule
          </PushButton>
        </div>
      </div>
    </Sheet>
  );
}

/** "Try it": what Agentman would do with a command or path, under the
 *  settings as saved. */
function TryPermission() {
  const home = useHome();
  const [kind, setKind] = useState("command");
  const [target, setTarget] = useState("");
  const [folder, setFolder] = useState("");
  const [check, setCheck] = useState<PermissionCheck>();
  const { folders } = useSettingsState();
  const where = folder || home;

  useEffect(() => {
    void loadFolders();
  }, []);

  const run = async () => {
    if (!target.trim()) return;
    setCheck(await checkPermission(kind, target, where));
  };

  return (
    <Section title="Try it" footer="Uses your saved settings, so save changes first to try them.">
      <div className="try-it">
        <div className="hstack" style={{ gap: 8 }}>
          <PopUp value={kind} options={["command", "edit", "read", "network"].map((k) => ({ value: k, label: RuleKind.title(k) }))} onChange={setKind} />
          <TextField
            mono
            className="try-field"
            value={target}
            placeholder={kind === "command" ? "git push origin main" : kind === "network" ? "https://example.com" : "src/app.ts"}
            onChange={setTarget}
            onSubmit={() => void run()}
          />
          <PushButton disabled={!target.trim()} onClick={() => void run()}>
            Check
          </PushButton>
        </div>
        <div className="hstack" style={{ gap: 6 }}>
          <span className="caption muted">For an agent in</span>
          <MenuButton className="plain caption" entries={() => folders.slice(0, 12).map((f) => ({ label: tilde(f.path, home), action: () => setFolder(f.path) }))}>
            {tilde(where, home)}
          </MenuButton>
        </div>
        {check && (
          <div className="hstack try-verdict" style={{ gap: 6 }}>
            <span className={check.auto ? "ok" : "needs-you"}>
              <Icon icon={check.auto ? CircleCheck : Hand} sf={check.auto ? "checkmark.circle.fill" : "hand.raised.fill"} size={12} />
            </span>
            <span>{check.auto ? `Agentman would approve it: ${check.summary}.` : `It would wait for you: ${check.reason ?? check.summary}.`}</span>
          </div>
        )}
      </div>
    </Section>
  );
}

