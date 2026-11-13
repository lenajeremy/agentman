// The tab strip, the path bar and the disk banner (EditorWindow.swift's
// TabStrip, EditorTab, PathBar and DiskBanner).

import { useRef } from "react";
import { ChevronRight, Eye, Pencil, Plus, TriangleAlert, X } from "lucide-react";
import { Icon } from "../ui";
import { contextMenu, menuUnder } from "../thread/menu";
import { reveal } from "../host";
import { PanelTabButton } from "../RightPanel";
import { tabTitle } from "../panel/state";
import { useStore } from "../store";
import type { EditorDocument } from "./document";
import { FileTypeIcon } from "./fileIcons";
import { copyText, paths } from "./fs";
import { folderAgent, MAX_PANES, type EditorStore, type WorkspaceTab } from "./store";

export function TabStrip({ store }: { store: EditorStore }) {
  const { sessions } = useStore();
  const plus = useRef<HTMLButtonElement>(null);
  // Two tabs with one name show their folders too.
  const counts = new Map<string, number>();
  for (const d of store.documents) counts.set(d.name, (counts.get(d.name) ?? 0) + 1);
  // The agent the Diff and History tabs are for: the one they're open on,
  // or the one in the panel, or the one working on this folder.
  const route = store.panelRoute;
  const agent = store.changesSession ?? (route?.kind === "agent" ? route.id : undefined) ?? folderAgent(store.root, sessions)?.id;
  return (
    <div
      className="tab-strip"
      onContextMenu={(e) => {
        const active = store.active;
        contextMenu(e, [
          { label: "Close All", action: () => store.closeAll() },
          { label: "Close Saved", action: () => store.closeSaved() },
          ...(active ? [{ label: "Close Others", action: () => store.closeOthers(active) }] : []),
        ]);
      }}
    >
      <div className="tab-scroll">
        {agent && (
          <div className="workspace-tabs">
            {store.workspaceTabs.map((tab) => (
              <PanelTabButton
                key={tab}
                tab={tab}
                selected={tab === store.workspaceTab}
                select={() => store.showWorkspace(tab, agent)}
                close={() => store.closeWorkspace(tab)}
              />
            ))}
            <button
              ref={plus}
              className="panel-plus"
              title="Open the agent's changes or history"
              onClick={() =>
                menuUnder(plus.current!, (["diff", "history"] as WorkspaceTab[]).map((tab) => ({ label: tabTitle[tab], action: () => store.showWorkspace(tab, agent) })))
              }
            >
              <Icon icon={Plus} sf="plus" size={11} weight="medium" />
            </button>
          </div>
        )}
        {agent && store.documents.length > 0 && <span className="workspace-tabs-rule" />}
        {store.documents.map((document) => (
          <EditorTab
            key={document.id}
            store={store}
            document={document}
            active={!store.showingChanges && document.id === store.activeID}
            folder={(counts.get(document.name) ?? 0) > 1 ? paths.dir(document.path) : null}
          />
        ))}
      </div>
    </div>
  );
}

function EditorTab({ store, document, active, folder }: { store: EditorStore; document: EditorDocument; active: boolean; folder: string | null }) {
  return (
    <div
      className={`tab ${active ? "active" : ""} ${document.isDirty ? "dirty" : ""}`}
      title={document.path}
      onClick={() => store.activate(document)}
      onContextMenu={(e) =>
        contextMenu(e, [
          { label: "Close", action: () => store.requestClose(document) },
          { label: "Close Others", action: () => store.closeOthers(document) },
          { label: "Close Saved", action: () => store.closeSaved() },
          { label: "Close All", action: () => store.closeAll() },
          { separator: true },
          { label: "Open to the Side", action: () => store.openToSide(document), disabled: store.panes.length >= MAX_PANES },
          { separator: true },
          { label: "Show in Finder", action: () => void reveal(document.url) },
          { label: "Copy Path", action: () => void copyText(document.url) },
          { label: "Copy Relative Path", action: () => void copyText(document.path) },
        ])
      }
    >
      <div className="tab-body">
        <FileTypeIcon name={document.name} size={13} />
        <span className="tab-name" style={{ textDecoration: document.deletedOnDisk ? "line-through" : undefined }}>
          {document.name}
        </span>
        {folder && <span className="tab-folder">{folder}</span>}
        <span className="tab-end">
          <span className="tab-dot" />
          <button
            className="tab-close"
            title="Close (⌘W)"
            onClick={(e) => {
              e.stopPropagation();
              store.requestClose(document);
            }}
          >
            <Icon icon={X} sf="xmark" size={8.5} weight="bold" />
          </button>
        </span>
      </div>
    </div>
  );
}

/** Where the file sits, and Save and Revert while it has changes. */
export function PathBar({ store, document }: { store: EditorStore; document: EditorDocument }) {
  const parts = document.readOnly
    ? [paths.tilde(paths.dir(document.url), store.home), document.name]
    : [store.name, ...document.path.split("/")];
  return (
    <div className="path-bar">
      {parts.map((part, index) => (
        <span key={index} className="path-part">
          {index > 0 && (
            <span className="path-chevron">
              <Icon icon={ChevronRight} sf="chevron.right" size={7.5} weight="semibold" />
            </span>
          )}
          <span className={index === parts.length - 1 ? "path-last" : "path-crumb"}>{part}</span>
        </span>
      ))}
      <span className="path-spacer" />
      {document.isMarkdown && (
        <button
          className="path-preview"
          title={document.previewing ? "Back to the text (⇧⌘V)" : "Read it rendered (⇧⌘V)"}
          onClick={() => store.togglePreview()}
        >
          <Icon icon={document.previewing ? Pencil : Eye} sf={document.previewing ? "pencil" : "eye"} size={12} />
          <span>{document.previewing ? "Edit" : "Preview"}</span>
        </button>
      )}
      {document.readOnly && (
        <span className="path-readonly" title="A file from outside the folder, opened to read">Read only</span>
      )}
      {document.isDirty && (
        <>
          <button className="path-revert" title="Back to the saved file" onClick={() => document.revert()}>Revert</button>
          <button className="path-save" title="Save (⌘S)" onClick={() => void document.save()}>Save</button>
        </>
      )}
    </div>
  );
}

/** Says when the file changed or was deleted on disk while it had unsaved
 *  edits, and what can be done about it. */
export function DiskBanner({ document, close }: { document: EditorDocument; close: () => void }) {
  if (!document.changedOnDisk && !document.deletedOnDisk) return null;
  return (
    <div className="disk-banner">
      <span className="disk-icon">
        <Icon icon={TriangleAlert} sf="exclamationmark.triangle.fill" size={13} />
      </span>
      <span className="disk-text">
        {document.deletedOnDisk
          ? "This file was deleted on disk. Save to put it back."
          : "This file changed on disk while you were editing it."}
      </span>
      <span style={{ flex: 1 }} />
      {document.deletedOnDisk ? (
        <button className="mac-button small" onClick={close}>Close</button>
      ) : (
        <>
          <button className="mac-button small" title="Keep your edits; saving writes over the version on disk" onClick={() => void document.keepMine()}>
            Keep Mine
          </button>
          <button className="mac-button small prominent" onClick={() => document.reloadFromDisk()}>Use the Disk Version</button>
        </>
      )}
    </div>
  );
}
