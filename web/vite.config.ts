import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import wails from "@wailsio/runtime/plugins/vite";
import { readFile } from "node:fs/promises";
import { homedir } from "node:os";
import { join, resolve } from "node:path";

// The phone app's TypeScript that has no React Native in it (the protocol,
// markdown, parsers) is shared, not copied. Its bare imports (marked)
// resolve from here, as mobile/node_modules may not be installed.
const shared = resolve(__dirname, "../mobile/lib");
const components = resolve(__dirname, "../mobile/components");

// The phone's agent marks name their one picture (Antigravity's) with
// React Native's require(); here it becomes an asset URL.
function nativeRequire(): Plugin {
  return {
    name: "agentman-native-require",
    transform(code, id) {
      if (!id.endsWith("agentMarks.ts")) return null;
      return code.replace(/require\(("[^"]+")\)/g, "new URL($1, import.meta.url).href");
    },
  };
}

// In a browser, with no Wails shell to ask, the page reads am serve's app
// endpoint from here: the screens can be built and tested in Chrome against
// a test daemon (AGENTMAN_HOME is the home it was given with -config-home).
function devEndpoint(): Plugin {
  return {
    name: "agentman-dev-endpoint",
    configureServer(server) {
      server.middlewares.use("/__agentman/endpoint", async (_req, res) => {
        const home = process.env.AGENTMAN_HOME || homedir();
        try {
          res.setHeader("Content-Type", "application/json");
          res.end(await readFile(join(home, ".agentman", "app.json")));
        } catch {
          res.statusCode = 503;
          res.end(JSON.stringify({ error: "am serve isn't running" }));
        }
      });
    },
  };
}

export default defineConfig({
  // One tsconfig for every file, the shared ones too: mobile/'s own extends
  // Expo's, which isn't installed here.
  tsconfig: resolve(__dirname, "tsconfig.json"),
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
    fs: { allow: [resolve(__dirname, ".."), shared, components, resolve(__dirname, "../mobile/assets")] },
  },
  resolve: {
    alias: {
      "@shared": shared,
      "@marks": resolve(components, "agentMarks.ts"),
      marked: resolve(__dirname, "node_modules/marked"),
    },
  },
  plugins: [react(), wails("./bindings"), devEndpoint(), nativeRequire()],
});
