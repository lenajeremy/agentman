/**
 * Where a held message and its menu go on screen.
 *
 * Holding a message lifts it out of the conversation — the rest blurs back —
 * and attaches a menu to it. That is how Messages, Telegram, WhatsApp and the
 * rest do it, and the part that is easy to get wrong is placement: a message
 * near the bottom has no room for a menu below it, and a long one may not fit
 * on screen beside a menu at all. The arithmetic lives here, apart from the
 * views, so those cases are tested rather than discovered on a phone.
 */

export interface Frame {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface MenuLayout {
  /** Where the lifted message is drawn. Same x and width as it had. */
  bubble: Frame;
  /** True when the message was too tall to show whole beside the menu. */
  clipped: boolean;
  menu: { x: number; y: number; width: number; height: number };
  /** Which side of the message the menu opened on, so it grows from there. */
  placement: "below" | "above";
}

/** Space kept clear of the screen's edges and between message and menu. */
export const MENU_GAP = 8;
export const MENU_EDGE = 12;
export const MENU_WIDTH = 216;
export const MENU_ROW_HEIGHT = 46;

/** A menu's height for a number of rows, including its border. */
export function menuHeight(rows: number): number {
  return rows * MENU_ROW_HEIGHT + 2;
}

/**
 * Place a held message and its menu.
 *
 * The message stays exactly where it was whenever it can, because a message
 * that jumps when held reads as the app losing your place. So the menu goes
 * below if there is room, above if there is not, and only when neither works
 * does the message itself move — and only when it still cannot fit is it
 * clipped, top kept, so its opening line is what you see.
 */
export function layoutMessageMenu(options: {
  anchor: Frame;
  rows: number;
  screen: { width: number; height: number };
  insets: { top: number; bottom: number };
  /** Which edge the menu lines up with: a sent message sits on the right. */
  align?: "left" | "right";
}): MenuLayout {
  const { anchor, rows, screen, insets, align = "right" } = options;
  const height = menuHeight(rows);
  const width = Math.min(MENU_WIDTH, screen.width - MENU_EDGE * 2);
  const top = insets.top + MENU_EDGE;
  const bottom = screen.height - insets.bottom - MENU_EDGE;

  const menuX = clamp(
    align === "right" ? anchor.x + anchor.width - width : anchor.x,
    MENU_EDGE,
    screen.width - MENU_EDGE - width,
  );
  const menu = (y: number) => ({ x: menuX, y, width, height });

  // Where it is, with room below.
  const belowY = anchor.y + anchor.height + MENU_GAP;
  if (anchor.y >= top && belowY + height <= bottom) {
    return { bubble: anchor, clipped: false, menu: menu(belowY), placement: "below" };
  }
  // Where it is, with room above.
  const aboveY = anchor.y - MENU_GAP - height;
  if (aboveY >= top && anchor.y + anchor.height <= bottom) {
    return { bubble: anchor, clipped: false, menu: menu(aboveY), placement: "above" };
  }

  // Neither: the message has to move. Keep the pair together, menu below,
  // and slide it as little as possible to fit between top and bottom.
  const room = bottom - top - MENU_GAP - height;
  const bubbleHeight = Math.max(0, Math.min(anchor.height, room));
  const bubbleY = clamp(anchor.y, top, bottom - MENU_GAP - height - bubbleHeight);
  return {
    bubble: { x: anchor.x, y: bubbleY, width: anchor.width, height: bubbleHeight },
    clipped: bubbleHeight < anchor.height,
    menu: menu(bubbleY + bubbleHeight + MENU_GAP),
    placement: "below",
  };
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), Math.max(min, max));
}
