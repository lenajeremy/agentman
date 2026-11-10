import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { daemon } from "./daemon";
import "./styles/base.css";

daemon.start();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
