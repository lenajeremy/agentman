import { ReactNode } from "react";
import { StyleProp, StyleSheet, View, ViewStyle } from "react-native";

import { layout } from "../lib/theme";

export function ContentColumn({
  children,
  narrow = false,
  style,
}: {
  children: ReactNode;
  narrow?: boolean;
  style?: StyleProp<ViewStyle>;
}) {
  return (
    <View style={[styles.column, narrow && styles.narrow, style]}>
      {children}
    </View>
  );
}

const styles = StyleSheet.create({
  column: {
    alignSelf: "center",
    width: "100%",
    maxWidth: layout.contentMax,
  },
  narrow: { maxWidth: layout.formMax },
});
