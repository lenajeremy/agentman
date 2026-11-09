import AppKit
import SwiftUI

/// A passage of what an agent said, quoted in the reply to it.
struct ReplyQuote: Identifiable, Equatable {
    let id = UUID()
    let text: String
}

/// How a quoted passage travels: as a Markdown quote ahead of the words,
/// which every agent reads as what the message is about, and which comes
/// back in the transcript as it went.
nonisolated enum ReplyQuotes {
    /// Agentman's own place among the quotes kept by session.
    static let agentman = "agentman"

    static func message(_ quotes: [String], words: String) -> String {
        (quotes.map(block) + (words.isEmpty ? [] : [words])).joined(separator: "\n\n")
    }

    private static func block(_ quote: String) -> String {
        quote.split(separator: "\n", omittingEmptySubsequences: false)
            .map { $0.isEmpty ? ">" : "> " + $0 }
            .joined(separator: "\n")
    }

    /// The quotes a message opens with and the words after them. Quotes end
    /// at a blank line; anything else leaves the message as it is.
    static func split(_ message: String) -> (quotes: [String], words: String) {
        let lines = message.components(separatedBy: "\n")
        var quotes: [String] = []
        var current: [String] = []
        var index = 0
        while index < lines.count {
            let line = lines[index]
            if line == ">" || line.hasPrefix("> ") {
                current.append(line == ">" ? "" : String(line.dropFirst(2)))
                index += 1
            } else if line.isEmpty, !current.isEmpty {
                quotes.append(current.joined(separator: "\n"))
                current = []
                index += 1
            } else {
                break
            }
        }
        if !current.isEmpty, index == lines.count {
            quotes.append(current.joined(separator: "\n"))
            current = []
        }
        guard current.isEmpty, !quotes.isEmpty else { return ([], message) }
        return (quotes, lines[index...].joined(separator: "\n"))
    }
}

// MARK: - Offering to quote what's selected

/// Text selected in what an agent said, and where to offer to quote it.
struct QuoteOffer: Equatable {
    let text: String
    let origin: CGPoint
}

extension View {
    /// Marks an agent's message as text that can be quoted.
    func quotable() -> some View {
        background(QuotableMarker())
    }

    /// Offers to quote text selected in the `quotable` messages within.
    func offersQuotes(when enabled: Bool, add: @escaping (String) -> Void) -> some View {
        modifier(QuoteOffering(enabled: enabled, add: add))
    }
}

/// Reaches the watcher's view from the button over it.
@MainActor
private final class WatcherHandle {
    weak var view: SelectionWatcherView?
}

private struct QuoteOffering: ViewModifier {
    let enabled: Bool
    let add: (String) -> Void
    @State private var offer: QuoteOffer?
    @State private var watcher = WatcherHandle()

    func body(content: Content) -> some View {
        content
            .background(SelectionWatcher(enabled: enabled, handle: watcher) { offer = $0 })
            .overlay(alignment: .topLeading) {
                ZStack(alignment: .topLeading) {
                    if let offer {
                        QuoteButton {
                            add(offer.text)
                            watcher.view?.dismiss()
                        }
                        .offset(x: offer.origin.x, y: offer.origin.y)
                        .transition(.opacity)
                    }
                }
                .animation(.easeOut(duration: 0.1), value: offer)
            }
    }
}

/// The small chip that floats under a selection, in the look of the
/// Board's.
private struct QuoteButton: View {
    static let size = CGSize(width: 68, height: 24)
    private static let shape = RoundedRectangle(cornerRadius: 6, style: .continuous)
    let action: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 4) {
                Image(systemName: "arrowshape.turn.up.left")
                    .font(.system(size: 9.5))
                    .foregroundStyle(Palette.muted)
                Text("Reply")
                    .font(Fonts.sans(12))
                    .foregroundStyle(hovering ? Palette.text : Palette.textSecondary)
            }
            .frame(width: Self.size.width, height: Self.size.height)
            .background(hovering ? Palette.fill : Palette.surface, in: Self.shape)
            .overlay(Self.shape.strokeBorder(Palette.line))
            .contentShape(Self.shape)
        }
        .buttonStyle(.plain)
        .shadow(color: .black.opacity(0.07), radius: 4, y: 1)
        .onHover { hovering = $0 }
        .help("Reply about this part")
    }
}

