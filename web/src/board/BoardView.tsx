// Agentman's page (Views/BoardView.swift): every agent as a card, in a
// column for what it wants from the user, with Agentman's brief above and a
// box to ask it anything floating below. Its conversation opens beside the
// columns. Quiet on purpose: one size of type on a card, and colour only
// where a state is.

import { useEffect, useLayoutEffect, useMemo, useRef, useState, type MouseEvent as ReactMouseEvent } from "react";
import { useKeyboardBack } from "../palette/PaletteView";
import {
  BadgeCheck, ChevronDown, CircleArrowUp, CircleCheck, CircleX as XCircle, Palette, Search, Check, CircleAlert, Circle, CircleX, Clock, Code, Diff, EyeOff, Ellipsis, FileText, Globe, MessagesSquare, MessageSquarePlus,
  Plus, SquarePen, TriangleAlert, X,
} from "lucide-react";
import type { ManagerCard, ManagerEntry, Mission, Question, SearchHit, Session, Verification } from "@shared/protocol";
import { cardQuestion } from "@shared/manager-feed";
import { confirmEnd, endable } from "../AgentHeader";
import { openEditor, openURL } from "../host";
import { folderLabel, since } from "../labels";
import { AgentmanView } from "../manager/AgentmanView";
import { encoded, type Attachment } from "../manager/attach";
import { CompactCard } from "../manager/CompactCard";
import { ManagerBox } from "../manager/Composer";
import { SchedulesButton } from "../manager/Schedules";
import {
  dismissCard, getManager, missionOf, missionProgress, openCardsOf, replyToCard, reset, runningMissions, send as askAgentman, setBoardChatOpen, setProblem,
  unreadOf, useManager, type QuestionReply,
} from "../manager/store";
import { openAgent, openNewAgent } from "../nav";
import { getPref, setPref, usePref } from "../prefs";
import { Loader } from "../shell/Loader";
import { archive, filterState, isArchived, useChoices } from "../shell/sections";
import { loadFolders, setShell, useShell } from "../shell/state";
import { cachedHome } from "../shell/native";
import { follow, sessionsLoaded, useStatus, useStore } from "../store";
import { tidy } from "../thread/items";
import { showPanel } from "../panel/state";
import { git, type GitState } from "../workspace/api";
import { openCardTab } from "../workspace/cardTab";
import { quickAction } from "../workspace/gitAction";
import { confirmSheet, menuUnder, contextMenu, type MenuEntry } from "../thread/menu";
import { promptRepeatsDetail, QuestionChoices, QuestionNotice } from "../thread/QuestionCard";
import { perform, send as sendToAgent, useActionError } from "../thread/state";
import { AgentMark, ChromeButton, FolderMark, Icon } from "../ui";
import { makeDigest, type BoardDigest, type BoardProgress, type BoardStep } from "./digest";
import { detail, headline, makeBoard, mentioned, shown, type BoardSections } from "./sections";
import { ShipSheet, TonightButton } from "./BoardActions";
import { BoardCommitSheet, BoardGitChip } from "./BoardGit";
import { openCommitSheet, useCommitSheet, useGitChanged, useGitRun } from "./git";
import { BoardLink, Snippet } from "./parts";
import { agentColor, COLOR_BY, useColorBy } from "./colors";
import { boardSearch, openHit, searchDetail, searchHeadline, useMessageHits, type BoardSearch } from "./search";
import { saveSettings } from "../settings/state";
import "../styles/board.css";

/** Agentman's conversation, beside the columns. */
const CHAT_WIDTH = 380;
const MARGIN = 20;
const GAP = 10;
/** Columns stretch between these, and scroll sideways rather than get too
 *  narrow to read. */
const NARROWEST = 264;
const WIDEST = 400;
/** What the columns' last cards keep clear of the floating box. */
const ASK_CLEARANCE = 92;

const NEW_CHAT_MESSAGE = "This conversation is set aside, and Agentman keeps what matters from it in its private notes. Cards still waiting stay.";

type Kind = "needsYou" | "working" | "idle";
const TITLES: Record<Kind, string> = { needsYou: "Needs you", working: "Working", idle: "Idle" };
const EMPTY: Record<Kind, string> = { needsYou: "Nothing needs you", working: "Nothing running", idle: "No idle agents" };

// MARK: - The agents taken off the Board by hand, until they next need the
// user. Kept across launches, as the Mac's boardHidden.

