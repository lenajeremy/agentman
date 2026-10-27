import { useCallback, useEffect, useRef, useState } from "react";
import * as Clipboard from "expo-clipboard";
import * as DocumentPicker from "expo-document-picker";
import { File } from "expo-file-system";

import { canAttachMore, MAX_ATTACHMENTS } from "./attachments";
import { MAX_FILE_BYTES, sendFile } from "./files";
import type { DaemonEvent, Request } from "./protocol";
import { clipboardHasImage, pasteImage, pickImages, preparePasted } from "./imagesource";
import { uploadImage } from "./upload";
import type { Credentials } from "./client";

export interface Attachment {
  key: string;
  /** Local uri for the thumbnail, or of the file. */
  uri: string;
  /** A picture's, read when it was chosen; a file's is read on sending. */
  base64: string;
  /** A picture goes through the relay's upload locker and is shown as one;
   *  any other file goes to the Mac in parts and is named in the message. */
  kind?: "image" | "file";
  /** A file's name and size, for its chip. */
  name?: string;
  size?: number;
}

let sequence = 0;

/**
 * Images waiting to go with the next message.
 *
 * They are held on the phone until send, not uploaded on attach. The relay
 * only keeps an upload for two minutes, and the gap between choosing a
 * screenshot and finishing the sentence about it is easily longer than that.
 */
export function useAttachments(credentials: Credentials | null) {
  const [attachments, setAttachments] = useState<Attachment[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [clipboardReady, setClipboardReady] = useState(false);
  const alive = useRef(true);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  const refreshClipboard = useCallback(() => {
    void clipboardHasImage().then((has) => {
      if (alive.current) setClipboardReady(has);
    });
  }, []);

  // Copying a screenshot happens in another app, so the offer to paste it has
  // to appear without the composer being touched.
  useEffect(() => {
    refreshClipboard();
    const subscription = Clipboard.addClipboardListener(() => refreshClipboard());
    return () => Clipboard.removeClipboardListener(subscription);
  }, [refreshClipboard]);

  const add = useCallback(async (load: () => Promise<Attachment[]>) => {
    setError("");
    setBusy(true);
    try {
      const next = await load();
      if (!alive.current || next.length === 0) return;
      setAttachments((current) => [...current, ...next].slice(0, MAX_ATTACHMENTS));
    } catch (reason) {
      if (alive.current) setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      if (alive.current) setBusy(false);
    }
  }, []);

  const pick = useCallback(
    () =>
      add(async () => {
        const remaining = MAX_ATTACHMENTS - attachments.length;
        if (remaining <= 0) return [];
        const picked = await pickImages(remaining);
        return picked.map((image) => ({
          key: `a${++sequence}`,
          uri: image.uri,
          base64: image.base64,
        }));
      }),
    [add, attachments.length],
  );

  /** Any file, from Files or another app's documents. */
  const pickFile = useCallback(
    () =>
      add(async () => {
        const remaining = MAX_ATTACHMENTS - attachments.length;
        if (remaining <= 0) return [];
        const result = await DocumentPicker.getDocumentAsync({ multiple: true, copyToCacheDirectory: true });
        if (result.canceled) return [];
        const chosen = result.assets.slice(0, remaining);
        const large = chosen.find((asset) => (asset.size ?? 0) > MAX_FILE_BYTES);
        if (large) throw new Error(`${large.name} is larger than 10 MB, the most your Mac takes.`);
        return chosen.map((asset) => ({
          key: `a${++sequence}`,
          uri: asset.uri,
          base64: "",
          kind: "file" as const,
          name: asset.name,
          size: asset.size,
        }));
      }),
    [add, attachments.length],
  );

  const paste = useCallback(
    () =>
      add(async () => {
        if (!canAttachMore(attachments.length)) return [];
        const image = await pasteImage();
        if (!image) {
          throw new Error("There is no image on the clipboard.");
        }
        return [{ key: `a${++sequence}`, uri: image.uri, base64: image.base64 }];
      }),
    [add, attachments.length],
  );

  /** Images the text box's own Paste handed over, already on disk. */
  const addPasted = useCallback(
    (images: { uri: string; width: number; height: number }[]) =>
      add(async () => {
        const remaining = MAX_ATTACHMENTS - attachments.length;
        if (remaining <= 0) return [];
        const prepared = await preparePasted(images.slice(0, remaining));
        return prepared.map((image) => ({
          key: `a${++sequence}`,
          uri: image.uri,
          base64: image.base64,
        }));
      }),
    [add, attachments.length],
  );

  const remove = useCallback((key: string) => {
    setAttachments((current) => current.filter((item) => item.key !== key));
  }, []);

  const clear = useCallback(() => {
    setAttachments([]);
    setError("");
  }, []);

  /**
   * Hand every attachment to the relay and return their tickets, in order.
   *
   * Sequential rather than parallel: the relay caps what one account may hold
   * at once, and a burst that trips that cap would fail a send the user could
   * have completed by waiting a moment.
   */
  const upload = useCallback(async (): Promise<string[]> => {
    const images = attachments.filter((item) => item.kind !== "file");
    if (images.length === 0) return [];
    if (!credentials) throw new Error("Not paired with a Mac.");
    const ids: string[] = [];
    for (const item of images) {
      ids.push(await uploadImage(credentials.relayUrl, credentials.token, item.base64));
    }
    return ids;
  }, [attachments, credentials]);

  /** Send every file to the Mac, in order, and return where each now is. */
  const sendFiles = useCallback(
    async (ask: (request: Request, timeoutMs?: number) => Promise<DaemonEvent>, sessionId: string): Promise<string[]> => {
      const paths: string[] = [];
      for (const item of attachments) {
        if (item.kind !== "file") continue;
        const base64 = await new File(item.uri).base64();
        paths.push(await sendFile(ask, sessionId, item.key.replace(/[^A-Za-z0-9-]/g, "") + Date.now(), item.name ?? "file", base64));
      }
      return paths;
    },
    [attachments],
  );

  return {
    attachments,
    busy,
    error,
    clipboardReady,
    canAdd: canAttachMore(attachments.length),
    pick,
    pickFile,
    paste,
    addPasted,
    remove,
    clear,
    upload,
    sendFiles,
    setError,
  };
}