private struct QuotableMarker: NSViewRepresentable {
    func makeNSView(context: Context) -> QuotableMarkerView { QuotableMarkerView() }
    func updateNSView(_ view: QuotableMarkerView, context: Context) {}
}

/// An empty view behind an agent's message: what says a selection is in one.
final class QuotableMarkerView: NSView {
    override func hitTest(_ point: NSPoint) -> NSView? { nil }
}

private struct SelectionWatcher: NSViewRepresentable {
    let enabled: Bool
    let handle: WatcherHandle
    let offered: (QuoteOffer?) -> Void

    func makeNSView(context: Context) -> SelectionWatcherView {
        let view = SelectionWatcherView()
        handle.view = view
        return view
    }

    func updateNSView(_ view: SelectionWatcherView, context: Context) {
        view.enabled = enabled
        view.offered = offered
    }
}

/// Watches the window for a selection ending. SwiftUI's selectable text
/// says nothing about its selection, but the view holding it is the first
/// responder and answers as any text does to accessibility, which leaves
/// the clipboard alone.
final class SelectionWatcherView: NSView {
    var enabled = true {
        didSet { if oldValue, !enabled { show(nil) } }
    }
    var offered: (QuoteOffer?) -> Void = { _ in }
    private(set) var offer: QuoteOffer?
    private var monitor: Any?

    override var isFlipped: Bool { true }
    override func hitTest(_ point: NSPoint) -> NSView? { nil }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        if let monitor { NSEvent.removeMonitor(monitor) }
        monitor = nil
        guard window != nil else { return }
        monitor = NSEvent.addLocalMonitorForEvents(matching: [.leftMouseDown, .leftMouseUp, .scrollWheel, .keyDown, .keyUp]) { [weak self] event in
            self?.handle(event)
            return event
        }
    }

    func handle(_ event: NSEvent) {
        guard enabled, event.window === window else { return }
        switch event.type {
        case .leftMouseDown:
            if !overButton(event) { show(nil) }
        case .leftMouseUp:
            guard !overButton(event) else { return }
            // Once the text has had the mouse up, and its selection is final.
            let point = event.locationInWindow
            DispatchQueue.main.async { [weak self] in self?.read(near: point) }
        case .scrollWheel, .keyDown:
            show(nil)
        case .keyUp where !event.modifierFlags.isDisjoint(with: [.shift, .command]):
            DispatchQueue.main.async { [weak self] in self?.read(near: nil) }
        default:
            break
        }
    }

    /// Takes the offer back, once it has been used.
    func dismiss() {
        show(nil)
    }

    /// Offers the selection, if there is one in a message here.
    func read(near point: NSPoint?) {
        guard let window, let selected = Self.selection(in: window) else { return show(nil) }
        let source = selected.view.convert(selected.view.bounds, to: nil)
        guard source.intersects(convert(bounds, to: nil)), Self.isQuotable(source, in: window) else { return show(nil) }
        let anchor = point.map { convert($0, from: nil) }
            ?? convert(NSPoint(x: source.midX, y: source.minY), from: nil)
        show(QuoteOffer(text: selected.text, origin: origin(below: anchor)))
    }

    /// What is selected in the window, wherever that is: never what is
    /// being typed.
    static func selection(in window: NSWindow) -> (text: String, view: NSView)? {
        guard let view = window.firstResponder as? NSView else { return nil }
        if let field = view as? NSTextView, field.isEditable { return nil }
        let text = (view.accessibilitySelectedText() ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        return text.isEmpty ? nil : (text, view)
    }

    private static func isQuotable(_ source: NSRect, in window: NSWindow) -> Bool {
        guard let root = window.contentView else { return false }
        var stack = [root]
        while let view = stack.popLast() {
            if view is QuotableMarkerView, view.convert(view.bounds, to: nil).intersects(source) { return true }
            stack.append(contentsOf: view.subviews)
        }
        return false
    }

    /// Under the point, so what is being read above it stays in view; over
    /// it only where there is no room below.
    private func origin(below point: NSPoint) -> CGPoint {
        let size = QuoteButton.size
        let x = min(max(point.x - size.width / 2, 8), max(bounds.width - size.width - 8, 8))
        let y = point.y + 14
        return CGPoint(x: x, y: y + size.height > bounds.height - 4 ? point.y - size.height - 12 : y)
    }

    private func overButton(_ event: NSEvent) -> Bool {
        guard let offer else { return false }
        return NSRect(origin: offer.origin, size: QuoteButton.size)
            .insetBy(dx: -4, dy: -4)
            .contains(convert(event.locationInWindow, from: nil))
    }

    private func show(_ next: QuoteOffer?) {
        guard next != offer else { return }
        offer = next
        offered(next)
    }
}

