// The editor's status bar (EditorWindow.swift's StatusBar, VimModeLabel,
// ProblemCount and LanguageLabel): branch and changes on the left; the
// cursor, the file's settings and its language server on the right.

import { ChartSpline, CircleX, GitBranch, TriangleAlert } from "lucide-react";
import { Icon } from "../ui";
import type { EditorDocument } from "./document";
import type { EditorStore } from "./store";

export function StatusBar({ store }: { store: EditorStore }) {
  const document = store.active;
  const changed = Object.keys(store.marks).length;
  return (
    <div className="status-bar">
      {store.vimMode && <VimModeLabel mode={store.vimMode} keys={store.vimKeys} />}
      {store.notice && <span className="status-notice">{store.notice}</span>}
      {store.branch && (
        <span className="status-label">
          <Icon icon={GitBranch} sf="arrow.triangle.branch" size={11} />
          <span>{store.branch}</span>
        </span>
      )}
      {changed > 0 && <span>{changed} changed</span>}
      <span className="status-spacer" />
      {/* What the agents spent and what's left of each plan, over the editor. */}
      <button
        className={`status-usage ${store.showUsage ? "open" : ""}`}
        title="What your agents spent, and what's left of each plan"
        // Clicked, it leaves the keyboard where it is, for the popover to
        // give back when it closes.
        onMouseDown={(e) => e.preventDefault()}
        onClick={() => {
          store.showUsage = !store.showUsage;
          store.changed();
        }}
      >
        <Icon icon={ChartSpline} sf="chart.xyaxis.line" size={11} />
        <span>Usage</span>
      </button>
      {document && (
        <>
          {document.kind === "text" && (
            <>
              <ProblemCount store={store} document={document} />
              <span>Ln {document.line}, Col {document.column}</span>
              <span>{document.indent === "\t" ? "Tabs" : `Spaces: ${document.indent.length}`}</span>
              <span>UTF-8</span>
              <span>{document.crlf ? "CRLF" : "LF"}</span>
            </>
          )}
          <LanguageLabel store={store} document={document} />
        </>
      )}
    </div>
  );
}

/** Vim's mode, tinted by kind, then the keys of a command being typed. */
function VimModeLabel({ mode, keys }: { mode: string; keys: string }) {
  const tint =
    mode === "insert" ? "var(--working-text)" : mode === "replace" ? "var(--error-text)" : mode.startsWith("visual") ? "var(--needs-you-text)" : "var(--text-2)";
  return (
    <span className="vim-label" title="Vim mode. Turn Vim off in the Editor menu." aria-label={`Vim ${mode} mode`}>
      <span className="vim-mode" style={{ color: tint }}>
        <span className="vim-mode-wash" style={{ background: tint }} />
        {mode.toUpperCase()}
      </span>
      {keys && <span className="vim-keys">{keys}</span>}
    </span>
  );
}

/** The language server's errors and warnings; a click goes to the next. */
function ProblemCount({ store, document }: { store: EditorStore; document: EditorDocument }) {
  const { errors, warnings } = document.problems;
  if (errors + warnings === 0) return null;
  return (
    <button className="status-problems" title="Go to the next problem (F8)" onClick={() => store.monaco?.run("editor.action.marker.next")}>
      {errors > 0 && (
        <span className="status-label" style={{ color: "var(--error-text)" }}>
          <Icon icon={CircleX} sf="xmark.circle" size={11} />
          <span>{errors}</span>
        </span>
      )}
      {warnings > 0 && (
        <span className="status-label" style={{ color: "var(--needs-you-text)" }}>
          <Icon icon={TriangleAlert} sf="exclamationmark.triangle" size={11} />
          <span>{warnings}</span>
        </span>
      )}
    </button>
  );
}

/** The file's language, and how its language server is doing. */
function LanguageLabel({ store, document }: { store: EditorStore; document: EditorDocument }) {
  const status = document.language;
  let lead = null;
  if (status) {
    switch (status.state) {
      case "installing":
      case "starting":
      case "restarting":
        lead = (
          <>
            <span className="status-spinner" />
            <span className="status-detail">{status.detail || `Starting ${status.name}…`}</span>
          </>
        );
        break;
      case "failed":
        lead = (
          <button
            className="status-label status-failed"
            title={`${status.detail}\nClick to try again.`}
            onClick={() => store.monaco?.restartLanguage(document.id)}
          >
            <Icon icon={TriangleAlert} sf="exclamationmark.triangle" size={11} />
            <span className="status-detail">{status.detail}</span>
          </button>
        );
        break;
      default:
        if (status.detail) lead = <span className="status-detail" style={{ maxWidth: 280 }} title={status.detail}>{status.detail}</span>;
    }
  }
  return (
    <span className="status-language">
      {lead}
      <span title={status?.state === "ready" ? `${status.name} is running` : ""}>{document.languageName}</span>
      {status?.state === "ready" && <span className="status-ready" />}
    </span>
  );
}
