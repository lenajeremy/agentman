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

/**
 * A file sent with a message is named ahead of the words as @ and its path
 * on the Mac: one Agentman kept, sent from the phone, or the user's own,
 * attached at the Mac. The conversation shows its name instead. Only an
 * absolute path to something with an extension counts: "@docs/a.md" in a
 * sentence stays words.
 */
const SENT_FILE = /^@(\/\S*\/[^\s/]+\.[A-Za-z0-9]{1,12})$/;

export interface SentMessageParts {
  images: string[];
  /** Files sent with it, as their paths on the Mac. */
  files: string[];
  text: string;
}

export function splitSentMessage(message: string): SentMessageParts {
  const images: string[] = [];
  const files: string[] = [];
  let rest = message;
  for (;;) {
    const token = /^\S+/.exec(rest)?.[0];
    if (!token || token.includes("..")) break;
    const file = SENT_FILE.exec(token);
    if (file) files.push(file[1]);
    else if (KEPT_IMAGE.test(token)) images.push(token);
    else break;
    rest = rest.slice(token.length).replace(/^\s+/, "");
  }
  return { images, files, text: rest };
}

/**
 * A message as the conversation shows it: the pictures attached to it, from
 * the agent's own record of the message, then any its words lead with. A
 * picture pasted into the agent's terminal sits wherever the agent kept it,
 * so only that record can say it is one.
 */
export function sentMessageParts(text: string, attached: readonly string[] = []): SentMessageParts {
  const parts = splitSentMessage(text);
  if (attached.length === 0) return parts;
  return { ...parts, images: [...attached, ...parts.images.filter((path) => !attached.includes(path))] };
}
