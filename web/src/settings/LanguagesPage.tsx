// Languages (Views/LanguagesSettings.swift): the language servers behind
// the editor's errors, completions and Go to Definition: where each comes
// from, what am downloaded, and the user's say over them. Kept by am serve,
// which runs them.

import { useEffect, useState } from "react";
import { Ellipsis } from "lucide-react";
import { menuUnder, type MenuEntry } from "../thread/menu";
import { Icon } from "../ui";
import { PushButton, Spinner, Toggle, lastPart, tilde } from "./controls";
import { Row, Section, useTestScroll } from "./layout";
import { home, pick } from "./native";
import { languages, type LanguageChange, type LanguageServer, type LanguagesState } from "./state";

/** Bytes as the Mac's ByteCountFormatter (.file) writes them: "30.2 MB". */
export function size(bytes: number): string {
  if (bytes < 1000) return bytes === 1 ? "1 byte" : `${bytes} bytes`;
  const units = ["KB", "MB", "GB", "TB"];
  let value = bytes / 1000;
  let unit = 0;
  while (value >= 1000 && unit < units.length - 1) {
    value /= 1000;
    unit++;
  }
  const digits = unit === 0 ? 0 : 1;
  const text = value.toFixed(digits).replace(/\.0$/, "");
  return `${text} ${units[unit]}`;
}

/** Where a server comes from, in a few words. */
export function detail(server: LanguageServer): string {
  // The title is already the language; a server named the same says nothing more.
  const parts = server.name === server.language ? [] : [server.name];
  switch (server.source) {
    case "yours":
      parts.push("your own: " + tilde(server.path ?? "", home()));
      break;
    case "mac":
      parts.push("on this Mac");
      break;
    case "downloaded":
      parts.push("downloaded" + (server.size !== undefined ? " · " + size(server.size) : ""));
      break;
    case "download":
      parts.push("downloads when first needed" + (server.downloadSize ? ", " + server.downloadSize : ""));
      break;
    case "off":
      parts.push("off");
      break;
    default:
      parts.push(server.missing ?? "not on this Mac");
  }
  if (server.running?.length) parts.push("running for " + server.running.map(lastPart).join(", "));
  return parts.join(" · ");
}

export function LanguagesPage() {
  const [state, setState] = useState<LanguagesState>();
  const [problem, setProblem] = useState<string>();
  const scroll = useTestScroll();

  const apply = (reply: { state?: LanguagesState; problem?: string }) => {
    if (reply.state) {
      setState(reply.state);
      setProblem(undefined);
    } else {
      setProblem(reply.problem);
    }
  };
  useEffect(() => {
    void languages("lsp_status").then(apply);
  }, []);
  const change = (c: LanguageChange) => void languages("lsp_set", c).then(apply);
  const remove = (id: string) => void languages("lsp_remove", { server: id }).then(apply);

  const choose = async (server: LanguageServer) => {
    const start = server.path ? server.path.replace(/\/[^/]*$/, "") : "/opt/homebrew/bin";
    const paths = await pick({ folders: false, start, message: `Choose the program that runs ${server.name}`, button: "Use" });
    if (paths[0]) change({ server: server.id, path: paths[0] });
  };

  const menu = (server: LanguageServer): MenuEntry[] => [
    { label: "Use My Own Program…", action: () => void choose(server) },
    ...(server.source === "yours" ? [{ label: "Use the Usual One", action: () => change({ server: server.id, path: "" }) }] : []),
    { separator: true },
    server.source === "off"
      ? { label: "Turn On", action: () => change({ server: server.id, disabled: false }) }
      : { label: "Turn Off", action: () => change({ server: server.id, disabled: true }) },
  ];

  return (
    <div className="settings-scroll" ref={scroll}>
      <div className="settings-page tight">
        <div className="page-head-text">
          <h1 className="settings-display">Languages</h1>
          <div className="page-intro">
            Errors, completions and Go to Definition in the editor come from language servers that am serve runs on this Mac. Each starts when a file of its language
            opens, and stops when no window has used it for ten minutes.
          </div>
        </div>

        {state ? (
          <>
            <Section>
              <Row
                title="Download servers when needed"
                detail="The first file of a language gets its server, once. Off, only servers already on this Mac run."
                divider={false}
              >
                <Toggle on={!state.noDownloads} onChange={(on) => change({ noDownloads: !on })} />
              </Row>
            </Section>
            <Section title="Servers">
              {state.servers.map((server, index) => (
                <Row key={server.id} title={server.language} detail={detail(server)} divider={index < state.servers.length - 1} help={server.path ?? ""}>
                  <div className="hstack" style={{ gap: 10 }}>
                    {server.source === "downloaded" && <PushButton onClick={() => remove(server.id)}>Remove</PushButton>}
                    <button className="ellipsis-menu" aria-label="More" onClick={(e) => menuUnder(e.currentTarget, menu(server))}>
                      <Icon icon={Ellipsis} sf="ellipsis.circle" size={13} />
                    </button>
                  </div>
                </Row>
              ))}
            </Section>
            {!!state.nodeSize && state.nodeSize > 0 && (
              <Section>
                <Row title="Node.js" detail={`Downloaded to run the TypeScript and Python servers · ${size(state.nodeSize)}`} divider={false}>
                  <PushButton onClick={() => remove("node")}>Remove</PushButton>
                </Row>
              </Section>
            )}
          </>
        ) : (
          !problem && (
            <div className="languages-loading">
              <Spinner />
            </div>
          )
        )}

        {problem && <div className="caption needs-you-text languages-problem">{problem}</div>}
      </div>
    </div>
  );
}
