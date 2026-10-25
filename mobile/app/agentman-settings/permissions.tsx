import Feather from "@expo/vector-icons/Feather";
import { useState } from "react";
import { StyleSheet, Text, View } from "react-native";

import { MotionPressable } from "../../components/MotionPressable";
import { Divider, Field, Problem, Section, Segmented, SettingsPage, SwitchRow } from "../../components/SettingsKit";
import { useStyles, useTheme } from "../../lib/appearance";
import { useManager } from "../../lib/manager";
import type { ManagerSettings, PermissionCheck } from "../../lib/protocol";
import { font, Palette, radius, size, space } from "../../lib/theme";

type Rule = NonNullable<ManagerSettings["rules"]>[number];
type Kinds = NonNullable<ManagerSettings["approve"]>;

const ALL_KINDS: Kinds = { reads: true, edits: true, commands: true, continue: true };

const RULE_KINDS = [
  { value: "command", label: "Commands", placeholder: "npm run *" },
  { value: "edit", label: "Changing files", placeholder: "docs/*" },
  { value: "read", label: "Reading files", placeholder: "docs/*" },
  { value: "network", label: "Network", placeholder: "https://api.example.com/*" },
  { value: "any", label: "Anything", placeholder: "*.env" },
];

const kindLabel = (kind: string) => RULE_KINDS.find((item) => item.value === kind)?.label ?? kind;
const folderName = (folder: string) => folder.split("/").filter(Boolean).at(-1) ?? folder;

/** What Agentman may approve without asking, the user's own rules, and a
 *  way to try a request against them. Each change is saved as it is made. */
export default function PermissionsSettings() {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const manager = useManager();
  const settings = manager.settings;
  const [problem, setProblem] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);

  if (!settings) {
    return (
      <SettingsPage title="Permissions">
        <Problem text="Agentman's settings haven't arrived from your Mac yet." />
      </SettingsPage>
    );
  }
  const kinds = settings.approve ?? ALL_KINDS;
  const rules = settings.rules ?? [];
  const save = (change: Partial<ManagerSettings>) => {
    void manager.saveSettings({ ...settings, ...change }).then(setProblem);
  };
  const setKind = (key: keyof Kinds, on: boolean) => save({ approve: { ...kinds, [key]: on } });

  return (
    <SettingsPage title="Permissions" intro="What Agentman answers for you the moment an agent asks, and what always waits for you.">
      <Section>
        <SwitchRow label="Approve low-risk requests for me" detail="Agentman answers these itself and tells you in a line."
          value={settings.autoApprove} onChange={(autoApprove) => save({ autoApprove })} />
      </Section>

      <Section label="Agentman may approve" footer="Inside the agent's own folder, and never anything that touches keys or credentials.">
        <SwitchRow label="Reading and searching files" value={kinds.reads} disabled={!settings.autoApprove}
          onChange={(on) => setKind("reads", on)} />
        <Divider />
        <SwitchRow label="Changing files" value={kinds.edits} disabled={!settings.autoApprove}
          onChange={(on) => setKind("edits", on)} />
        <Divider />
        <SwitchRow label="Tests, builds and read-only commands" value={kinds.commands} disabled={!settings.autoApprove}
          onChange={(on) => setKind("commands", on)} />
        <Divider />
        <SwitchRow label="“Continue?” prompts" value={kinds.continue} disabled={!settings.autoApprove}
          onChange={(on) => setKind("continue", on)} />
      </Section>

      <Section label="Your rules"
        footer="An “Always ask” rule wins over everything. No rule lets an agent touch keys and credentials or run anything as root. Use * for anything and ? for one character.">
        {rules.length === 0 && !adding ? <Text style={styles.empty}>No rules yet.</Text> : null}
        {rules.map((rule, index) => (
          <View key={`${rule.action}-${rule.kind}-${rule.pattern}-${rule.folder ?? ""}`}>
            {index > 0 ? <Divider /> : null}
            <View style={styles.rule}>
              <View style={[styles.badge, rule.action === "allow" ? styles.allow : styles.ask]}>
                <Text style={[styles.badgeText, rule.action === "allow" ? styles.allowText : styles.askText]}>
                  {rule.action === "allow" ? "Allow" : "Always ask"}
                </Text>
              </View>
              <View style={styles.ruleWords}>
                <Text style={styles.pattern} numberOfLines={2}>{rule.pattern}</Text>
                <Text style={styles.ruleWhere}>
                  {kindLabel(rule.kind)} · {rule.folder ? `in ${folderName(rule.folder)}` : "everywhere"}
                </Text>
              </View>
              <MotionPressable onPress={() => save({ rules: rules.filter((_, at) => at !== index) })} hitSlop={10}
                accessibilityRole="button" accessibilityLabel={`Remove the rule for ${rule.pattern}`}>
                <Feather name="trash-2" size={16} color={color.muted} />
              </MotionPressable>
            </View>
          </View>
        ))}
        {adding ? (
          <NewRule
            onCancel={() => setAdding(false)}
            onAdd={(rule) => {
              setAdding(false);
              save({ rules: [...rules, rule] });
            }}
          />
        ) : (
          <>
            {rules.length > 0 ? <Divider /> : null}
            <MotionPressable onPress={() => setAdding(true)} style={styles.add} accessibilityRole="button">
              <Feather name="plus" size={16} color={color.workingText} />
              <Text style={styles.addText}>Add a rule</Text>
            </MotionPressable>
          </>
        )}
      </Section>

      <TryIt />
      <Problem text={problem} />
    </SettingsPage>
  );
}

