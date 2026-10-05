/**
 * Requests that wait on a reply from the Mac — a file read, a piece of an
 * image download, a resume, a folder's list — each carry a timer, so a reply
 * that never comes ends in an error rather than a spinner forever. When the
 * Mac goes offline no reply can come, and waiting out a 30-second timer only
 * froze the screen: a download stopped at its percentage, a folder spun.
 * failPending answers them all at once instead.
 */
export function failPending(
  pending: Map<string, { reject(error: Error): void; timer: ReturnType<typeof setTimeout> }>,
  message: string,
  cancel: (id: string) => void = () => {},
): void {
  for (const [id, request] of pending) {
    clearTimeout(request.timer);
    cancel(id);
    request.reject(new Error(message));
  }
  pending.clear();
}
