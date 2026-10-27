/**
 * What a document's pictures from the web need besides an Image: SVG, which
 * an Image can't draw, is what badges are made of (shields.io and the like),
 * so it is fetched as text and drawn with react-native-svg at its own shape.
 *
 * Pure, so it is tested on Node like the other rules in this directory.
 */

/** Badges are small; anything this long is not one, and is left as a name. */
export const MAX_SVG_CHARS = 512 * 1024;

/** An address that serves SVG, by its name or by who serves it. */
export function looksLikeSvg(url: string): boolean {
  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    return false;
  }
  return (
    /\.svg$/i.test(parsed.pathname) ||
    ["img.shields.io", "badgen.net", "badge.fury.io", "codecov.io", "app.codecov.io"].includes(parsed.hostname) ||
    /\/badge(\.svg)?$/i.test(parsed.pathname)
  );
}

/**
 * An SVG's size, from its root element: width and height, else its viewBox;
 * null for text that isn't one, or has neither.
 */
export function svgSize(xml: string): { width: number; height: number } | null {
  if (xml.length > MAX_SVG_CHARS) return null;
  const root = /<svg\b[^>]*>/i.exec(xml)?.[0];
  if (!root) return null;
  const number = (name: string) => {
    const value = new RegExp(`\\s${name}\\s*=\\s*["']\\s*([0-9.]+)(px)?\\s*["']`, "i").exec(root)?.[1];
    const parsed = value ? Number.parseFloat(value) : NaN;
    return Number.isFinite(parsed) && parsed > 0 ? parsed : null;
  };
  const width = number("width");
  const height = number("height");
  if (width && height) return { width, height };
  const box = /\sviewBox\s*=\s*["']([^"']+)["']/i.exec(root)?.[1]?.trim().split(/[\s,]+/).map(Number);
  if (box && box.length === 4 && box[2] > 0 && box[3] > 0) return { width: box[2], height: box[3] };
  return null;
}
