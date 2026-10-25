/**
 * The images that went with a message the user sent.
 *
 * An image sent to an agent is saved on the Mac and the agent is given its
 * path, typed ahead of the words. The conversation shows the picture where
 * that path would be. Only what the daemon filed counts: a path into its own
 * images folder, to a picture; anything else a message starts with stays
 * words. The Mac app applies the same rule.
 */
const KEPT_IMAGE =
  /^\/\S*\/\.agentman\/images\/[^\s/]+\/[^\s/]+\.(png|jpe?g|gif|webp)$/i;

export interface SentMessageParts {
  images: string[];
  text: string;
}

export function splitSentMessage(message: string): SentMessageParts {
  const images: string[] = [];
  let rest = message;
  for (;;) {
    const space = rest.indexOf(" ");
    const token = space === -1 ? rest : rest.slice(0, space);
    if (!token || token.includes("..") || !KEPT_IMAGE.test(token)) break;
    images.push(token);
    rest = space === -1 ? "" : rest.slice(space).replace(/^ +/, "");
  }
  return { images, text: rest };
}
