// A picture the daemon filed for a message, under its own images folder
// (SentMessage.isKeptImage): typed as its path ahead of the words it went
// with.

export function isKeptImage(token: string): boolean {
  return token.startsWith("/") && token.includes("/.agentman/images/") && !token.includes("..") &&
    ["png", "jpg", "jpeg", "gif", "webp"].includes(token.split(".").at(-1)?.toLowerCase() ?? "");
}
