import { NativeModule, requireOptionalNativeModule } from "expo";
import { Platform } from "react-native";

/** An image the system Paste command put into a text box, saved to a file. */
export interface PastedImage {
  uri: string;
  width: number;
  height: number;
}

type Events = {
  onPasteImages(event: { images: PastedImage[] }): void;
};

declare class ImagePasteModule extends NativeModule<Events> {}

// Optional because only iOS has it: Android's text box has no Paste for
// images to hook into, and there the composer's + menu still pastes one.
const native = requireOptionalNativeModule<ImagePasteModule>("ImagePaste");

// The web build hears the browser's own paste instead.
const web = Platform.OS === "web" && typeof document !== "undefined";

/** Whether Paste in a text box can take an image on this platform. */
export const pasteTakesImages = native != null || web;

/**
 * Listen for images pasted into a text box.
 *
 * While anything is listening, Paste with only an image on the clipboard
 * delivers it here instead of doing nothing, so listen only while the text box
 * meant to receive it is focused.
 */
export function addPastedImagesListener(
  listener: (images: PastedImage[]) => void,
): { remove(): void } | null {
  if (native) return native.addListener("onPasteImages", (event) => listener(event.images));
  if (!web) return null;
  const onPaste = (event: ClipboardEvent) => {
    const files = Array.from(event.clipboardData?.files ?? []).filter((file) => file.type.startsWith("image/"));
    if (files.length === 0) return;
    // An image, not its file name, goes in the box.
    event.preventDefault();
    void Promise.all(files.map(readPasted)).then((images) =>
      listener(images.filter((image): image is PastedImage => image !== null)),
    );
  };
  document.addEventListener("paste", onPaste);
  return { remove: () => document.removeEventListener("paste", onPaste) };
}

/** A pasted file as a data URL, with its size, as the native module gives. */
function readPasted(file: File): Promise<PastedImage | null> {
  return new Promise((resolve) => {
    const reader = new FileReader();
    reader.onerror = () => resolve(null);
    reader.onload = () => {
      const uri = String(reader.result ?? "");
      const image = new window.Image();
      image.onerror = () => resolve(null);
      image.onload = () => resolve({ uri, width: image.naturalWidth, height: image.naturalHeight });
      image.src = uri;
    };
    reader.readAsDataURL(file);
  });
}
