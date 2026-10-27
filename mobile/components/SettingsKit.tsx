import Feather from "@expo/vector-icons/Feather";
import { useRouter } from "expo-router";
import type { ReactNode } from "react";
import {
  KeyboardAvoidingView,
  Platform,
  ScrollView,
  StyleSheet,
  Switch,
  Text,
  TextInput,
  View,
  type TextInputProps,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useStyles, useTheme } from "../lib/appearance";
import { font, Palette, radius, size, space, scaled } from "../lib/theme";
import { ContentColumn } from "./ContentColumn";
import { MotionPressable } from "./MotionPressable";

/**
 * The pieces Agentman's settings pages are made of, in the Settings screen's
 * style: a page with a way back and a title, sections of rows on cards, and
 * the controls those rows hold.
 */
export function SettingsPage({
  title,
  intro,
  action,
  children,
}: {
  title: string;
  /** A line under the title saying what the page is for. */
  intro?: string;
  /** A control at the top right, such as Save or Add. */
  action?: ReactNode;
  children: ReactNode;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  return (
    <KeyboardAvoidingView
      style={[styles.page, { paddingTop: insets.top }]}
      behavior={Platform.OS === "ios" ? "padding" : undefined}
    >
      <ScrollView
        contentContainerStyle={{ paddingBottom: insets.bottom + space.xl }}
        keyboardShouldPersistTaps="handled"
        keyboardDismissMode="interactive"
      >
        <ContentColumn style={styles.content}>
          <View style={styles.topBar}>
            <MotionPressable onPress={() => router.back()} hitSlop={12} pressedScale={0.92}
              style={styles.iconButton} accessibilityRole="button" accessibilityLabel="Back">
              <Feather name="chevron-left" size={20} color={color.text} />
            </MotionPressable>
            {action}
          </View>
          <Text style={styles.title} accessibilityRole="header">{title}</Text>
          {intro ? <Text style={styles.intro}>{intro}</Text> : null}
          {children}
        </ContentColumn>
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

export function Section({ label, footer, children }: { label?: string; footer?: string; children: ReactNode }) {
  const styles = useStyles(makeStyles);
  return (
    <View>
      {label ? <Text style={styles.sectionLabel}>{label}</Text> : <View style={styles.sectionGap} />}
      <View style={styles.card}>{children}</View>
      {footer ? <Text style={styles.footer}>{footer}</Text> : null}
    </View>
  );
}

export function Divider() {
  const styles = useStyles(makeStyles);
  return <View style={styles.divider} />;
}

/** A setting: what it is on the left, its control on the right. */
export function SettingRow({ label, detail, children }: { label: string; detail?: string; children?: ReactNode }) {
  const styles = useStyles(makeStyles);
  return (
    <View style={styles.row}>
      <View style={styles.rowWords}>
        <Text style={styles.rowLabel}>{label}</Text>
        {detail ? <Text style={styles.rowDetail}>{detail}</Text> : null}
      </View>
      {children}
    </View>
  );
}

export function SwitchRow({ label, detail, value, onChange, disabled }: {
  label: string;
  detail?: string;
  value: boolean;
  onChange(value: boolean): void;
  disabled?: boolean;
}) {
  const { color } = useTheme();
  return (
    <SettingRow label={label} detail={detail}>
      <Switch value={value} onValueChange={onChange} disabled={disabled}
        trackColor={{ true: color.working, false: color.fillStrong }} />
    </SettingRow>
  );
}

/** A row that opens a page or a menu, with what is chosen now. */
export function LinkRow({ label, value, onPress, icon }: {
  label: string;
  value?: string;
  onPress(): void;
  icon?: React.ComponentProps<typeof Feather>["name"];
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  return (
    <MotionPressable onPress={onPress} style={styles.row} pressedScale={0.99} accessibilityRole="button"
      accessibilityLabel={value ? `${label}: ${value}` : label}>
      <View style={styles.linkLabel}>
        {icon ? <Feather name={icon} size={16} color={color.muted} /> : null}
        <Text style={styles.rowLabel}>{label}</Text>
      </View>
      <View style={styles.linkValue}>
        {value ? <Text style={styles.rowValue} numberOfLines={1}>{value}</Text> : null}
        <Feather name="chevron-right" size={16} color={color.faint} />
      </View>
    </MotionPressable>
  );
}

/** Choices side by side, one of them on. */
export function Segmented<T extends string>({ options, value, onChange }: {
  options: { value: T; label: string }[];
  value: T;
  onChange(value: T): void;
}) {
  const styles = useStyles(makeStyles);
  return (
    <View style={styles.segmented} accessibilityRole="radiogroup">
      {options.map((option) => {
        const active = option.value === value;
        return (
          <MotionPressable key={option.value} onPress={() => onChange(option.value)}
            style={[styles.segment, active && styles.segmentActive]} pressedScale={0.97}
            accessibilityRole="radio" accessibilityState={{ selected: active }}>
            <Text style={[styles.segmentLabel, active && styles.segmentLabelActive]}>{option.label}</Text>
          </MotionPressable>
        );
      })}
    </View>
  );
}

export function Field(props: TextInputProps & { mono?: boolean }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const { mono, style, ...rest } = props;
  return (
    <TextInput
      placeholderTextColor={color.faint}
      {...rest}
      style={[styles.field, props.multiline && styles.fieldMultiline, mono && styles.fieldMono, style]}
    />
  );
}

/** A text button for a page's top bar. */
export function TopAction({ label, onPress, disabled, busy }: {
  label: string;
  onPress(): void;
  disabled?: boolean;
  busy?: boolean;
}) {
  const styles = useStyles(makeStyles);
  return (
    <MotionPressable onPress={onPress} disabled={disabled || busy} style={[styles.topAction, (disabled || busy) && styles.topActionOff]}
      pressedScale={0.95} accessibilityRole="button">
      <Text style={styles.topActionText}>{busy ? "Saving…" : label}</Text>
    </MotionPressable>
  );
}

export function Problem({ text }: { text: string | null }) {
  const styles = useStyles(makeStyles);
  if (!text) return null;
  return <Text style={styles.problem}>{text}</Text>;
}

export const makeStyles = (c: Palette) =>
  StyleSheet.create({
    page: { flex: 1, backgroundColor: c.paper },
    content: { paddingHorizontal: space.lg },
    topBar: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingTop: space.sm },
    iconButton: {
      width: 40,
      height: 40,
      borderRadius: radius.pill,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.line,
    },
    title: {
      fontFamily: font.sansBold,
      fontSize: size.display,
      letterSpacing: -1.2,
      color: c.text,
      marginTop: space.lg,
      marginBottom: space.xs,
    },
    intro: { fontFamily: font.sans, fontSize: size.caption, lineHeight: scaled(19), color: c.muted, marginBottom: space.xs },
    sectionLabel: {
      fontFamily: font.sansBold,
      fontSize: size.caption,
      color: c.muted,
      marginLeft: space.xxs,
      marginTop: space.xl,
      marginBottom: space.sm,
    },
    sectionGap: { height: space.lg },
    card: {
      backgroundColor: c.surface,
      borderRadius: radius.xl,
      paddingHorizontal: space.lg,
      borderWidth: 1,
      borderColor: c.line,
    },
    footer: {
      fontFamily: font.sans,
      fontSize: size.label,
      lineHeight: scaled(17),
      color: c.faint,
      marginTop: space.sm,
      marginHorizontal: space.xs,
    },
    divider: { height: StyleSheet.hairlineWidth, backgroundColor: c.line },
    row: {
      minHeight: 56,
      flexDirection: "row",
      alignItems: "center",
      justifyContent: "space-between",
      gap: space.md,
      paddingVertical: space.md,
    },
    rowWords: { flex: 1, gap: 2 },
    rowLabel: { fontFamily: font.sansMedium, fontSize: size.body, color: c.text },
    rowDetail: { fontFamily: font.sans, fontSize: size.label, lineHeight: scaled(17), color: c.muted },
    rowValue: { flexShrink: 1, textAlign: "right", fontFamily: font.sans, fontSize: size.caption, color: c.muted },
    linkLabel: { flexDirection: "row", alignItems: "center", gap: space.sm },
    linkValue: { flex: 1, flexDirection: "row", alignItems: "center", justifyContent: "flex-end", gap: space.sm },
    segmented: { flexDirection: "row", padding: 4, gap: 4, borderRadius: radius.pill, backgroundColor: c.fill, marginVertical: space.sm },
    segment: { flex: 1, minHeight: 36, borderRadius: radius.pill, alignItems: "center", justifyContent: "center" },
    segmentActive: {
      backgroundColor: c.surface,
      shadowColor: c.shadow,
      shadowOpacity: 0.08,
      shadowRadius: 6,
      shadowOffset: { width: 0, height: 2 },
      elevation: 1,
    },
    segmentLabel: { fontFamily: font.sansMedium, fontSize: size.caption, color: c.muted },
    segmentLabelActive: { fontFamily: font.sansBold, color: c.text },
    field: {
      minHeight: 44,
      paddingVertical: space.sm,
      fontFamily: font.sans,
      fontSize: size.body,
      color: c.text,
    },
    fieldMultiline: { minHeight: 120, textAlignVertical: "top", lineHeight: scaled(21), paddingTop: space.md, paddingBottom: space.md },
    fieldMono: { fontFamily: font.mono, fontSize: size.caption },
    topAction: {
      height: 36,
      paddingHorizontal: space.lg,
      borderRadius: radius.pill,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.inverse,
    },
    topActionOff: { opacity: 0.35 },
    topActionText: { fontFamily: font.sansBold, fontSize: size.caption, color: c.onInverse },
    problem: { fontFamily: font.sans, fontSize: size.label, color: c.errorText, marginTop: space.sm, marginHorizontal: space.xs },
  });
