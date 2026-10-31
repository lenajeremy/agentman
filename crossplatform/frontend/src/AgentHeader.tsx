// An agent's header (AgentThreadView.swift's title bar): "folder / name",
// the name renamed in place; then the servers menu, terminal, changes
// panel, workspace card and the more menu.

import { useEffect, useRef, useState, type MouseEvent } from "react";
import { Ellipsis, Globe, PanelRight, SquareTerminal } from "lucide-react";
import type { Server, Session } from "@shared/protocol";
import { daemon } from "./daemon";
import { openEditor, openURL } from "./host";
import { folderLabel } from "./labels";
import { go } from "./nav";
import { ChromeButton, Icon, PlusSlashMinus } from "./ui";
import { copyText } from "./thread/clipboard";
import { confirmSheet, contextMenu, menuUnder, type MenuEntry } from "./thread/menu";
import { useFolder } from "./thread/folder";
import { ask, setActionError } from "./thread/state";

const RESUMABLE = new Set(["claude", "codex", "cursor-cli", "kiro", "antigravity"]);

/** A session running in a terminal agentman can close and reopen. */
export function endable(session: Session): boolean {
  return session.inject === "tmux" && RESUMABLE.has(session.kind);
}

/** An ended session its agent can reopen, as the phone decides it. */
export function resumable(session: Session): boolean {
  return session.inject === "none" && session.state === "ended" && RESUMABLE.has(session.kind);
}

const WORKTREE = /^(.*)\/Agentman\/Worktrees\/([^/]+)\/([^/]+)/;

/** A worktree Agentman made: its home, repository and name. */
function worktreeParts(cwd: string): { home: string; repo: string; name: string } | null {
  const m = WORKTREE.exec(cwd);
  return m ? { home: m[1], repo: m[2], name: m[3] } : null;
}

/** The session's name, renamed in place: Return keeps it, Escape or clicking
 *  away leaves it as it was. */
function ThreadTitle({ session }: { session: Session }) {
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState("");
  const field = useRef<HTMLInputElement>(null);
  const original = (session as Session & { originalName?: string }).originalName;

  useEffect(() => setEditing(false), [session.id]);
  useEffect(() => {
    if (editing) field.current?.select();
  }, [editing]);

  const begin = () => {
    setName(session.name);
    setEditing(true);
  };
  const rename = (to: string) =>
    void ask({ type: "rename_session", sessionId: session.id, text: to.trim() }, "Couldn't rename it.").catch(() => {});
  const save = () => {
    const next = name.trim();
    setEditing(false);
    if (next && next !== session.name) rename(next);
  };
  const menu = (e: MouseEvent) => {
    const entries: MenuEntry[] = [{ label: "Rename…", action: begin }];
    if (original) entries.push({ label: `Use the agent's own name, “${original}”`, action: () => rename("") });
    contextMenu(e, entries);
  };

  return (
    <div className="crumbs no-drag" onContextMenu={menu}>
      <span className="crumb-folder">{folderLabel(session.cwd)}</span>
      <span className="crumb-slash">/</span>
      {editing ? (
        <input
          ref={field}
          className="crumb-edit"
          placeholder="Name"
          value={name}
          autoFocus
          onChange={(e) => setName(e.target.value)}
          onBlur={() => setEditing(false)}
          onKeyDown={(e) => {
            if (e.nativeEvent.isComposing) return;
            if (e.key === "Enter") save();
            if (e.key === "Escape") setEditing(false);
          }}
        />
      ) : (
        <button
          className="crumb-name"
          onClick={begin}
          title={original ? `Click to rename · renamed from “${original}”` : "Click to rename"}
          aria-label={`Rename ${session.name}`}
        >
          {session.name}
        </button>
      )}
    </div>
  );
}

/** A server as its menu section names it: its page's title, or its
 *  program, and its port. */
function heading(server: Server): string {
  const port = `:${server.port}`;
  if (server.title) return `${server.title} · ${port}`;
  if (server.command) return `${port} · ${server.command}`;
  return port;
}

/** The dev servers an agent runs: open one here, share a link to it, or stop it. */
function ServersMenu({ session, servers }: { session: Session; servers: Server[] }) {
  const ports = servers.map((s) => `:${s.port}`).join(", ");
  const share = async (server: Server) => {
    try {
      const reply = await ask({ type: "open_server", sessionId: session.id, port: server.port }, "It couldn't be shared.");
      if (typeof reply.link === "string") void copyText(reply.link);
      setActionError(session.id, null);
    } catch (err) {
      setActionError(session.id, err instanceof Error ? err.message : String(err));
    }
  };
  const stop = async (server: Server) => {
    const chosen = await confirmSheet(`Stop the server on port ${server.port}?`, "Its process ends. Agentman can't start it again.", ["Stop server", "Cancel"]);
    if (chosen !== "Stop server") return;
    try {
      await ask({ type: "stop_server", sessionId: session.id, port: server.port }, "It couldn't be stopped.");
    } catch (err) {
      setActionError(session.id, err instanceof Error ? err.message : String(err));
    }
  };
  const open = (button: HTMLElement) => {
    const entries: MenuEntry[] = [];
    servers.forEach((server, i) => {
      if (i > 0) entries.push({ separator: true });
      entries.push({ label: heading(server), header: true });
      entries.push({ label: "Open in browser", action: () => void openURL(`http://localhost:${server.port}`) });
      if (server.link) {
        const link = server.link;
        entries.push({ label: "Copy share link", action: () => void copyText(link) });
        entries.push({ label: "Stop sharing", action: () => daemon.send({ type: "close_server", sessionId: session.id, port: server.port }) });
      } else {
        entries.push({ label: "Share a link…", action: () => void share(server) });
      }
      entries.push({ label: "Stop server…", action: () => void stop(server) });
    });
    menuUnder(button, entries);
  };
  return (
    <button className="servers-label" title={`Dev servers · ${ports}`} aria-label={`Dev servers, ${ports}`} onClick={(e) => open(e.currentTarget)}>
      <Icon icon={Globe} sf="globe" size={11} />
      {servers.length === 1 ? ` :${servers[0].port}` : ` ${servers.length}`}
    </button>
  );
}

