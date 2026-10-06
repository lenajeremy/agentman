#!/usr/bin/env node
// Runs the `am` binary built for this machine.
//
// npm installs exactly one of the agentman-<os>-<cpu> packages, the one whose
// "os" and "cpu" match, because they are optional dependencies of this one.
// This file finds it and hands over the terminal: same arguments, same stdin,
// stdout and stderr, same exit status.
"use strict";

const { spawn } = require("node:child_process");

const PACKAGES = {
  "darwin arm64": "agentman-darwin-arm64",
  "darwin x64": "agentman-darwin-x64",
  "linux arm64": "agentman-linux-arm64",
  "linux x64": "agentman-linux-x64",
};

const name = PACKAGES[`${process.platform} ${process.arch}`];
if (!name) {
  console.error(`agentman: there is no build for ${process.platform} on ${process.arch}. It runs on macOS and Linux, on x64 and arm64.`);
  process.exit(1);
}

let binary;
try {
  binary = require.resolve(`${name}/bin/am`);
} catch {
  console.error(
    `agentman: ${name}, the part of agentman built for this machine, is not installed.\n` +
      "That happens when optional dependencies are skipped (--omit=optional or --no-optional).\n" +
      "Reinstall without that flag: npm install -g agentman",
  );
  process.exit(1);
}

const child = spawn(binary, process.argv.slice(2), { stdio: "inherit" });

// Ctrl-C reaches `am` directly, since it shares this terminal; this process
// only needs to outlive it. A signal sent to this process alone, by a process
// manager or `kill`, is passed on so `am serve` can shut down cleanly.
process.on("SIGINT", () => {});
for (const signal of ["SIGTERM", "SIGHUP"]) {
  process.on(signal, () => child.kill(signal));
}

child.on("error", (error) => {
  console.error(`agentman: could not start ${binary}: ${error.message}`);
  process.exit(1);
});
child.on("exit", (code, signal) => {
  if (signal) {
    process.removeAllListeners(signal);
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code ?? 1);
});
