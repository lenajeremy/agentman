import Feather from "@expo/vector-icons/Feather";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  ActivityIndicator,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { ChangeList } from "../../components/ChangeList";
import { CommitList } from "../../components/CommitList";
import { ContentColumn } from "../../components/ContentColumn";
import { FileBadge } from "../../components/FileBadge";
import { MotionPressable } from "../../components/MotionPressable";
import { Popover, PopoverItem } from "../../components/Popover";
import { useStyles, useTheme } from "../../lib/appearance";
import { appendPage, commitMatches } from "../../lib/commits";
import {
  WorkspaceBranch,
  WorkspaceCommit,
  WorkspaceResult,
} from "../../lib/protocol";
import { useStore } from "../../lib/store";
import { ago, font, Palette, radius, size, space } from "../../lib/theme";

/** A directory listing is worth filtering long before it is worth paging. */
const FILTER_FROM_ENTRIES = 8;

type Tab = "files" | "changes" | "commits";
const TABS: { value: Tab; label: string }[] = [
  { value: "files", label: "Files" },
  { value: "changes", label: "Changes" },
  { value: "commits", label: "Commits" },
];

export default function WorkspaceScreen() {
  const {
    id,
    path: pathParam,
    tab: tabParam,
  } = useLocalSearchParams<{
    id: string;
    path?: string;
    tab?: string;
  }>();
  const sessionId = decodeURIComponent(String(id));
  // Folders are local state rather than routes. Pushing a screen per level
  // rebuilt the header and tabs on every tap and made "back" walk out one
  // directory at a time; this keeps one page whose listing changes.
  const [folder, setFolder] = useState(
    typeof pathParam === "string" ? pathParam : "",
  );
  const store = useStore();
  const session = store.findSession(sessionId);
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  // Openable on any tab. Arriving from "3 files changed" and landing on a
  // directory listing means tapping again to reach the thing that was named on
  // the button you pressed.
  const [tab, setTab] = useState<Tab>(
    TABS.find((item) => item.value === tabParam)?.value ?? "files",
  );
  const [result, setResult] = useState<WorkspaceResult | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [refresh, setRefresh] = useState(0);
  const [filter, setFilter] = useState("");
  // History arrives a page at a time. The first page is the screen's ordinary
  // load; older pages are added below it on request.
  const [commits, setCommits] = useState<WorkspaceCommit[]>([]);
  const [olderCommits, setOlderCommits] = useState(false);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [olderError, setOlderError] = useState("");
  // Bumped on every fresh load, so an older page that lands after a refresh
  // or a change of tab is dropped rather than added to a list it is not from.
  const generation = useRef(0);
  // The branch whose history is shown; null for whatever is checked out.
  // Showing a branch reads its history and checks nothing out, so an agent
  // working here keeps its branch, its index and its files.
  const [shownBranch, setShownBranch] = useState<WorkspaceBranch | null>(null);
  const [branches, setBranches] = useState<WorkspaceBranch[]>([]);
  const [branchMenu, setBranchMenu] = useState(false);
  const [loadingBranches, setLoadingBranches] = useState(false);
  const [branchError, setBranchError] = useState("");
  const [headerHeight, setHeaderHeight] = useState(0);

  useEffect(() => {
    let current = true;
    generation.current += 1;
    setLoading(true);
    setError("");
    setResult(null);
    setCommits([]);
    setOlderCommits(false);
    setLoadingOlder(false);
    setOlderError("");
    void store
      .workspace(
        sessionId,
        tab === "files" ? "list_files" : tab === "changes" ? "list_changes" : "list_commits",
        tab === "files" ? folder : "",
        undefined,
        tab === "commits" && shownBranch ? { ref: shownBranch.ref } : undefined,
      )
      .then((value) => {
        if (!current) return;
        setResult(value);
        if (tab === "commits") {
          setCommits(value.commits ?? []);
          setOlderCommits(value.truncated === true);
        }
      })
      .catch((reason) => {
        if (current)
          setError(reason instanceof Error ? reason.message : String(reason));
      })
      .finally(() => {
        if (current) setLoading(false);
      });
    return () => {
      current = false;
    };
    // The request is keyed to route and refresh, not to store's changing snapshot.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, folder, tab, refresh, shownBranch?.ref]);

  // A filter typed in one folder means nothing in the next one.
  useEffect(() => setFilter(""), [folder, tab]);

  const openFolder = (name: string) =>
    setFolder(folder ? `${folder}/${name}` : name);
  const loadOlder = () => {
    if (loadingOlder) return;
    const asked = generation.current;
    setLoadingOlder(true);
    setOlderError("");
    void store
      .workspace(
        sessionId,
        "list_commits",
        "",
        commits.length,
        shownBranch ? { ref: shownBranch.ref } : undefined,
      )
      .then((page) => {
        if (generation.current !== asked) return;
        setCommits((shown) => appendPage(shown, page.commits ?? []));
        setOlderCommits(page.truncated === true);
      })
      .catch((reason) => {
        if (generation.current === asked)
          setOlderError(reason instanceof Error ? reason.message : String(reason));
      })
      .finally(() => {
        if (generation.current === asked) setLoadingOlder(false);
      });
  };
  // Branches come and go while agents work, so the list is read each time
  // the menu opens rather than once.
  const openBranches = () => {
    if (loadingBranches) return;
    setLoadingBranches(true);
    setBranchError("");
    void store
      .workspace(sessionId, "list_branches")
      .then((value) => {
        setBranches(value.branches ?? []);
        setBranchMenu(true);
      })
      .catch((reason) =>
        setBranchError(reason instanceof Error ? reason.message : String(reason)),
      )
      .finally(() => setLoadingBranches(false));
  };
  const openCommit = (commit: WorkspaceCommit) =>
    router.push(
      `/commit/${encodeURIComponent(sessionId)}?sha=${encodeURIComponent(commit.hash)}`,
    );
  const openFile = (name: string, initial: "code" | "diff" = "code") => {
    const next = tab === "files" && folder ? `${folder}/${name}` : name;
    router.push(
      `/file/${encodeURIComponent(sessionId)}?path=${encodeURIComponent(next)}&tab=${initial}`,
    );
  };
  /** Jump straight to an ancestor instead of stepping up one level at a time. */
  const openCrumb = (depth: number) =>
    setFolder(folder.split("/").slice(0, depth).join("/"));
  // Back leaves the workspace only from the root; inside, it goes up a level,
  // which is what the breadcrumb implies.
  const goBack = () => {
    if (folder) openCrumb(folder.split("/").length - 1);
    else router.back();
  };

  const needle = filter.trim().toLowerCase();
  const entries = useMemo(() => {
    const all = result?.entries ?? [];
    if (!needle) return all;
    return all.filter((entry) => entry.name.toLowerCase().includes(needle));
  }, [result, needle]);
  const changes = useMemo(() => {
    const all = result?.changes ?? [];
    if (!needle) return all;
    return all.filter((change) => change.path.toLowerCase().includes(needle));
  }, [result, needle]);
  const shownCommits = useMemo(
    () => commits.filter((commit) => commitMatches(commit, needle)),
    [commits, needle],
  );

  const crumbs = folder ? folder.split("/") : [];
  const total =
    tab === "files"
      ? (result?.entries?.length ?? 0)
      : tab === "changes"
        ? (result?.changes?.length ?? 0)
        : commits.length;
  const more = tab === "commits" ? olderCommits : result?.truncated === true;
  const noun = tab === "commits" ? "commit" : "item";
  // The checked-out branch, which the Mac reports with every page of history.
  const checkedOut = result?.kind === "commits" ? result.branch : undefined;
  // Offered once there is history to choose from, and kept through a failed
  // load, so a branch deleted since it was chosen can be swapped for another.
  const branch =
    tab === "commits" && (result?.kind === "commits" || shownBranch)
      ? (shownBranch?.name ?? checkedOut ?? "HEAD")
      : undefined;
  const branchItems: PopoverItem[] = useMemo(() => {
    // The checked-out branch first, then local branches, then remote ones,
    // each newest first as the Mac sent them.
    const order = (item: WorkspaceBranch) => (item.current ? 0 : item.remote ? 2 : 1);
    const sorted = [...branches].sort((a, b) => order(a) - order(b));
    const items: PopoverItem[] = sorted.map((item) => ({
      key: item.ref,
      label: item.name,
      mono: true,
      icon: item.remote ? "cloud" : "git-branch",
      detail: item.current ? "checked out" : ago(item.updatedAt),
      checked: shownBranch ? shownBranch.ref === item.ref : item.current === true,
      onPress: () => setShownBranch(item.current ? null : item),
    }));
    // A detached HEAD is on no branch, so it has its own way back.
    if (!branches.some((item) => item.current)) {
      items.unshift({
        key: "HEAD",
        label: "HEAD",
        mono: true,
        icon: "git-commit",
        detail: "checked out",
        checked: shownBranch === null,
        onPress: () => setShownBranch(null),
      });
    }
    return items;
  }, [branches, shownBranch]);
  const hidden = result?.hidden ?? 0;
  const hiddenLabel = `${hidden} ${hidden === 1 ? "file" : "files"}`;
  const showFilter = !loading && !error && total >= FILTER_FROM_ENTRIES;

  return (
    <View style={[styles.page, { paddingTop: insets.top }]}>
      {/* Measured so the branch menu opens just under the header. */}
      <View
        onLayout={(event) => setHeaderHeight(event.nativeEvent.layout.height)}
      >
        <ContentColumn style={styles.header}>
          <View style={styles.topBar}>
            <MotionPressable
              onPress={goBack}
              style={styles.iconButton}
              accessibilityRole="button"
              accessibilityLabel={folder ? "Up one folder" : "Back"}
            >
              <Feather name="chevron-left" size={20} color={color.text} />
            </MotionPressable>
            <View style={styles.heading}>
              <Text
                style={styles.title}
                numberOfLines={1}
                accessibilityRole="header"
              >
                {tab === "changes"
                  ? "Changes"
                  : tab === "commits"
                    ? "Commits"
                    : crumbs.length > 0
                      ? crumbs[crumbs.length - 1]
                      : "Workspace"}
              </Text>
              <Text style={styles.subtitle} numberOfLines={1}>
                {session ? session.cwd.split("/").at(-1) : sessionId}
                {total > 0
                  ? ` · ${total}${more ? "+" : ""} ${noun}${total === 1 ? "" : "s"}`
                  : ""}
              </Text>
            </View>
            <MotionPressable
              onPress={() => setRefresh((value) => value + 1)}
              style={styles.iconButton}
              accessibilityRole="button"
              accessibilityLabel="Refresh"
            >
              <Feather name="refresh-cw" size={17} color={color.text} />
            </MotionPressable>
          </View>

          <View style={styles.tabs}>
            {TABS.map(({ value, label }) => (
              <MotionPressable
                key={value}
                onPress={() => setTab(value)}
                style={[styles.tab, tab === value && styles.tabActive]}
                accessibilityRole="tab"
                accessibilityState={{ selected: tab === value }}
              >
                <Text
                  style={[styles.tabText, tab === value && styles.tabTextActive]}
                >
                  {label}
                </Text>
              </MotionPressable>
            ))}
          </View>

          {tab === "files" ? (
            // Breadcrumbs scroll rather than wrap: a deep path must not push the
            // listing down the screen.
            <ScrollView
              horizontal
              showsHorizontalScrollIndicator={false}
              style={styles.crumbBar}
              contentContainerStyle={styles.crumbs}
            >
              <MotionPressable
                onPress={() => openCrumb(0)}
                disabled={crumbs.length === 0}
                accessibilityRole="button"
                accessibilityLabel="Workspace root"
              >
                <Feather
                  name="home"
                  size={13}
                  color={crumbs.length === 0 ? color.text : color.workingText}
                />
              </MotionPressable>
              {crumbs.map((crumb, index) => (
                <View key={`${crumb}:${index}`} style={styles.crumbItem}>
                  <Feather name="chevron-right" size={12} color={color.faint} />
                  <MotionPressable
                    onPress={() => openCrumb(index + 1)}
                    disabled={index === crumbs.length - 1}
                    accessibilityRole="button"
                  >
                    <Text
                      style={[
                        styles.crumb,
                        index === crumbs.length - 1 && styles.crumbCurrent,
                      ]}
                      numberOfLines={1}
                    >
                      {crumb}
                    </Text>
                  </MotionPressable>
                </View>
              ))}
            </ScrollView>
          ) : null}

          {/* On the Commits tab the branch sits beside the filter: it says
              whose history this is, and a subtitle already holding a folder
              and a count cut it off. */}
          {showFilter || branch ? (
            <View style={styles.toolRow}>
              {branch ? (
                <MotionPressable
                  onPress={openBranches}
                  style={styles.branch}
                  pressedScale={0.97}
                  accessibilityRole="button"
                  accessibilityState={{ busy: loadingBranches, expanded: branchMenu }}
                  accessibilityLabel={`Showing ${branch}. Choose a branch`}
                >
                  <Feather name="git-branch" size={13} color={color.muted} />
                  <Text
                    style={styles.branchName}
                    numberOfLines={1}
                    ellipsizeMode="middle"
                  >
                    {branch}
                  </Text>
                  {loadingBranches ? (
                    <ActivityIndicator size="small" color={color.faint} style={styles.branchSpinner} />
                  ) : (
                    <Feather name="chevron-down" size={13} color={color.faint} />
                  )}
                </MotionPressable>
              ) : null}
              {showFilter ? (
                <View style={styles.search}>
                  <Feather name="search" size={14} color={color.faint} />
                  <View style={styles.searchField}>
                    {/* The native placeholder rendered in the wrong face — tracked
                        out, as though it had fallen back to the mono font — while
                        every other Text in the app resolved Geist correctly. Drawing
                        it as a Text makes it match the rest by construction. */}
                    {filter.length === 0 ? (
                      <Text
                        style={styles.searchPlaceholder}
                        pointerEvents="none"
                        numberOfLines={1}
                      >
                        {/* Beside the branch there is room for one word, and
                          the tab already says what is being filtered. */}
                      {tab === "commits" ? "Filter…" : `Filter ${tab}…`}
                      </Text>
                    ) : null}
                    <TextInput
                      value={filter}
                      onChangeText={setFilter}
                      style={styles.searchInput}
                      autoCapitalize="none"
                      autoCorrect={false}
                      accessibilityLabel={`Filter ${tab}`}
                    />
                  </View>
                  {filter.length > 0 ? (
                    <MotionPressable
                      onPress={() => setFilter("")}
                      hitSlop={8}
                      accessibilityRole="button"
                      accessibilityLabel="Clear filter"
                    >
                      <Feather name="x-circle" size={15} color={color.faint} />
                    </MotionPressable>
                  ) : null}
                </View>
              ) : null}
            </View>
          ) : null}
        </ContentColumn>
      </View>

      {branchMenu ? (
        <Popover
          visible
          onDismiss={() => setBranchMenu(false)}
          top={headerHeight}
          align="left"
          title={
            checkedOut
              ? `Your Mac stays on ${checkedOut}. Choosing a branch only shows its history.`
              : "Choosing a branch only shows its history. Nothing is checked out."
          }
          items={branchItems}
        />
      ) : null}

      <ScrollView
        contentContainerStyle={{ paddingBottom: insets.bottom + space.xl }}
        keyboardShouldPersistTaps="handled"
      >
        <ContentColumn style={styles.content}>
          {loading && (
            <ActivityIndicator style={styles.center} color={color.muted} />
          )}
          {!!error && <Text style={styles.error}>{error}</Text>}
          {tab === "commits" && branchError ? (
            <Text style={styles.error}>{branchError}</Text>
          ) : null}

          {!loading &&
            !error &&
            needle.length > 0 &&
            (tab === "files"
              ? entries.length === 0
              : tab === "changes"
                ? changes.length === 0
                : shownCommits.length === 0) && (
              <View style={styles.emptyWrap}>
                <Feather name="search" size={22} color={color.faint} />
                <Text style={styles.empty}>
                  Nothing matches “{filter.trim()}”.
                </Text>
              </View>
            )}
          {!loading && !error && !needle && tab === "files" && total === 0 && (
            <View style={styles.emptyWrap}>
              <Feather name="folder" size={22} color={color.faint} />
              <Text style={styles.empty}>
                {hidden > 0
                  ? `Nothing to show here. ${hiddenLabel} kept private.`
                  : "This folder is empty."}
              </Text>
            </View>
          )}
          {!loading &&
            !error &&
            !needle &&
            tab === "changes" &&
            total === 0 && (
              <View style={styles.emptyWrap}>
                <Feather name="check-circle" size={22} color={color.faint} />
                <Text style={styles.empty}>
                  {hidden > 0
                    ? `No changes you can review. ${hiddenLabel} kept private.`
                    : "No working tree changes here."}
                </Text>
              </View>
            )}
          {!loading &&
            !error &&
            !needle &&
            tab === "commits" &&
            total === 0 && (
              <View style={styles.emptyWrap}>
                <Feather name="git-commit" size={22} color={color.faint} />
                <Text style={styles.empty}>
                  {shownBranch
                    ? `No commits on ${shownBranch.name} touch this folder.`
                    : "Nothing has been committed here yet."}
                </Text>
              </View>
            )}

          {/* One bordered surface with hairline dividers, rather than a card
              per row: a directory is a list, and the per-row chrome was
              costing about half the rows that fit on screen. */}
          {tab === "files" && entries.length > 0 ? (
            <View style={styles.list}>
              {entries.map((entry, index) => (
                <MotionPressable
                  key={entry.name}
                  onPress={() =>
                    entry.directory
                      ? openFolder(entry.name)
                      : openFile(entry.name)
                  }
                  style={[styles.row, index > 0 && styles.rowDivided]}
                  pressedScale={0.995}
                  accessibilityRole="button"
                  accessibilityLabel={`${entry.name}, ${entry.directory ? "folder" : "file"}`}
                >
                  <FileBadge name={entry.name} directory={entry.directory} />
                  <Text
                    style={[
                      styles.rowName,
                      entry.directory && styles.rowFolder,
                    ]}
                    numberOfLines={1}
                  >
                    {entry.name}
                  </Text>
                  {!entry.directory && entry.size !== undefined ? (
                    <Text style={styles.rowMeta}>{formatSize(entry.size)}</Text>
                  ) : null}
                  <Feather name="chevron-right" size={14} color={color.faint} />
                </MotionPressable>
              ))}
            </View>
          ) : null}

          {tab === "changes" && changes.length > 0 ? (
            <ChangeList
              changes={changes}
              onOpen={(change) => openFile(change.path, "diff")}
            />
          ) : null}

          {tab === "commits" && !loading && shownBranch && checkedOut &&
          shownBranch.name !== checkedOut ? (
            <View style={styles.viewing}>
              <Feather name="eye" size={12} color={color.faint} />
              <Text style={styles.viewingText} numberOfLines={1}>
                Viewing only. Your Mac is still on {checkedOut}.
              </Text>
            </View>
          ) : null}
          {tab === "commits" && shownCommits.length > 0 ? (
            <CommitList commits={shownCommits} onOpen={openCommit} />
          ) : null}
          {tab === "commits" && !loading && !error && olderCommits ? (
            <MotionPressable
              onPress={loadOlder}
              disabled={loadingOlder}
              style={styles.older}
              accessibilityRole="button"
              accessibilityState={{ busy: loadingOlder }}
              accessibilityLabel="Show older commits"
            >
              {loadingOlder ? (
                <ActivityIndicator size="small" color={color.muted} />
              ) : (
                <Feather name="chevron-down" size={15} color={color.text} />
              )}
              <Text style={styles.olderText}>Show older commits</Text>
            </MotionPressable>
          ) : null}
          {olderError ? <Text style={styles.error}>{olderError}</Text> : null}

          {tab !== "commits" && result?.truncated && (
            <Text style={styles.note}>Showing the first entries only.</Text>
          )}
          {/* Say what was withheld. This screen once reported "no changes"
              while twenty files sat behind a filter, and nothing on it could
              have told you otherwise. */}
          {hidden > 0 && total > 0 && (
            <Text style={styles.note}>
              {hiddenLabel} kept private and not shown.
            </Text>
          )}
          {/* A filter can only search what has arrived, and older pages
              have not; say so rather than imply the history has no match. */}
          {tab === "commits" && needle.length > 0 && olderCommits && (
            <Text style={styles.note}>
              Filtering the commits loaded so far. Older ones may match too.
            </Text>
          )}
          {!loading && !error && tab !== "commits" && (
            <Text style={styles.note}>
              {tab === "files"
                ? "Private files and symlinks are hidden. Files are read only."
                : "Changes since the last commit, including staged and new files."}
            </Text>
          )}
        </ContentColumn>
      </ScrollView>
    </View>
  );
}

/** Short enough to sit in a row without pushing the filename out. */
function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    page: { flex: 1, backgroundColor: c.paper },
    header: {
      paddingHorizontal: space.lg,
      paddingTop: space.sm,
      paddingBottom: space.sm,
    },
    topBar: { flexDirection: "row", alignItems: "center", gap: space.sm },
    heading: { flex: 1, minWidth: 0 },
    // The old display-size title cost a fifth of the screen on a browser whose
    // whole job is showing as many rows as possible.
    title: {
      color: c.text,
      fontFamily: font.sansBold,
      fontSize: size.title,
      letterSpacing: -0.3,
    },
    subtitle: {
      color: c.muted,
      fontFamily: font.mono,
      fontSize: 11,
      marginTop: 1,
    },
    iconButton: {
      width: 36,
      height: 36,
      alignItems: "center",
      justifyContent: "center",
      borderRadius: radius.pill,
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.line,
    },

    tabs: { flexDirection: "row", gap: space.xs, marginTop: space.md },
    tab: {
      paddingHorizontal: space.lg,
      paddingVertical: 7,
      borderRadius: radius.pill,
      backgroundColor: c.fill,
    },
    tabActive: { backgroundColor: c.inverse },
    tabText: {
      color: c.muted,
      fontFamily: font.sansMedium,
      fontSize: size.caption,
    },
    tabTextActive: { color: c.onInverse },

    crumbBar: { marginTop: space.md, flexGrow: 0 },
    crumbs: {
      flexDirection: "row",
      alignItems: "center",
      gap: 3,
      paddingRight: space.lg,
    },
    crumbItem: { flexDirection: "row", alignItems: "center", gap: 3 },
    crumb: { color: c.workingText, fontFamily: font.mono, fontSize: 12 },
    crumbCurrent: { color: c.text, fontFamily: font.monoMedium },

    toolRow: { flexDirection: "row", gap: space.sm, marginTop: space.md },
    branch: {
      flexDirection: "row",
      alignItems: "center",
      gap: 6,
      flexShrink: 1,
      maxWidth: "62%",
      height: 40,
      paddingHorizontal: space.md,
      borderRadius: radius.md,
      borderWidth: 1,
      borderColor: c.line,
      backgroundColor: c.surface,
    },
    branchSpinner: { transform: [{ scale: 0.7 }], width: 13, height: 13 },
    branchName: {
      flexShrink: 1,
      color: c.textSecondary,
      fontFamily: font.monoMedium,
      fontSize: 12,
    },
    search: {
      flex: 1,
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      height: 40,
      paddingHorizontal: space.md,
      borderRadius: radius.md,
      backgroundColor: c.fill,
    },
    // The field fills the row and centres its own text. `padding: 0` alone left
    // the placeholder sitting off the row's centre line, and without an explicit
    // lineHeight the two platforms disagreed about where the baseline sits.
    searchField: { flex: 1, height: "100%", justifyContent: "center" },
    searchPlaceholder: {
      position: "absolute",
      left: 0,
      right: 0,
      color: c.faint,
      fontFamily: font.sans,
      fontSize: size.caption,
    },
    searchInput: {
      height: "100%",
      color: c.text,
      fontFamily: font.sans,
      fontSize: size.caption,
      lineHeight: 18,
      paddingVertical: 0,
      paddingHorizontal: 0,
      includeFontPadding: false,
      textAlignVertical: "center",
    },

    content: { paddingHorizontal: space.lg, paddingTop: space.sm },
    list: {
      borderWidth: 1,
      borderColor: c.line,
      borderRadius: radius.lg,
      backgroundColor: c.surface,
      overflow: "hidden",
    },
    row: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.md,
      minHeight: 44,
      paddingHorizontal: space.md,
    },
    rowDivided: {
      borderTopWidth: StyleSheet.hairlineWidth,
      borderTopColor: c.line,
    },
    rowName: { flex: 1, color: c.text, fontFamily: font.mono, fontSize: 13 },
    rowFolder: { fontFamily: font.monoMedium },
    rowMeta: { color: c.faint, fontFamily: font.mono, fontSize: 11 },

    viewing: {
      flexDirection: "row",
      alignItems: "center",
      gap: 6,
      paddingHorizontal: space.xxs,
      marginBottom: space.xs,
    },
    viewingText: {
      flexShrink: 1,
      color: c.faint,
      fontFamily: font.sans,
      fontSize: 11.5,
    },
    older: {
      flexDirection: "row",
      alignItems: "center",
      justifyContent: "center",
      gap: space.sm,
      minHeight: 44,
      marginTop: space.md,
      borderRadius: radius.pill,
      backgroundColor: c.fill,
    },
    olderText: {
      color: c.text,
      fontFamily: font.sansMedium,
      fontSize: size.caption,
    },

    emptyWrap: {
      alignItems: "center",
      gap: space.sm,
      paddingVertical: space.xxl,
    },
    empty: {
      color: c.muted,
      fontFamily: font.sans,
      fontSize: size.caption,
      textAlign: "center",
    },
    error: {
      color: c.errorText,
      fontFamily: font.sans,
      fontSize: size.body,
      paddingVertical: space.md,
    },
    center: { marginTop: space.xl },
    note: {
      color: c.faint,
      fontFamily: font.sans,
      fontSize: 11.5,
      marginTop: space.md,
      lineHeight: 16,
    },
  });
