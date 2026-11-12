import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { daemon } from "./daemon";
import { serverSystem } from "./host";
import "./styles/base.css";
import "./settings/appearance";
import "./shell/keepEnded";

daemon.start();
// Asked now, so the words that depend on it (where a key is kept) are right
// from their first showing.
void serverSystem();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
