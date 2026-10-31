import SwiftUI

/// A conversation that follows its end only while the reader is there, and
/// loads what came before as they reach its top.
///
/// New messages and streamed text keep the view at the bottom when it is
/// already at the bottom. Once the user scrolls up to read, growth anchors to
/// the top instead, so what they are reading stays where it is, and
/// `following.atEnd` turns false for the view's own follow-along to check.
///
/// With `earlier` set, scrolling near the top loads the messages before the
/// first one shown. They arrive above it, and it stays exactly where the
/// reader had it; the view marks it with `firstMessage(_:if:)`.
struct FollowsEnd: ViewModifier {
    let following: Following
    /// Loads earlier messages, when there are any to load.
    var earlier: (() async -> Void)? = nil
    /// Set while earlier messages arrive. SwiftUI scrolls back to the end
    /// when rows are inserted at the top of a view that started there, so
    /// that start is let go of meanwhile.
    @State private var prepending = false
    /// Set once the reader has left the end: the conversation has opened,
    /// and "start at the bottom" no longer applies. Left in force, it pulled
    /// the reader back to the end when rows near the top were first laid
    /// out and the conversation grew.
    @State private var leftEnd = false
    @State private var position = ScrollPosition()
    @State private var first = FirstMessage()

    /// How close to the bottom still counts as there: a line or two, so a
    /// small nudge while reading the last message doesn't stop the follow.
    static let slack: CGFloat = 80
    /// How close to the top starts loading what came before: early enough
    /// that it is usually there before the reader arrives.
    static let topSlack: CGFloat = 160
    /// The scroll view's own space, which the first message is measured in.
    static let space = "FollowsEnd"

    func body(content: Content) -> some View {
        content
            .environment(\.firstMessage, first)
            .coordinateSpace(.named(Self.space))
            .scrollPosition($position)
            .defaultScrollAnchor(prepending || leftEnd ? nil : .bottom, for: .initialOffset)
            .defaultScrollAnchor(following.atEnd ? .bottom : .top, for: .sizeChanges)
            .onScrollGeometryChange(for: Edge.self) { geometry in
                Edge(height: geometry.contentSize.height, viewport: geometry.containerSize.height,
                     atEnd: geometry.visibleRect.maxY >= geometry.contentSize.height - Self.slack,
                     nearTop: geometry.contentSize.height > 0 && geometry.visibleRect.minY <= Self.topSlack)
            } action: { old, new in
                // Only the reader leaves the end: growth that lands before
                // the view catches up mustn't stop it following.
                var follows = following.atEnd
                if new.atEnd {
                    follows = true
                } else if new.height == old.height {
                    follows = false
                }
                if follows != following.atEnd { following.atEnd = follows }
                if !follows, !leftEnd { leftEnd = true }

                first.viewport = new.viewport
                first.nearTop = new.nearTop
                first.short = new.height > 0 && new.height <= new.viewport
                // The reader, scrolled up, arriving at the top; never a view
                // passing through it as it opens at the end.
                if new.nearTop, !old.nearTop, !follows {
                    loadEarlier()
                }
                // Messages too few to scroll have no top to arrive at. A
                // conversation being laid out looks that way for a moment,
                // so it has to stay short to count.
                if first.short {
                    first.settling?.cancel()
                    first.settling = Task {
                        try? await Task.sleep(for: .milliseconds(500))
                        if !Task.isCancelled, first.short { loadEarlier() }
                    }
                }
            }
            // Still too short to scroll once the last page is in.
            .onChange(of: prepending) { _, now in
                if !now, first.short { loadEarlier() }
            }
    }

    private func loadEarlier() {
        guard let earlier, !prepending else { return }
        prepending = true
        Task {
            await earlier()
            // The page is in, not yet laid out: the first message was where
            // it was last drawn. Put it back there in the same update.
            if let scroll = first.scroll, let frame = first.frame, first.viewport > frame.height {
                // An anchor's point on the row meets the same point of the
                // view, so this one puts the row's top at frame.minY.
                scroll(&position, UnitPoint(x: 0, y: frame.minY / (first.viewport - frame.height)))
            }
            try? await Task.sleep(for: .milliseconds(300))
            prepending = false
        }
    }

    private struct Edge: Equatable {
        var height: CGFloat
        var viewport: CGFloat
        var atEnd: Bool
        var nearTop: Bool
    }
}

/// Whether a conversation is at its end, kept apart from the view that
/// shows it. As that view's own state, the whole conversation and the box
/// under it were made again each time the reader left the end or came back,
/// and the change animated everything in the scroll view with it.
@MainActor @Observable
final class Following {
    var atEnd = true
}

/// The way back to the end, shown while the reader is up the conversation:
/// it alone watches whether they are.
struct JumpToLatestOverlay: View {
    let following: Following
    var hidden = false
    var action: () -> Void

    var body: some View {
        ZStack {
            if !following.atEnd, !hidden {
                JumpToLatest(action: action)
            }
        }
        .animation(.easeOut(duration: 0.15), value: following.atEnd)
    }
}

/// Where a conversation's first message was last drawn, kept aside rather
/// than as state, so scrolling redraws nothing.
final class FirstMessage {
    var frame: CGRect?
    var viewport: CGFloat = 0
    var nearTop = false
    var short = false
    var settling: Task<Void, Never>?
    /// Scrolls to it, by its id.
    var scroll: ((inout ScrollPosition, UnitPoint) -> Void)?
}

extension EnvironmentValues {
    @Entry var firstMessage: FirstMessage?
}

extension View {
    /// Marks the conversation's first message, which `FollowsEnd` keeps in
    /// place as earlier ones arrive above it.
    func firstMessage(_ id: some Hashable & Sendable, if isFirst: Bool) -> some View {
        modifier(FirstMessageMark(id: id, isFirst: isFirst))
    }
}

private struct FirstMessageMark<ID: Hashable & Sendable>: ViewModifier {
    @Environment(\.firstMessage) private var first
    let id: ID
    let isFirst: Bool

    func body(content: Content) -> some View {
        content.onGeometryChange(for: CGRect.self) { geometry in
            isFirst ? geometry.frame(in: .named(FollowsEnd.space)) : .null
        } action: { frame in
            guard isFirst, let first else { return }
            first.frame = frame
            first.scroll = { [id] position, anchor in position.scrollTo(id: id, anchor: anchor) }
        }
    }
}

/// Where earlier messages will appear, while there are more to load.
struct LoadingEarlier: View {
    var body: some View {
        ProgressView()
            .controlSize(.small)
            .frame(maxWidth: .infinity)
            .accessibilityLabel("Loading earlier messages")
    }
}

/// Back to the latest message, shown while the user has scrolled up.
struct JumpToLatest: View {
    var action: () -> Void

    var body: some View {
        Button(action: action) {
            Image(systemName: "arrow.down")
                .font(.system(size: 13, weight: .semibold))
                .foregroundStyle(Palette.text)
                .frame(width: 32, height: 32)
                .background(Palette.surface, in: Circle())
                .overlay(Circle().strokeBorder(Palette.line))
                .shadow(color: .black.opacity(0.1), radius: 8, y: 3)
        }
        .buttonStyle(.plain)
        .help("Jump to the latest")
        .accessibilityLabel("Jump to the latest message")
        .padding(.bottom, 10)
        .transition(.opacity.combined(with: .scale(scale: 0.9)))
    }
}
