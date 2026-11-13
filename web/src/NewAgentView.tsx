// Starting an agent (Views/NewAgentView.swift): what it should work on,
// which agent, and where, in one box, the way Codex and Cursor start a
// task. Return starts it. Where can be a worktree of the folder, one
// already made or a new one, so several agents can work on one repository
// at once.

import { useEffect, useRef, useState } from "react";

import { ArrowUp, ChevronDown, Folder as FolderIcon, GitBranch } from "lucide-react";
import { agentName } from "./labels";
import { pickFolder, hostCall } from "./host";
import { openAgent, openNewAgent } from "./nav";
import { usePref } from "./prefs";
import { homeFolder } from "./shell/native";
import { Sym } from "./shell/Sym";
import { ask, getShell, loadFolders, plainProblem, setShell, useShell } from "./shell/state";
import { getStore, useStatus } from "./store";
import { menuUnder, type MenuEntry } from "./thread/menu";
import { AgentMark, Icon } from "./ui";
import { tilde } from "./settings/controls";
import "./styles/newagent.css";

export const AGENT_KINDS = ["claude", "codex", "cursor-cli", "opencode", "kiro", "antigravity", "muse"];

/** The product names the menu shows (AgentName.of). */
const kindName = agentName;

interface Worktree {
  path: string;
  name: string;
  repo: string;
  branch?: string;
  main?: boolean;
}

type Place = { kind: "folder" } | { kind: "worktree"; worktree: Worktree } | { kind: "new"; name: string };

/** "New Worktree from…" in a thread: this page, on the agent's folder, with
 *  the worktree namer up (NewAgentPreset). */
export function newWorktreeFrom(folder: string) {
  setShell({ newAgentPreset: { folder, newWorktree: true } });
  openNewAgent();
}

/** A worktree's name as its branch, as the daemon makes it. */
export function branchName(name: string): string {
  return name.trim().replace(/\s+/g, "-").replace(/^[-/]+|[-/]+$/g, "");
}

/** A worktree's name from the task: its first few words. */
export function suggestedName(task: string): string {
  return (task.toLowerCase().match(/[\p{L}\p{N}]+/gu) ?? []).slice(0, 4).join("-");
}

const inShell = location.protocol === "wails:" || location.hostname === "wails.localhost";

async function folderExists(path: string): Promise<boolean> {
  if (!inShell) return true;
  try {
    const info = (await hostCall("Files.Folder", path)) as { exists: boolean };
    return info.exists;
  } catch {
    return true;
  }
}

function relative(folder: string, home: string): string | null {
  return folder.startsWith(home + "/") ? folder.slice(home.length + 1) : null;
}

async function worktreeReply(req: Record<string, unknown> & { type: string }) {
  const reply = await ask(req);
  if (reply.type === "error") {
    const message = plainProblem(reply.error, "Git couldn't do that.");
    throw new Error(message.charAt(0).toUpperCase() + message.slice(1));
  }
  return reply;
}

