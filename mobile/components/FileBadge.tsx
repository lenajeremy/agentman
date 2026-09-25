import { StyleSheet, View } from "react-native";
import Svg, { Path } from "react-native-svg";

import { fileIcons } from "../lib/fileIcons";
import { fileIconName } from "../lib/filetype";

/**
 * A file's type, shown with a Material Icon Theme symbol.
 *
 * This used to be a two-or-three letter tag in the language's colour — GO, TSX,
 * { } — which scanned well but asked you to learn a second alphabet for
 * something you already recognise by sight. The icons come from the Material
 * Icon Theme extension for VS Code.
 *
 * A single grey document on every row is still the thing to avoid: it gives
 * the eye nothing to sort by and a long directory reads as a wall. These carry
 * their own colours for exactly that reason.
 */
export function FileBadge({ name, directory }: { name: string; directory: boolean }) {
  const icon = fileIcons[fileIconName(name, directory)] ?? fileIcons.document;

  return (
    <View style={styles.box}>
      <Svg width={ICON} height={ICON} viewBox={icon.viewBox}>
        {icon.paths.map((path, index) => (
          <Path
            key={index}
            d={path.d}
            fill={path.fill}
            fillRule={path.evenOdd ? "evenodd" : "nonzero"}
          />
        ))}
      </Svg>
    </View>
  );
}

const ICON = 20;
const BOX = 30;

const styles = StyleSheet.create({
  // Keep names aligned and prevent the icon from shrinking beside long paths.
  box: {
    width: BOX,
    height: BOX,
    flexShrink: 0,
    alignItems: "center",
    justifyContent: "center",
  },
});
