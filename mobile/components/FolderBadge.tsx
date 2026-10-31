import { useEffect, useState } from "react";
import { Image, StyleSheet, Text, View } from "react-native";

import { useStyles } from "../lib/appearance";
import { folderIconKey, folderIconNow, loadFolderIcon } from "../lib/folder-icons";
import { folderName } from "../lib/session-view";
import { useStore } from "../lib/store";
import { font, Palette } from "../lib/theme";

/**
 * The folder's logo as a data URI, once the Mac has found one (its favicon,
 * logo.png or icon.png) and it has been fetched; null until then, and for a
 * folder without one.
 */
export function useFolderIcon(sessionId: string, cwd: string, icon?: string): string | null {
  const store = useStore();
  const key = icon ? folderIconKey(cwd, icon) : null;
  const [picture, setPicture] = useState(() => (key ? folderIconNow(key) : null));

  useEffect(() => {
    if (!key || !icon) {
      setPicture(null);
      return;
    }
    let live = true;
    void loadFolderIcon(key, () => store.workspace(sessionId, "read_file", icon)).then((uri) => {
      if (live) setPicture(uri);
    });
    return () => {
      live = false;
    };
    // The store's identity changes as it updates; the logo depends on the key.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, sessionId]);

  return picture;
}

/** A folder's logo, its corners rounded in proportion. */
export function FolderPicture({ uri, size }: { uri: string; size: number }) {
  return (
    <Image
      source={{ uri }}
      style={{ width: size, height: size, borderRadius: Math.round(size * 0.25) }}
      accessibilityIgnoresInvertColors
    />
  );
}

/** A folder by its first two letters on the brand wash, as on the Mac. */
export function FolderInitials({ cwd, size = 16 }: { cwd: string; size?: number }) {
  const styles = useStyles(makeStyles);
  return (
    <View style={[styles.badge, { width: size, height: size, borderRadius: Math.round(size * 0.25) }]}>
      <Text style={[styles.letters, { fontSize: Math.max(7, size * 0.47) }]}>
        {folderName(cwd).slice(0, 2).toUpperCase()}
      </Text>
    </View>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    badge: { alignItems: "center", justifyContent: "center", backgroundColor: c.workingWash },
    letters: { ...font.sansBold, color: c.workingText },
  });