const HIDDEN = "boardHidden";
const hideFromBoard = (id: string) => setPref(HIDDEN, [...new Set([...getPref<string[]>(HIDDEN, []), id])]);
const showOnBoard = (id: string) => setPref(HIDDEN, getPref<string[]>(HIDDEN, []).filter((h) => h !== id));
const showAllOnBoard = () => setPref(HIDDEN, []);

export function BoardView({ headerLeading }: { headerLeading: number }) {
  const { sessions, branches } = useStore();
  const manager = useManager();
  const choices = useChoices();
  const { folderFilter, search } = useShell();
  const hits = useMessageHits(search);
  const found = useMemo(() => boardSearch(search, sessions, branches, folderFilter, hits), [search, sessions, branches, folderFilter, hits]);
  const { status } = useStatus();
  const [hiddenList] = usePref<string[]>(HIDDEN, []);
  const chatOpen = manager.boardChatOpen;
  const cards = openCardsOf(manager);

  const sections = useMemo(() => {
    const waiting = new Set(cards.filter((c) => c.kind === "question" || c.kind === "needs_you").map((c) => c.sessionId).filter((id): id is string => !!id));
    return makeBoard(sessions, {
      stateOf: filterState,
      archived: (s) => isArchived(s, choices),
      hidden: new Set(hiddenList),
      waiting,
      folder: folderFilter,
      matching: found?.ids,
    });
  }, [sessions, cards, choices, hiddenList, folderFilter, found]);
  const offers = cards.filter((c) => c.kind === "offer");
  const outlined = useMemo(() => (chatOpen ? mentioned(manager.entries, sessions) : new Set<string>()), [chatOpen, manager.entries, sessions]);

  // A hidden agent that needs the user is back for good, not only while it
  // waits.
  useEffect(() => {
    if (!hiddenList.length) return;
    const back = sessions.filter((s) => hiddenList.includes(s.id) && filterState(s) === "waiting_input").map((s) => s.id);
    if (back.length) setPref(HIDDEN, hiddenList.filter((id) => !back.includes(id)));
  }, [sessions, hiddenList]);

  // The columns' width, from the room there is.
  const room = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(0);
  useLayoutEffect(() => {
    const el = room.current;
    if (!el) return;
    const measure = () => setWidth(el.clientWidth);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, []);
  const column = Math.max(NARROWEST, Math.min(WIDEST, (width - MARGIN * 2 - GAP * 2) / 3));
  const columnsWidth = column * 3 + GAP * 2;
  const row = Math.max(0, Math.min(columnsWidth, width - MARGIN * 2));
  const clearance = chatOpen ? 12 : ASK_CLEARANCE;

  const connected = status === "connected" && sessionsLoaded();
  const committing = useCommitSheet();
  const startsEmpty = !found && shown(sections) === 0 && sections.hidden.length === 0 && connected;

  const newChat = async () => {
    if ((await confirmSheet("Start a new chat?", NEW_CHAT_MESSAGE, ["New Chat", "Cancel"])) !== "New Chat") return;
    const problem = await reset();
    if (problem) setProblem(problem);
  };

  return (
    <div className="board-page">
      <div className="board-main">
        <header className="board-top drag" style={{ paddingLeft: 16 + headerLeading }}>
          {headerLeading > 0 && <span className="board-top-title">Agentman</span>}
          <span className="board-spacer" />
          <span className="no-drag board-top-buttons">
            <HandledButton />
            <TonightButton />
            <ColorMenu />
            <FolderMenu />
            {manager.schedules.length > 0 && <SchedulesButton />}
            <ChromeButton icon={SquarePen} sf="square.and.pencil" title="New agent" onClick={() => openNewAgent()} />
            {!chatOpen && (
              <span className="board-chat-button">
                <ChromeButton icon={MessagesSquare} sf="bubble.left.and.bubble.right" title="Open the conversation with Agentman" onClick={() => setBoardChatOpen(true)} />
                {unreadOf(manager).length > 0 && <span className="board-unread" aria-label="New from Agentman" />}
              </span>
            )}
          </span>
        </header>
        <div className="board-room" ref={room}>
          <div className="board-head" style={{ width: row }}>
            <div className="board-brief">
              <div className="board-headline">{found ? searchHeadline(found, shown(sections)) : headline(sections, connected)}</div>
              <div className="board-detail">
                <span>{found ? searchDetail(found) : startsEmpty ? "Start one and it shows up here." : detail(sections) || "\u00a0"}</span>
                {startsEmpty && <BoardLink title="New agent" icon={Plus} sf="plus" onClick={() => openNewAgent()} />}
              </div>
            </div>
            <SearchField width={Math.min(380, row * 0.45)} />
          </div>
          <div className="board-scroll">
            <div className="board-columns" style={{ gridTemplateColumns: `repeat(3, ${column}px)`, minWidth: width }}>
              <BoardColumn kind="needsYou" sessions={sections.needsYou} outlined={outlined} found={found} offers={offers} clearance={clearance} />
              <BoardColumn kind="working" sessions={sections.working} outlined={outlined} found={found} missions={runningMissions(manager)} clearance={clearance} />
              <BoardColumn kind="idle" sessions={sections.idle} outlined={outlined} found={found} hidden={sections.hidden} clearance={clearance} />
            </div>
          </div>
        </div>
        {!chatOpen && <Ask />}
      </div>
      {chatOpen && (
        <aside className="board-chat" style={{ width: CHAT_WIDTH }}>
          <header className="board-top drag">
            <span className="board-top-title">Agentman</span>
            <span className="board-spacer" />
            <span className="no-drag board-top-buttons">
              {manager.entries.length > 0 && (
                <ChromeButton icon={MessageSquarePlus} sf="plus.bubble" title="Start a new conversation with Agentman" onClick={() => void newChat()} />
              )}
              <ChromeButton icon={X} sf="xmark" title="Close the conversation" onClick={() => setBoardChatOpen(false)} />
            </span>
          </header>
          <AgentmanView inPanel />
        </aside>
      )}
      {committing && <BoardCommitSheet key={committing} sessionId={committing} close={() => openCommitSheet(null)} />}
    </div>
  );
}

/** One box to ask Agentman, floating over the columns' foot; sending opens
 *  the conversation beside them. */
function Ask() {
  const [draft, setDraft] = useState("");
  const [attachments, setAttachments] = useState<Attachment[]>([]);
  const [sending, setSending] = useState(false);
  const submit = () => {
    const message = draft.trim();
    if ((!message && attachments.length === 0) || sending) return;
    setSending(true);
    setBoardChatOpen(true);
    void (async () => {
      if (await askAgentman(message, await encoded(attachments))) {
        setDraft("");
        setAttachments([]);
      }
      setSending(false);
    })();
  };
  return (
    <div className="board-ask">
      <div className="board-ask-box">
        <ManagerBox text={draft} setText={setDraft} attachments={attachments} setAttachments={setAttachments} sending={sending} onSend={submit} />
      </div>
    </div>
  );
}

/** Show one folder's agents, as the sidebar's filter, which steps aside here. */
function FolderMenu() {
  const { folderFilter, folders } = useShell();
  useEffect(() => void loadFolders(), []);
  const open = (e: ReactMouseEvent<HTMLButtonElement>) => {
    menuUnder(e.currentTarget, [
      { label: "All Folders", checked: !folderFilter, action: () => setShell({ folderFilter: null }) },
      { separator: true },
      ...folders.slice(0, 15).map((f) => ({
        label: f.path.replace(/\/+$/, "").split("/").pop() || f.path,
        checked: folderFilter === f.path,
        action: () => setShell({ folderFilter: f.path }),
      })),
    ]);
  };
  return (
    <button className="board-folder-menu" title="Show one folder's agents" onClick={open}>
      <span>{folderFilter ? folderFilter.replace(/\/+$/, "").split("/").pop() : "All folders"}</span>
      <ChevronDown size={9} strokeWidth={2.5} />
    </button>
  );
}

function BoardColumn({ kind, sessions, outlined, found, offers = [], missions = [], hidden = [], clearance }: {
  kind: Kind;
  sessions: Session[];
  outlined: Set<string>;
  /** The search, when one is running. */
  found: BoardSearch | null;
  /** What Agentman offers the user, above the agents waiting on them. */
  offers?: ManagerCard[];
  /** The teams at work, above the agents doing their parts. */
  missions?: Mission[];
  hidden?: Session[];
  clearance: number;
}) {
  // Colour and motion only while the column holds agents or offers; the
  // words for an empty column only when it holds nothing at all.
  const quiet = sessions.length === 0 && offers.length === 0;
  const empty = quiet && missions.length === 0 && (kind !== "idle" || !found?.past.length);
  const [shipping, setShipping] = useState(false);
  const hiddenMenu = (e: ReactMouseEvent<HTMLButtonElement>) =>
    menuUnder(e.currentTarget, [
      ...hidden.map((s) => ({ label: s.name, action: () => showOnBoard(s.id) })),
      { separator: true },
      { label: "Show All", action: showAllOnBoard },
    ]);
  return (
    <section className="board-column">
      <div className="board-column-head">
        <span className="board-column-glyph">
          {kind === "needsYou" && !quiet ? <Icon icon={CircleAlert} sf="exclamationmark.circle.fill" size={13} className="needs" />
            : kind === "working" && !quiet ? <Loader size={11} />
            : <Icon icon={Circle} sf="circle" size={13} className="faint" />}
        </span>
        <span className="board-column-title">{TITLES[kind]}</span>
        <span className="board-column-count">{sessions.length + offers.length}</span>
        <span className="board-spacer" />
        {hidden.length > 0 && (
          <button className="board-column-action" title="Agents you hid. They come back by themselves when they need you." onClick={hiddenMenu}>
            <EyeOff size={10} />
            <span>{hidden.length} hidden</span>
          </button>
        )}
        {kind === "idle" && sessions.length > 0 && (
          <button className="board-column-action" title="Commit, push and open pull requests for finished agents" onClick={() => setShipping(true)}>
            <CircleArrowUp size={11} />
            <span>Ship</span>
          </button>
        )}
        {shipping && <ShipSheet sessions={sessions} close={() => setShipping(false)} />}
      </div>
      <div className="board-column-cards" style={{ paddingBottom: clearance }}>
        {missions.map((m) => <BoardMission key={m.id} mission={m} />)}
        {offers.map((offer) => (
          <div key={offer.id} className="board-offer">
            <CompactCard card={offer} close={() => dismissCard(offer)} />
          </div>
        ))}
        {sessions.map((s) => <BoardCard key={s.id} session={s} kind={kind} outlined={outlined.has(s.id)} match={found?.bySession[s.id]} query={found?.query ?? ""} />)}
        {kind === "idle" && found && found.past.length > 0 && <PastHits hits={found.past} query={found.query} />}
        {empty && <div className="board-column-empty">{found ? "No matches" : EMPTY[kind]}</div>}
      </div>
    </section>
  );
}

/** One agent: where it is, what it is called, and what it wants, answerable
 *  or followable on the spot. Clicking anywhere else opens its conversation. */
function BoardCard({ session, kind, outlined, match, query }: { session: Session; kind: Kind; outlined: boolean; match?: SearchHit; query: string }) {
  const [colorBy] = useColorBy();
  const tint = agentColor(session, colorBy);
  const { messages } = useStore();
  const manager = useManager();
  const problem = useActionError(session.id);
  useEffect(() => follow(session.id), [session.id]);
  const list = messages[session.id];
  const digest = useMemo(() => makeDigest(list ?? [], tidy), [list]);
  const brief = manager.briefs[session.id];
  const mission = missionOf(session.id, manager);
  // What a search found in its conversation, which a click opens at.
  const open = () => (match ? openHit(match, query) : openAgent(session.id));
  const when = kind === "working" ? digest.turnStarted : session.lastActivityAt;

  const items = (): MenuEntry[] => {
    const entries: MenuEntry[] = [{ label: "Open Conversation", action: open }];
    if (kind !== "needsYou") entries.push({ label: "Hide from the Board", action: () => hideFromBoard(session.id) });
    if (endable(session)) entries.push({ label: "End Session…", action: () => void confirmEnd(session) });
    entries.push({ label: "Trust This Folder", header: true });
    const level = trustLevel(session.cwd, manager.settings);
    for (const option of TRUST_LEVELS) {
      entries.push({ label: option.title, checked: option.id === level, action: () => void setTrust(option.id, session.cwd) });
    }
    if (kind === "idle") {
      entries.push({ separator: true });
      entries.push({ label: "Archive", action: () => archive(session.id, new Set([session.id])) });
    }
    return entries;
  };

  // A click on the card, not on a control in it, opens the conversation.
  const cardClick = (e: ReactMouseEvent<HTMLDivElement>) => {
    if ((e.target as HTMLElement).closest("button, input, textarea, a, .choices, .board-code")) return;
    open();
  };

  return (
    <div
      className={`board-card${outlined ? " outlined" : ""}${tint ? " tinted" : ""}`}
      style={tint ? ({ "--tint": tint } as React.CSSProperties) : undefined}
      onClick={cardClick}
      onContextMenu={(e) => contextMenu(e, items())}
    >
      <div className="board-card-top">
        <div className="board-card-meta">
          <FolderMark session={session} size={14} />
          <span className="board-card-folder">{folderLabel(session.cwd)}</span>
          <span className="board-spacer" />
          {when ? <span className="board-card-when">{since(when)}</span> : null}
          <span className="board-card-mark">
            <span className="mark-at-rest"><AgentMark kind={session.kind} size={13} /></span>
            <button className="board-card-more" title="More for this agent" aria-label={`More for ${session.name}`} onClick={(e) => menuUnder(e.currentTarget, items())}>
              <Ellipsis size={13} strokeWidth={2.5} />
            </button>
          </span>
        </div>
        {mission && <div className="board-card-mission">{mission.goal}</div>}
        <button className="board-card-name" title={`Open ${session.name}`} onClick={open}>{session.name}</button>
        {match && <div className="board-card-match" title={match.snippet}><Snippet hit={match} query={query} /></div>}
      </div>
      {kind === "needsYou" && <NeedsYou session={session} digest={digest} />}
      {kind === "working" && <Working digest={digest} summary={brief?.summary || brief?.goal} now={brief?.now} />}
      {kind === "idle" && <Idle session={session} said={brief?.summary || digest.said} />}
      {problem && <div className="board-card-problem">{problem}</div>}
    </div>
  );
}

function answer(session: Session, question: Question, reply: QuestionReply): Promise<boolean> {
  if (reply.optionKeys?.length) return perform(session.id, { type: "answer_question", questionId: question.id, optionKeys: reply.optionKeys });
  return perform(session.id, {
    type: "answer_question", questionId: question.id, optionKey: reply.optionKey,
    ...(reply.text ? { answerText: reply.text } : {}),
  });
}

function NeedsYou({ session, digest }: { session: Session; digest: BoardDigest }) {
  const manager = useManager();
  const brief = manager.briefs[session.id];
  const card = openCardsOf(manager).find((c) => c.sessionId === session.id && (c.kind === "question" || c.kind === "needs_you"));
  const summary = brief?.summary;
  const question = session.question;
  if (question) {
    return (
      <>
        {summary && <BoardText text={summary} lines={3} />}
        <BoardText text={question.title || "Needs your answer"} lines={3} />
        {question.detail && <div className="board-code code-box">{question.detail}</div>}
        <QuestionNotice question={question} />
        {question.prompt && !promptRepeatsDetail(question) && <BoardText text={question.prompt} lines={4} />}
        <div className="board-choices">
          <QuestionChoices
            key={question.id ?? question.prompt}
            question={question}
            suggested={card?.questionId === question.id ? card?.suggested : undefined}
            roomy
            answer={(reply) => answer(session, question, reply)}
          />
        </div>
      </>
    );
  }
  if (card) {
    const asked = cardQuestion(card);
    return (
      <>
        <BoardText text={card.title} lines={3} />
        {asked ? (
          <div className="board-choices">
            <QuestionChoices key={card.id} question={asked} suggested={card.suggested} typedPrompt="Or reply to Agentman" roomy
              answer={async (reply) => (await replyToCard(card, reply)) === undefined} />
          </div>
        ) : (
          <BoardReply placeholder="Reply to Agentman" send={async (text) => (await replyToCard(card, { text })) === undefined} />
        )}
      </>
    );
  }
  // Its question is at the end of what it said, and the brief says what
  // it's all about.
  const need = brief?.needsYou || digest.asked || "Waiting for your answer";
  return (
    <>
      <BoardText text={need} lines={5} />
      {summary && summary !== need && <BoardText text={summary} lines={3} />}
      {session.inject && session.inject !== "none" && (
        <BoardReply placeholder={`Reply to ${session.name}`} send={(text) => sendToAgent(session.id, text)} />
      )}
    </>
  );
}

function Working({ digest, summary, now }: { digest: BoardDigest; summary?: string; now?: string }) {
  return (
    <>
      {summary && <BoardText text={summary} lines={3} />}
      {now && now !== summary && <BoardText text={now} lines={2} />}
      {digest.steps.length > 0 && (
        <div className="board-steps">{digest.steps.map((step) => <Step key={step.id} step={step} />)}</div>
      )}
      {digest.progress && <Progress progress={digest.progress} />}
      {digest.command && <div className="board-command" title={digest.command}>{digest.command}</div>}
    </>
  );
}

function Idle({ session, said }: { session: Session; said?: string }) {
  const check = useManager().verifications[session.id];
  const run = useGitRun(session.id);
  const changedHere = useGitChanged(session.id);
  const [state, setState] = useState<GitState | null>(null);
  // What is left to commit, read again whenever it did something and
  // whenever a button here has finished.
  const settled = run && run.phase.kind !== "running" ? run.id : 0;
  useEffect(() => {
    let current = true;
    git("git_status", session.id).then((s) => current && setState(s)).catch(() => current && setState(null));
    return () => {
      current = false;
    };
  }, [session.id, session.lastActivityAt, settled, changedHere]);

  const action = quickAction(state);
  const showsGit = !!run || (!!state && action.kind !== "idle");
  const id = session.id;
  const opening = state && state.changes > 0
    ? [{ title: state.changes === 1 ? "Review 1 file" : `Review ${state.changes} files`, icon: Diff, sf: "plus.forwardslash.minus", onClick: () => {
        showPanel("diff");
        openAgent(id);
      } }]
    : [];
  const elsewhere: { title: string; icon: typeof Code; sf: string; onClick: () => void }[] = [];
  if (session.cwd) elsewhere.push({ title: "View", icon: Code, sf: "chevron.left.forwardslash.chevron.right", onClick: () => void openEditor({ folder: session.cwd }) });
  const documents = session.artifactsToReview ?? 0;
  if (documents > 0) {
    elsewhere.push({ title: documents === 1 ? "Review document" : `Review ${documents} documents`, icon: FileText, sf: "doc.text", onClick: () => {
      openCardTab("plans");
      openAgent(id);
    } });
  }
  for (const server of (session.servers ?? []).slice(0, 2)) {
    elsewhere.push({ title: `localhost:${server.port}`, icon: Globe, sf: "globe", onClick: () => void openURL(`http://localhost:${server.port}`) });
  }
  return (
    <>
      {said && <BoardText text={said} lines={4} />}
      {check && <BoardCheck check={check} />}
      {(showsGit || opening.length > 0 || elsewhere.length > 0) && (
        <div className="board-links">
          {opening.map((l) => <BoardLink key={l.title} {...l} />)}
          {showsGit && <BoardGitChip sessionId={id} action={action} state={state} onCommit={() => openCommitSheet(id)} />}
          {elsewhere.map((l) => <BoardLink key={l.title} {...l} />)}
        </div>
      )}
      {run?.phase.kind === "failed" && <div className="board-git-failed" title={run.phase.detail}>{run.phase.detail}</div>}
    </>
  );
}

/** A team at work: its goal, how far along it is, and who is waiting. */
function BoardMission({ mission }: { mission: Mission }) {
  return (
    <div className="board-mission">
      <div className="board-mission-goal">{mission.goal}</div>
      <div className="board-mission-progress">{missionProgress(mission)}</div>
      {mission.tasks.filter((t) => t.state === "planned").map((t) => (
        <div key={t.id} className="board-mission-task"><Clock size={9} /><span>{t.title}</span></div>
      ))}
    </div>
  );
}

const CHECK_WORDS: Record<string, string> = {
  verified: "Verified", passed: "Tests pass", concerns: "Something looks off", failed: "Tests fail",
};

/** What Agentman found checking the agent's finished work. */
function BoardCheck({ check }: { check: Verification }) {
  const words = check.summary || CHECK_WORDS[check.state] || "Checking its work";
  const good = check.state === "verified" || check.state === "passed";
  return (
    <div className={`board-check ${check.state}`} title={check.output || words}>
      <div className="board-check-line">
        <span className="board-check-icon">
          {good ? <BadgeCheck size={11} /> : check.state === "concerns" ? <TriangleAlert size={10} />
            : check.state === "failed" ? <CircleX size={11} /> : <Loader size={10} />}
        </span>
        <span className="board-check-words">{inline(words)}</span>
      </div>
      {(check.issues ?? []).map((issue) => <div key={issue} className="board-check-issue">{inline(issue)}</div>)}
      {check.sentBack && <div className="board-check-issue muted">Sent back for it to fix</div>}
    </div>
  );
}

/** Text on a card, cut to a few lines with the rest a click away. */
function BoardText({ text, lines }: { text: string; lines: number }) {
  const [open, setOpen] = useState(false);
  // About how many characters fill a line of a column.
  const long = text.length > lines * 58;
  return (
    <div className="board-text-wrap">
      <div className="board-text" style={open ? undefined : { WebkitLineClamp: lines }} title={open ? undefined : text}>{inline(text)}</div>
      {long && <button className="board-more" onClick={() => setOpen(!open)}>{open ? "Less" : "More"}</button>}
    </div>
  );
}

/** Markdown's inline marks taken off, as CompactCard.inline does. */
function inline(text: string): string {
  return text.replace(/\*\*([^*]+)\*\*/g, "$1").replace(/(^|\W)[*_]([^*_]+)[*_](?=\W|$)/g, "$1$2").replace(/`([^`]+)`/g, "$1").replace(/\[([^\]]+)\]\([^)]+\)/g, "$1");
}

function Step({ step }: { step: BoardStep }) {
  return (
    <div className={`board-step ${step.state}`} title={step.text}>
      <span className="board-step-icon">
        {step.state === "running" ? <Loader size={10} /> : step.state === "failed" ? <X size={10} strokeWidth={3} /> : <Check size={10} strokeWidth={3} />}
      </span>
      <span className="board-step-text">{step.text}</span>
    </div>
  );
}

function Progress({ progress }: { progress: BoardProgress }) {
  const line = `${progress.done} of ${progress.total}` + (progress.now ? ` · ${progress.now}` : "");
  return (
    <div className="board-progress" aria-label={line}>
      <div className="board-progress-bar"><span style={{ width: `${(100 * progress.done) / Math.max(1, progress.total)}%` }} /></div>
      <div className="board-progress-line">{line}</div>
    </div>
  );
}

function BoardReply({ placeholder, send }: { placeholder: string; send: (text: string) => Promise<boolean> }) {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const typed = text.trim() !== "";
  const reply = async () => {
    const words = text.trim();
    if (!words || busy) return;
    setBusy(true);
    try {
      if (await send(words)) setText("");
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="board-reply">
      <input
        value={text}
        placeholder={placeholder}
        disabled={busy}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && !e.nativeEvent.isComposing) void reply();
        }}
      />
      <button className={typed ? "can" : ""} disabled={!typed || busy} aria-label="Send" onClick={() => void reply()}>
        <svg width="9" height="9" viewBox="0 0 10 10" aria-hidden><path d="M5 9V1M1.5 4.5 5 1l3.5 3.5" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" /></svg>
      </button>
    </div>
  );
}

/** Conversations that have ended and said what was searched for, at the
 *  foot of the last column: not on the Board, but what was asked. */
function PastHits({ hits, query }: { hits: SearchHit[]; query: string }) {
  return (
    <div className="board-past">
      <div className="board-past-title">In past conversations</div>
      {hits.slice(0, 8).map((hit) => (
        <button key={`${hit.session?.id}-${hit.messageId}`} className="board-past-hit" title={hit.snippet} onClick={() => openHit(hit, query)}>
          <span className="board-past-name">{hit.session?.name ?? "Conversation"}</span>
          <span className="board-past-snippet"><Snippet hit={hit} query={query} /></span>
        </button>
      ))}
    </div>
  );
}

/** The sidebar's search, as the sidebar steps aside here: agents by name,
 *  folder or branch, and everything said in their conversations. ⌘F. */
function SearchField({ width }: { width: number }) {
  const { search } = useShell();
  const [focused, setFocused] = useState(false);
  const field = useRef<HTMLInputElement>(null);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "f") {
        e.preventDefault();
        field.current?.focus();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  return (
    <label className={`board-search${focused ? " focused" : ""}`} style={{ width }}>
      <Search size={13} strokeWidth={2.2} />
      <input
        ref={field}
        value={search}
        placeholder="Search agents and messages"
        aria-label="Search agents and messages"
        spellCheck={false}
        onFocus={() => setFocused(true)}
        onBlur={() => setFocused(false)}
        onChange={(e) => setShell({ search: e.target.value })}
        onKeyDown={(e) => {
          if (e.key === "Escape") {
            setShell({ search: "" });
            field.current?.blur();
          }
        }}
      />
      {search ? (
        <button className="board-search-clear" title="Clear the search" aria-label="Clear the search" onClick={() => setShell({ search: "" })}>
          <XCircle size={13} />
        </button>
      ) : !focused ? <span className="board-search-key">⌘F</span> : null}
    </label>
  );
}

/** Colour agents by project or folder. */
function ColorMenu() {
  const [colorBy, setColorBy] = useColorBy();
  return (
    <button
      className={`board-top-button${colorBy === "none" ? "" : " on"}`}
      title="Color agents by project or folder"
      aria-label="Color agents by"
      onClick={(e) => menuUnder(e.currentTarget, COLOR_BY.map((c) => ({ label: c.title, checked: c.id === colorBy, action: () => setColorBy(c.id) })))}
    >
      <Palette size={12} fill={colorBy === "none" ? "none" : "currentColor"} />
    </button>
  );
}

/** What Agentman approved by itself today, newest first (handledToday). */
function handledToday(entries: ManagerEntry[]): ManagerEntry[] {
  const start = new Date();
  start.setHours(0, 0, 0, 0);
  return entries.filter((e) => e.tool === "approve" && e.ts >= start.getTime()).reverse();
}

function HandledButton() {
  const manager = useManager();
  const handled = handledToday(manager.entries);
  const [open, setOpen] = useState(false);
  const button = useRef<HTMLButtonElement>(null);
  if (!handled.length) return null;
  return (
    <span className="board-tonight">
      <button ref={button} className="board-handled" title="What Agentman approved for you today" onClick={() => setOpen(!open)}>
        <CircleCheck size={11} />
        <span>{handled.length} handled</span>
      </button>
      {open && <HandledPanel anchor={button.current} entries={handled} close={() => setOpen(false)} />}
    </span>
  );
}

function HandledPanel({ anchor, entries, close }: { anchor: HTMLElement | null; entries: ManagerEntry[]; close: () => void }) {
  useKeyboardBack();
  const panel = useRef<HTMLDivElement>(null);
  const [asked, setAsked] = useState<Set<string>>(new Set());
  const manager = useManager();
  useEffect(() => {
    const down = (e: MouseEvent) => {
      if (!panel.current?.contains(e.target as Node) && !anchor?.contains(e.target as Node)) close();
    };
    const key = (e: KeyboardEvent) => e.key === "Escape" && close();
    document.addEventListener("mousedown", down);
    window.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("mousedown", down);
      window.removeEventListener("keydown", key);
    };
  }, [anchor, close]);
  const box = anchor?.getBoundingClientRect();
  const left = box ? Math.max(8, Math.min(window.innerWidth - 408, box.left + box.width / 2 - 200)) : 8;
  // Ask next time: a rule that asks, for what it approved (alwaysAsk).
  const alwaysAsk = async (entry: ManagerEntry) => {
    const settings = manager.settings;
    const approved = entry.approved;
    if (!settings || !approved) return;
    const rule = { action: "ask", kind: approved.kind, pattern: approved.target, ...(approved.folder ? { folder: approved.folder } : {}) };
    const rules = settings.rules ?? [];
    const known = rules.some((r) => r.action === rule.action && r.kind === rule.kind && r.pattern === rule.pattern && (r.folder ?? "") === (rule.folder ?? ""));
    if (!(await saveSettings({ ...settings, rules: known ? rules : [...rules, rule] }))) setAsked(new Set([...asked, entry.id]));
  };
  return (
    <div className="board-popover board-handled-panel" ref={panel} style={{ left, top: box ? box.bottom + 8 : 40 }} role="dialog" aria-label="Approved for you today">
      <div className="board-popover-title">Approved for you today</div>
      {entries.map((entry) => (
        <div key={entry.id} className="board-handled-row">
          <span className="board-handled-text">{inline(entry.text)}</span>
          {entry.approved && (
            <button className="board-handled-ask" disabled={asked.has(entry.id)} onClick={() => void alwaysAsk(entry)}>
              {asked.has(entry.id) ? "Will ask" : "Ask next time"}
            </button>
          )}
        </div>
      ))}
    </div>
  );
}

/** How much Agentman does by itself in a folder (FolderTrust). */
const TRUST_LEVELS = [
  { id: "watch", title: "Watch" },
  { id: "suggest", title: "Suggest" },
  { id: "handle", title: "Handle" },
];

/** The level that applies to a folder: the closest entry, or what the
 *  approving switch means where there is none. */
function trustLevel(folder: string, settings?: { trust?: { folder: string; level: string }[]; autoApprove: boolean }): string {
  if (!settings) return "watch";
  const home = cachedHome();
  const expanded = (p: string) => (p.startsWith("~") && home ? home + p.slice(1) : p);
  const closest = (settings.trust ?? [])
    .filter((t) => folder === expanded(t.folder) || folder.startsWith(expanded(t.folder) + "/"))
    .sort((a, b) => expanded(b.folder).length - expanded(a.folder).length)[0];
  return closest?.level ?? (settings.autoApprove ? "handle" : "watch");
}

async function setTrust(level: string, folder: string) {
  const settings = getManager().settings;
  if (!settings) return;
  const trust = (settings.trust ?? []).filter((t) => t.folder !== folder);
  trust.push({ folder, level });
  await saveSettings({ ...settings, trust });
}

export type { BoardSections };
