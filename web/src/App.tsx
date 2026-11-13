// Which window this page is: the main window, an editor window, or Settings,
// as the Mac app's scenes (App/AgentmanApp.swift). Each window is this same
// page, told which it is by its query (windows.go).

import { useEffect, useState } from "react";
import { EditorWindow } from "./EditorWindow";
import { platform } from "./host";
import { MainWindow } from "./MainWindow";
import { SettingsWindow } from "./settings/SettingsWindow";
import { SettingsSheet } from "./settings/SettingsSheet";
import "./nav";
import "./shell/native";
import "./notify/onScreen";

const view = new URLSearchParams(location.search).get("view");

export default function App() {
  const [os, setOs] = useState("");
  useEffect(() => {
    void platform().then(setOs);
  }, []);
  if (view === "settings") return <SettingsWindow />;
  // Settings over either, in a browser (settings/overPage.ts).
  return (
    <>
      {view === "editor" ? <EditorWindow /> : <MainWindow platform={os} />}
      <SettingsSheet />
    </>
  );
}
