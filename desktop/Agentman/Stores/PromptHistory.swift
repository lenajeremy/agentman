import Foundation

/// ↑ and ↓ in an agent's message box, as in a terminal: back through what
/// was said to the agent, newest first, and forward again to the message
/// being written.
///
/// It starts from the thread's own messages, already loaded, and asks the
/// daemon for older ones only when ↑ goes past them, a page of prompts at a
/// time, never the whole conversation. What it holds is let go once the box
/// is back on the draft, the message is sent, or the words are changed.
@MainActor
final class PromptHistory {
    /// Prompts newest first, with where the thread or the daemon can read
    /// further back from.
    struct Prompts {
        var prompts: [String]
        var cursor: String?
        var hasMore: Bool
    }

    /// The agent whose prompts these are.
    var sessionId: String? {
        didSet { if sessionId != oldValue { reset() } }
    }
    /// The prompts in the agent's thread as loaded, and where its older
    /// pages start.
    var loaded: (String) -> Prompts = { _ in Prompts(prompts: [], cursor: nil, hasMore: false) }
    /// Older prompts from before a cursor; nil when the daemon can't say.
    var fetch: (String, String) async -> Prompts? = { _, _ in nil }
    /// How many pages of prompts one ↑ asks for before giving up, for a long
    /// stretch in which the user said nothing.
    static let fetchesPerStep = 4

    /// The prompts as far back as ↑ has gone, newest first.
    private(set) var prompts: [String] = []
    /// Which of them the box shows; nil while it shows the draft.
    private(set) var position: Int?
    /// What the box held before ↑, given back after the newest prompt.
    private(set) var draft = ""
    /// The words the box was given, as it reads them back: anything else
    /// there means the user changed them, and the history starts again.
    private(set) var shown: String?
    private var cursor: String?
    private var hasMore = false
    private var fetching: Task<Void, Never>?

    var isBrowsing: Bool { position != nil }

    /// Whether the box holds a prompt from the history, as it was given.
    func shows(_ text: String) -> Bool { position != nil && text == shown }

    /// The box shows text it was given: read back as the box reads it.
    func showing(_ text: String) { shown = text }

    /// ↑ with text in the box: the prompt before the one shown, or the newest
    /// one before the draft, handed to show now or once the daemon has read
    /// further back. False when there is nothing further back, so the key
    /// does what it does anyway.
    func older(than text: String, show: @escaping (String) -> Void) -> Bool {
        guard let sessionId else { return false }
        if position != nil, text != shown { reset() }
        if position == nil {
            let start = loaded(sessionId)
            prompts = []
            add(start.prompts)
            cursor = start.cursor
            hasMore = start.hasMore
            draft = text
        }
        let next = (position ?? -1) + 1
        if next < prompts.count {
            position = next
            show(prompts[next])
            return true
        }
        guard hasMore, cursor != nil else {
            if position == nil { reset() }
            return false
        }
        // Asked already: the answer moves the box when it comes.
        guard fetching == nil else { return true }
        let from = position
        fetching = Task { [weak self] in
            var tries = 0
            while let self, self.sessionId == sessionId, self.position == from, next >= self.prompts.count,
                  self.hasMore, let cursor = self.cursor, tries < Self.fetchesPerStep, !Task.isCancelled {
                tries += 1
                guard let more = await self.fetch(sessionId, cursor), !Task.isCancelled else {
                    self.hasMore = false
                    break
                }
                guard self.sessionId == sessionId, self.position == from else { break }
                self.add(more.prompts)
                self.cursor = more.cursor
                self.hasMore = more.hasMore
            }
            guard let self, !Task.isCancelled else { return }
            fetching = nil
            guard self.sessionId == sessionId, position == from else { return }
            if next < prompts.count {
                position = next
                show(prompts[next])
            } else if position == nil {
                reset()
            }
        }
        return true
    }

    /// ↓: the prompt after the one shown, or the draft after the newest.
    /// False when the box is on the draft already, or its words were
    /// changed, so the key does what it does anyway.
    func newer(than text: String, show: (String) -> Void) -> Bool {
        guard let position else { return false }
        guard text == shown else {
            reset()
            return false
        }
        if position == 0 {
            let draft = draft
            reset()
            show(draft)
        } else {
            self.position = position - 1
            show(prompts[position - 1])
        }
        return true
    }

    /// Back on the draft, holding nothing.
    func reset() {
        fetching?.cancel()
        fetching = nil
        prompts = []
        position = nil
        draft = ""
        shown = nil
        cursor = nil
        hasMore = false
    }

    /// Older prompts after those held, a prompt said twice in a row once.
    private func add(_ older: [String]) {
        for prompt in older where prompt != prompts.last {
            prompts.append(prompt)
        }
    }

    /// A message the user wrote to the agent, for its history: theirs, with
    /// words, not a tool's result, a subagent's, or the summary of an
    /// earlier conversation the agent writes for itself.
    static func isPrompt(_ message: Message) -> Bool {
        guard message.role == .user, message.tool == nil, message.isSidechain != true,
              let text = message.text, !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return false }
        return !MessageRow.isCompactionSummary(text)
    }
}
