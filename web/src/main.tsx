import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { daemon } from "./daemon";
import { serverSystem } from "./host";
import "./styles/base.css";
import "./settings/appearance";
import "./shell/keepEnded";

daemon.start();
// Asked before the first draw, so the words that depend on it ("This Mac",
// Finder, where a key is kept) are right from their first showing: a call
// to am serve on this computer, waited for half a second at most.
void Promise.race([serverSystem(), new Promise((done) => setTimeout(done, 500))]).then(() => {
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <App />
    </StrictMode>,
  );
});
