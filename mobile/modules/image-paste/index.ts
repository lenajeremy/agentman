import { NativeModule, requireOptionalNativeModule } from "expo";

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

/** Whether Paste in a text box can take an image on this platform. */
export const pasteTakesImages = native != null;

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
  return native?.addListener("onPasteImages", (event) => listener(event.images)) ?? null;
}