function NewRule({ onAdd, onCancel }: { onAdd(rule: Rule): void; onCancel(): void }) {
  const styles = useStyles(makeStyles);
  const [action, setAction] = useState("allow");
  const [kind, setKind] = useState("command");
  const [pattern, setPattern] = useState("");
  const [folder, setFolder] = useState("");
  const placeholder = RULE_KINDS.find((item) => item.value === kind)?.placeholder ?? "";
  return (
    <View style={styles.newRule}>
      <Segmented options={[{ value: "allow", label: "Allow" }, { value: "ask", label: "Always ask" }]}
        value={action} onChange={setAction} />
      <Segmented options={RULE_KINDS.slice(0, 3).map(({ value, label }) => ({ value, label }))}
        value={kind} onChange={setKind} />
      <Segmented options={RULE_KINDS.slice(3).map(({ value, label }) => ({ value, label }))}
        value={kind} onChange={setKind} />
      <Field value={pattern} onChangeText={setPattern} placeholder={placeholder} mono autoCapitalize="none" autoCorrect={false} />
      <Divider />
      <Field value={folder} onChangeText={setFolder} placeholder="Only in this folder (optional): ~/code/app" mono
        autoCapitalize="none" autoCorrect={false} />
      <View style={styles.newRuleActions}>
        <MotionPressable onPress={onCancel} style={styles.secondary} accessibilityRole="button">
          <Text style={styles.secondaryText}>Cancel</Text>
        </MotionPressable>
        <MotionPressable
          onPress={() => onAdd({ action, kind, pattern: pattern.trim(), ...(folder.trim() ? { folder: folder.trim() } : {}) })}
          disabled={!pattern.trim()}
          style={[styles.primary, !pattern.trim() && styles.off]}
          accessibilityRole="button"
        >
          <Text style={styles.primaryText}>Add rule</Text>
        </MotionPressable>
      </View>
    </View>
  );
}

/** A request put to the rules as an agent would ask it, to see what
 *  Agentman would do. */
function TryIt() {
  const styles = useStyles(makeStyles);
  const manager = useManager();
  const [kind, setKind] = useState("command");
  const [target, setTarget] = useState("");
  const [folder, setFolder] = useState("");
  const [result, setResult] = useState<PermissionCheck | string | null>(null);
  const tryIt = async () => {
    setResult(await manager.checkPermission(kind, target.trim(), folder.trim() || "~"));
  };
  return (
    <Section label="Try it" footer="What Agentman would do if an agent asked this now, under the settings as saved.">
      <Segmented options={RULE_KINDS.slice(0, 4).map(({ value, label }) => ({ value, label: label.split(" ")[0] }))}
        value={kind} onChange={setKind} />
      <Field value={target} onChangeText={setTarget} placeholder="git push origin main" mono autoCapitalize="none"
        autoCorrect={false} onSubmitEditing={() => void tryIt()} />
      <Divider />
      <Field value={folder} onChangeText={setFolder} placeholder="For an agent in: ~/code/app" mono autoCapitalize="none"
        autoCorrect={false} />
      <View style={styles.newRuleActions}>
        <MotionPressable onPress={() => void tryIt()} disabled={!target.trim()} style={[styles.primary, !target.trim() && styles.off]}
          accessibilityRole="button">
          <Text style={styles.primaryText}>Try it</Text>
        </MotionPressable>
      </View>
      {result ? (
        <Text style={[styles.verdict, typeof result !== "string" && result.auto ? styles.verdictAuto : styles.verdictWait]}>
          {typeof result === "string"
            ? result
            : result.auto
              ? `Agentman would approve it: ${result.summary}.`
              : `It would wait for you: ${result.reason ?? result.summary}.`}
        </Text>
      ) : null}
    </Section>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    empty: { fontFamily: font.sans, fontSize: size.caption, color: c.faint, paddingVertical: space.md },
    rule: { flexDirection: "row", alignItems: "center", gap: space.md, paddingVertical: space.md },
    badge: { paddingHorizontal: 8, paddingVertical: 3, borderRadius: radius.pill },
    allow: { backgroundColor: c.okWash },
    ask: { backgroundColor: c.needsYouWash },
    badgeText: { fontFamily: font.sansMedium, fontSize: size.label },
    allowText: { color: c.ok },
    askText: { color: c.needsYouText },
    ruleWords: { flex: 1, gap: 2 },
    pattern: { fontFamily: font.mono, fontSize: size.caption, color: c.text },
    ruleWhere: { fontFamily: font.sans, fontSize: size.label, color: c.muted },
    add: { flexDirection: "row", alignItems: "center", gap: space.sm, paddingVertical: space.md },
    addText: { fontFamily: font.sansMedium, fontSize: size.body, color: c.workingText },
    newRule: { paddingVertical: space.sm, gap: 2 },
    newRuleActions: { flexDirection: "row", justifyContent: "flex-end", gap: space.sm, paddingVertical: space.md },
    secondary: { height: 36, paddingHorizontal: space.lg, borderRadius: radius.pill, justifyContent: "center", backgroundColor: c.fill },
    secondaryText: { fontFamily: font.sansMedium, fontSize: size.caption, color: c.textSecondary },
    primary: { height: 36, paddingHorizontal: space.lg, borderRadius: radius.pill, justifyContent: "center", backgroundColor: c.inverse },
    primaryText: { fontFamily: font.sansBold, fontSize: size.caption, color: c.onInverse },
    off: { opacity: 0.35 },
    verdict: { fontFamily: font.sans, fontSize: size.caption, lineHeight: 19, paddingBottom: space.md },
    verdictAuto: { color: c.ok },
    verdictWait: { color: c.needsYouText },
  });