// MARK: - Showing a quote

/// A quote's rule, the same one a quote in an agent's message has.
private struct QuoteRule: View {
    var body: some View {
        RoundedRectangle(cornerRadius: 1.5)
            .fill(Palette.faint)
            .frame(width: 3)
    }
}

/// The passages waiting above the words in a message box.
struct QuoteStrip: View {
    @Binding var quotes: [ReplyQuote]

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            ForEach(quotes) { row($0) }
        }
    }

    private func row(_ quote: ReplyQuote) -> some View {
        HStack(alignment: .top, spacing: 8) {
            QuoteRule()
            Text(quote.text)
                .font(Fonts.sans(12.5))
                .foregroundStyle(Palette.textSecondary)
                .lineLimit(2)
                .truncationMode(.tail)
                .frame(maxWidth: .infinity, alignment: .leading)
                .help(quote.text)
            Button {
                withAnimation(.easeOut(duration: 0.12)) {
                    quotes.removeAll { $0.id == quote.id }
                }
            } label: {
                Image(systemName: "xmark")
                    .font(.system(size: 8, weight: .bold))
                    .foregroundStyle(Palette.muted)
                    .frame(width: 16, height: 16)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help("Remove")
            .accessibilityLabel("Stop replying to this")
        }
        .fixedSize(horizontal: false, vertical: true)
        .padding(.leading, 8)
        .padding(.trailing, 5)
        .padding(.vertical, 7)
        .background(Palette.fill.opacity(0.7), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Replying to: \(quote.text)")
    }
}

/// A sent message: the passages it quotes, set apart, over its words.
struct QuotedMessage<Words: View>: View {
    let text: String
    @ViewBuilder let words: (String) -> Words

    var body: some View {
        let reply = ReplyQuotes.split(text)
        if reply.quotes.isEmpty {
            words(text)
        } else {
            VStack(alignment: .leading, spacing: 10) {
                ForEach(Array(reply.quotes.enumerated()), id: \.offset) { _, quote in
                    SentQuote(text: quote)
                }
                if !reply.words.isEmpty {
                    words(reply.words)
                }
            }
        }
    }
}

private struct SentQuote: View {
    let text: String
    @State private var open = false

    private var long: Bool { text.count > 260 || text.filter(\.isNewline).count > 3 }

    var body: some View {
        HStack(alignment: .top, spacing: 9) {
            QuoteRule()
            VStack(alignment: .leading, spacing: 3) {
                Text(text)
                    .font(Fonts.sans(13))
                    .foregroundStyle(Palette.muted)
                    .lineSpacing(3)
                    .lineLimit(long && !open ? 4 : nil)
                    .textSelection(.enabled)
                if long {
                    Button(open ? "Show less" : "Show more") {
                        withAnimation(.easeOut(duration: 0.15)) { open.toggle() }
                    }
                    .buttonStyle(.link)
                    .font(TextStyle.caption)
                }
            }
        }
        .fixedSize(horizontal: false, vertical: true)
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Replying to: \(text)")
    }
}