export function AgentHeader({ session, terminal, panel, card, onTerminal, onPanel, onCard }: {
  session: Session;
  terminal: boolean;
  panel: boolean;
  card: boolean;
  onTerminal: () => void;
  onPanel: () => void;
  onCard: () => void;
}) {
  const folder = useFolder(session.cwd);
  const servers = session.servers ?? [];
  const worktree = worktreeParts(session.cwd);

  // ⌃` the terminal, ⇧⌘D the diff, ⇧⌘E the workspace, as the Mac's buttons.
  const keys = useRef({ onTerminal, onPanel, onCard, terminalShown: folder.exists });
  keys.current = { onTerminal, onPanel, onCard, terminalShown: folder.exists };
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const k = keys.current;
      if (e.ctrlKey && !e.metaKey && !e.altKey && e.code === "Backquote" && k.terminalShown) {
        e.preventDefault();
        k.onTerminal();
      } else if (e.metaKey && e.shiftKey && !e.altKey && !e.ctrlKey && e.code === "KeyD") {
        e.preventDefault();
        k.onPanel();
      } else if (e.metaKey && e.shiftKey && !e.altKey && !e.ctrlKey && e.code === "KeyE") {
        e.preventDefault();
        k.onCard();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const removeWorktree = async (force: boolean) => {
    if (!worktree) return;
    try {
      await daemon.request({ type: "remove_worktree", path: session.cwd.slice(worktree.home.length + 1), ...(force ? { force: true } : {}) } as never)
        .then((reply) => {
          if (reply.type === "error") {
            const message = (reply.error ?? "Git couldn't do that.").replace(/^(daemon|workspace): /, "");
            throw new Error(message.charAt(0).toUpperCase() + message.slice(1));
          }
        });
    } catch (err) {
      const problem = err instanceof Error ? err.message : String(err);
      // Work or an agent would be lost: theirs to decide.
      if (!force && (problem.includes("uncommitted") || problem.includes("still working"))) {
        if ((await confirmSheet("Remove it anyway?", problem, ["Remove Anyway", "Cancel"])) === "Remove Anyway") void removeWorktree(true);
      } else {
        setActionError(session.id, problem);
      }
    }
  };

  const more = (button: HTMLElement) => {
    const entries: MenuEntry[] = [];
    if (folder.exists) {
      entries.push({ label: "Open in Editor", action: () => void openEditor({ folder: session.cwd }) });
      if (session.inject === "tmux") entries.push({ label: "Show the Agent's Own Terminal", action: () => !terminal && onTerminal() });
      if (folder.repository) {
        entries.push({ separator: true });
        const repo = worktree?.repo ?? (session.cwd.replace(/\/+$/, "").split("/").pop() || session.cwd);
        entries.push({ label: `New Worktree from ${repo}…`, action: () => go({ kind: "new", worktree: true, folder: session.cwd } as never) });
        if (worktree) {
          entries.push({
            label: "Remove This Worktree…",
            action: async () => {
              const home = worktree.home;
              const shown = session.cwd.startsWith(home) && /^\/(Users|home)\//.test(home) ? `~${session.cwd.slice(home.length)}` : session.cwd;
              const chosen = await confirmSheet(`Remove the ${worktree.name} worktree?`, `Its folder, ${shown}, goes. Its branch stays, with any commits on it.`, ["Remove Worktree", "Cancel"]);
              if (chosen === "Remove Worktree") void removeWorktree(false);
            },
          });
        }
      }
    }
    if (endable(session)) {
      entries.push({
        label: "End Session…",
        action: async () => {
          if ((await confirmSheet("End this session?", "", ["End session", "Cancel"])) !== "End session") return;
          try {
            await ask({ type: "end_session", sessionId: session.id }, "It couldn't be ended.");
          } catch (err) {
            setActionError(session.id, err instanceof Error ? err.message : String(err));
          }
        },
      });
    }
    menuUnder(button, entries);
  };

  return (
    <header className="detail-header drag">
      <ThreadTitle session={session} />
      <span className="header-spacer" />
      <div className="header-buttons no-drag">
        {servers.length > 0 && <ServersMenu session={session} servers={servers} />}
        {folder.exists && (
          <ChromeButton icon={SquareTerminal} sf="terminal" title={terminal ? "Close the terminal (⌃`)" : "Terminal in this folder (⌃`)"} active={terminal} onClick={onTerminal} />
        )}
        <ChromeButton sf="plus.forwardslash.minus" glyph={<PlusSlashMinus />} title={panel ? "Close the diff (⇧⌘D)" : "Diff and files (⇧⌘D)"} active={panel} onClick={onPanel} />
        <ChromeButton icon={PanelRight} sf="sidebar.right" title={card ? "Hide the workspace (⇧⌘E)" : "Files, changes and history (⇧⌘E)"} active={card} onClick={onCard} />
        {(folder.exists || endable(session)) && (
          <button className="ellipsis" aria-label="More" title="More" onClick={(e) => more(e.currentTarget)}>
            <Icon icon={Ellipsis} sf="ellipsis" size={13} />
          </button>
        )}
      </div>
    </header>
  );
}