export function NewAgentView({ worktree: namingAtStart }: { worktree?: boolean }) {
  const { folders, newAgentPreset } = useShell();
  const { status } = useStatus();
  const [kind, setKind] = usePref("newAgentKind", "claude");
  const [folder, setFolder] = usePref("newAgentFolder", "");
  const [message, setMessage] = useState("");
  const [starting, setStarting] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const [place, setPlace] = useState<Place>({ kind: "folder" });
  const [worktrees, setWorktrees] = useState<Worktree[]>([]);
  const [naming, setNaming] = useState(!!namingAtStart);
  const [home, setHome] = useState("");
  const box = useRef<HTMLTextAreaElement>(null);
  const whereButton = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    void homeFolder().then(setHome);
    box.current?.focus();
  }, []);
  // The recent folders, once am serve can be asked; a folder that's gone
  // falls back to the newest.
  useEffect(() => {
    if (status !== "connected") return;
    void (async () => {
      await loadFolders();
      const list = getShell().folders;
      if (!getShell().newAgentPreset && (!folder || !(await folderExists(folder)))) setFolder(list[0]?.path ?? "");
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [status]);

  // Sent here to start one in a particular place.
  useEffect(() => {
    if (!newAgentPreset) return;
    setShell({ newAgentPreset: null });
    setFolder(newAgentPreset.folder);
    setPlace({ kind: "folder" });
    setNaming(newAgentPreset.newWorktree);
  }, [newAgentPreset, setFolder]);

  // The folder's worktrees, its own first; none when it isn't in a repository.
  useEffect(() => {
    let live = true;
    void (async () => {
      const h = home || (await homeFolder());
      const rel = folder ? relative(folder, h) : null;
      if (!rel) {
        if (live) {
          setWorktrees([]);
          setPlace({ kind: "folder" });
          setNaming(false);
        }
        return;
      }
      try {
        const reply = await worktreeReply({ type: "list_worktrees", path: rel });
        if (!live) return;
        const list = (reply.worktrees as Worktree[] | undefined) ?? [];
        setWorktrees(list);
        setPlace((p) => (p.kind === "worktree" && !list.some((w) => w.path === p.worktree.path) ? { kind: "folder" } : p));
      } catch {
        if (!live) return;
        setWorktrees([]);
        setPlace({ kind: "folder" });
        setNaming(false);
      }
    })();
    return () => {
      live = false;
    };
  }, [folder, home]);

  const canStart = !starting && !!folder;
  const repo = worktrees[0]?.repo ?? (folder.replace(/\/+$/, "").split("/").pop() || folder);
  // Home as ~ at the start only: /Users/mackenzie isn't ~kenzie.
  const short = (path: string) => tilde(path, home);
  const fromBranch = worktrees.find((w) => w.path === folder)?.branch;

  const whereItStarts = (() => {
    switch (place.kind) {
      case "folder":
        return `in ${short(folder)}`;
      case "worktree":
        return `in the ${place.worktree.name} worktree, on ${place.worktree.branch ?? "a detached HEAD"}`;
      case "new": {
        const from = fromBranch ? `from ${fromBranch}` : `from what ${short(folder)} has checked out`;
        return `in a new worktree, ~/Agentman/Worktrees/${repo}/${place.name.replace(/\//g, "-")}, on a new branch ${place.name} ${from}`;
      }
    }
  })();

  async function start() {
    if (!canStart) return;
    setStarting(true);
    setProblem(null);
    const h = home || (await homeFolder());
    let target = folder;
    try {
      if (place.kind === "worktree") target = place.worktree.path;
      if (place.kind === "new") {
        const rel = relative(folder, h);
        if (!rel) throw new Error("Worktrees are made from a folder inside your home folder.");
        const reply = await worktreeReply({ type: "create_worktree", path: rel, text: place.name });
        const made = reply.worktree as Worktree | undefined;
        if (!made) throw new Error("Update am serve to make worktrees from here.");
        target = made.path;
        // Chosen from now on, so trying again starts the agent there rather
        // than making the worktree twice.
        setPlace({ kind: "worktree", worktree: made });
      }
    } catch (err) {
      setProblem(err instanceof Error ? err.message : String(err));
      setStarting(false);
      return;
    }
    const failed = await startAgent(kind, target, message, h);
    setProblem(failed);
    setStarting(false);
    if (!failed) setMessage("");
  }

  function agentMenu(e: React.MouseEvent<HTMLButtonElement>) {
    menuUnder(e.currentTarget, AGENT_KINDS.map((option) => ({ label: kindName(option), checked: option === kind, action: () => setKind(option) })));
  }

  async function choose() {
    const picked = await pickFolder("Choose where the agent works", folder || home, { button: "Choose", newFolder: true });
    if (picked) setFolder(picked);
  }

  function whereMenu(e: React.MouseEvent<HTMLButtonElement>) {
    const entries: MenuEntry[] = [{ label: "Recent", header: true }];
    for (const recent of folders.slice(0, 10)) {
      entries.push({ label: short(recent.path), checked: recent.path === folder && place.kind === "folder", action: () => {
        setFolder(recent.path);
        setPlace({ kind: "folder" });
      } });
    }
    if (worktrees.length) {
      entries.push({ label: `Worktrees of ${repo}`, header: true });
      for (const w of worktrees.filter((w) => w.path !== folder)) {
        const title = w.main ? `${w.name} (the repository)` : w.name;
        entries.push({ label: title, checked: place.kind === "worktree" && place.worktree.path === w.path, action: () => setPlace({ kind: "worktree", worktree: w }) });
      }
      entries.push({ label: "New Worktree…", action: () => setNaming(true) });
    }
    entries.push({ separator: true });
    entries.push({ label: "Choose a folder…", action: () => void choose() });
    menuUnder(e.currentTarget, entries);
  }

  const whereLabel = place.kind === "folder"
    ? (folder ? folder.replace(/\/+$/, "").split("/").pop() : "Choose a folder")
    : place.kind === "worktree" ? `${place.worktree.repo} · ${place.worktree.name}` : `${repo} · ${place.name}`;

  return (
    <div className="new-agent-page">
    {/* A title row, as every page has (NewAgentView's safeAreaInset). */}
    <div className="new-agent-header"><span className="new-agent-header-title">New agent</span></div>
    <div className="new-agent">
      <div className="new-agent-spacer top" />
      <div className="new-agent-title">What should we work on?</div>
      <div className="new-agent-box" onMouseDown={(e) => {
        if (e.target === e.currentTarget) {
          e.preventDefault();
          box.current?.focus();
        }
      }}>
        <AutoGrow
          ref={box}
          value={message}
          disabled={starting}
          placeholder="Describe the task, or leave it empty to start a session"
          onChange={setMessage}
          onSubmit={() => void start()}
        />
        <div className="new-agent-row">
          <button className="pill" title="Which agent to start" onClick={agentMenu}>
            <AgentMark kind={kind} size={14} />
            <span>{kindName(kind)}</span>
            <Sym className="pill-chevron" icon={ChevronDown} sf="chevron.down" size={8} weight="bold" />
          </button>
          <span className="pill-anchor">
            <button ref={whereButton} className="pill" title={folder || "Where it works"} onClick={whereMenu}>
              {place.kind === "folder"
                ? <Sym icon={FolderIcon} sf="folder" size={11} weight="medium" />
                : <Sym icon={GitBranch} sf="arrow.triangle.branch" size={11} weight="medium" />}
              <span>{whereLabel}</span>
              <Sym className="pill-chevron" icon={ChevronDown} sf="chevron.down" size={8} weight="bold" />
            </button>
            {naming && (
              <WorktreeNamer
                repo={repo}
                from={fromBranch}
                suggestion={suggestedName(message)}
                done={(name) => {
                  setNaming(false);
                  if (name) setPlace({ kind: "new", name });
                  box.current?.focus();
                }}
              />
            )}
          </span>
          <span style={{ flex: 1 }} />
          <button className={`start-button ${canStart ? "" : "disabled"}`} disabled={!canStart} title={`Start ${kindName(kind)}`} onClick={() => void start()}>
            {starting ? <span className="spinner" /> : <Icon icon={ArrowUp} sf="arrow.up" size={13} weight="bold" />}
          </button>
        </div>
      </div>
      {problem ? (
        <div className="new-agent-line problem">{problem}</div>
      ) : folder ? (
        <div className="new-agent-line">{starting ? `Starting ${kindName(kind)} ${whereItStarts}…` : `Starts ${whereItStarts}`}</div>
      ) : null}
      <div className="new-agent-spacer bottom" />
    </div>
    </div>
  );
}

/** Starts an agent in a folder in the user's home, with a first message if
 *  there is one, and opens it once it shows. Returns the problem, if any. */
async function startAgent(kind: string, folder: string, message: string, home: string): Promise<string | null> {
  if (!(folder === home || folder.startsWith(home + "/"))) return "Choose a folder inside your home folder.";
  try {
    const reply = await ask({
      type: "start_session",
      kind,
      path: folder.slice(home.length).replace(/^\/+|\/+$/g, ""),
      text: message.trim(),
      clientId: `app-start-${crypto.randomUUID()}`,
    });
    if (reply.type === "error" || !reply.sessionId) return String(reply.error ?? "It didn't start.").replace(/^daemon: /, "");
    const id = String(reply.sessionId);
    // Opened once the daemon lists it, with the filters cleared.
    for (let i = 0; i < 40 && !getStore().sessions.some((s) => s.id === id); i++) await new Promise((r) => setTimeout(r, 250));
    setShell({ folderFilter: null, stateFilter: null });
    openAgent(id);
    return null;
  } catch (err) {
    return err instanceof Error ? err.message : String(err);
  }
}

/** The task box: grows with what is typed, Return starts, Shift-Return a new line. */
function AutoGrow({ ref, value, disabled, placeholder, onChange, onSubmit }: {
  ref: React.Ref<HTMLTextAreaElement>; value: string; disabled: boolean; placeholder: string; onChange: (v: string) => void; onSubmit: () => void;
}) {
  const inner = useRef<HTMLTextAreaElement | null>(null);
  useEffect(() => {
    const el = inner.current;
    if (!el) return;
    el.style.height = "0px";
    el.style.height = `${Math.min(el.scrollHeight, 220)}px`;
  }, [value]);
  return (
    <textarea
      ref={(el) => {
        inner.current = el;
        if (typeof ref === "function") ref(el);
        else if (ref) (ref as React.RefObject<HTMLTextAreaElement | null>).current = el;
      }}
      className="new-agent-field"
      rows={1}
      value={value}
      disabled={disabled}
      placeholder={placeholder}
      spellCheck={false}
      onChange={(e) => onChange(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === "Enter" && !e.shiftKey && !e.altKey && !e.nativeEvent.isComposing) {
          e.preventDefault();
          onSubmit();
        }
      }}
    />
  );
}

/** Naming a new worktree, in a popover under the where menu: one field, the
 *  name it gets for its folder and its branch, suggested from the task. */
function WorktreeNamer({ repo, from, suggestion, done }: { repo: string; from?: string; suggestion: string; done: (name: string | null) => void }) {
  const [name, setName] = useState(suggestion);
  const field = useRef<HTMLInputElement>(null);
  const card = useRef<HTMLDivElement>(null);
  useEffect(() => {
    field.current?.focus();
    field.current?.select();
    const outside = (e: MouseEvent) => {
      if (card.current && !card.current.contains(e.target as Node)) done(null);
    };
    const t = setTimeout(() => document.addEventListener("mousedown", outside), 0);
    return () => {
      clearTimeout(t);
      document.removeEventListener("mousedown", outside);
    };
  }, [done]);
  const use = () => {
    const branch = branchName(name);
    if (branch) done(branch);
  };
  return (
    <div className="namer" ref={card} onKeyDown={(e) => {
      if (e.key === "Escape") {
        e.preventDefault();
        done(null);
      }
    }}>
      <div className="namer-arrow" />
      <div className="namer-title">New worktree of {repo}</div>
      <input
        ref={field}
        className="namer-field"
        placeholder="Name, such as rate-limits"
        value={name}
        spellCheck={false}
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            use();
          }
        }}
      />
      <div className="namer-text">A folder and a branch of its own{from ? `, from ${from}` : ""}, so this agent works beside the others without getting in their way.</div>
      <div className="namer-buttons">
        <button className="mac-button" onClick={() => done(null)}>Cancel</button>
        <button className="mac-button default" disabled={!branchName(name)} onClick={use}>Use</button>
      </div>
    </div>
  );
}
