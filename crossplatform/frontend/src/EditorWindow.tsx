// An editor window (Editor/EditorWindow.swift): one per folder, opened by
// windows.go with ?view=editor&folder=…[&session=…][&file=…][&terminal=1].

import { Editor } from "./Editor";
import { folderLabel } from "./labels";
import { useStore } from "./store";

const query = new URLSearchParams(location.search);

export function EditorWindow() {
  const { sessions } = useStore();
  const folder = query.get("folder");
  const session = sessions.find((s) => s.id === query.get("session")) ?? sessions.find((s) => s.cwd === folder) ?? sessions[0];
  if (!session) return <div className="empty big">Connecting to am serve…</div>;
  return (
    <div className="editor-window">
      <header className="detail-header drag" style={{ paddingLeft: document.documentElement.dataset.platform === "darwin" ? 82 : 18 }}>
        <div className="crumbs"><span className="crumb-name">{folderLabel(folder ?? session.cwd)}</span></div>
      </header>
      <div className="editor-body"><Editor session={session} /></div>
    </div>
  );
}
