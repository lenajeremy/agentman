// Copy, as the Mac app's Clipboard and ImageClipboard: words as text, a
// picture as PNG.

export async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    // Without the async clipboard (a page not in focus): the old way.
    const area = document.createElement("textarea");
    area.value = text;
    area.style.position = "fixed";
    area.style.opacity = "0";
    document.body.appendChild(area);
    area.select();
    document.execCommand("copy");
    area.remove();
  }
}

/** A picture, from its data: URL or any URL the page can read, as PNG. */
export async function copyImage(src: string) {
  try {
    const png = await toPng(src);
    await navigator.clipboard.write([new ClipboardItem({ "image/png": png })]);
  } catch {
    // Nothing the page can do: the picture stays where it is.
  }
}

async function toPng(src: string): Promise<Blob> {
  const blob = await (await fetch(src)).blob();
  if (blob.type === "image/png") return blob;
  const bitmap = await createImageBitmap(blob);
  const canvas = document.createElement("canvas");
  canvas.width = bitmap.width;
  canvas.height = bitmap.height;
  canvas.getContext("2d")!.drawImage(bitmap, 0, 0);
  return new Promise((resolve, reject) => canvas.toBlob((b) => (b ? resolve(b) : reject(new Error("no png"))), "image/png"));
}
