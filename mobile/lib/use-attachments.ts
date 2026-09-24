import { useCallback, useEffect, useRef, useState } from "react";
import * as Clipboard from "expo-clipboard";

import { canAttachMore, MAX_ATTACHMENTS } from "./attachments";
import { clipboardHasImage, pasteImage, pickImages } from "./imagesource";
import { uploadImage } from "./upload";
import type { Credentials } from "./client";

export interface Attachment {
  key: string;
  /** Local uri for the thumbnail. */
  uri: string;
  base64: string;
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
    if (attachments.length === 0) return [];
    if (!credentials) throw new Error("Not paired with a Mac.");
    const ids: string[] = [];
    for (const item of attachments) {
      ids.push(await uploadImage(credentials.relayUrl, credentials.token, item.base64));
    }
    return ids;
  }, [attachments, credentials]);

  return {
    attachments,
    busy,
    error,
    clipboardReady,
    canAdd: canAttachMore(attachments.length),
    pick,
    paste,
    remove,
    clear,
    upload,
    setError,
  };
}
