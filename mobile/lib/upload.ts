/**
 * Handing an image to the relay.
 *
 * Kept apart from imagesource.ts, which pulls in the picker and the clipboard:
 * this is the part with a failure mode worth testing, and it needs nothing but
 * fetch to do it.
 */

/**
 * Hand one image to the relay and return the ticket for it.
 *
 * Sent as raw bytes with no JSON wrapper: base64 in a JSON body would inflate
 * it by a third, and the relay identifies the image from its own bytes rather
 * than from anything declared here.
 */
export async function uploadImage(
  relayUrl: string,
  token: string,
  base64: string,
  signal?: AbortSignal,
): Promise<string> {
  const response = await fetch(`${relayUrl.replace(/\/+$/, "")}/upload`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/octet-stream",
    },
    // React Native's fetch types come from lib.dom, which does not list a
    // typed array in BodyInit — but its own convertRequestBody handles
    // ArrayBuffer and ArrayBufferView explicitly, so this is a typing gap
    // rather than an unsupported body.
    body: decode(base64) as unknown as BodyInit,
    signal,
  });
  if (!response.ok) {
    const detail = await response.json().catch(() => null);
    throw new Error(detail?.error ?? "That image could not be sent.");
  }
  const body = await response.json();
  if (typeof body?.id !== "string" || body.id === "") {
    throw new Error("The relay did not return an upload id.");
  }
  return body.id;
}

/** base64 to bytes, without pulling in a polyfill for Buffer. */
function decode(base64: string): Uint8Array {
  const binary = globalThis.atob(base64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}
