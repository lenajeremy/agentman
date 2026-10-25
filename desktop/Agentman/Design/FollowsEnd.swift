import SwiftUI

/// A conversation that follows its end only while the reader is there.
///
/// New messages and streamed text keep the view at the bottom when it is
/// already at the bottom. Once the user scrolls up to read, growth anchors to
/// the top instead, so what they are reading stays where it is, and `atEnd`
/// turns false for the view's own follow-along to check.
struct FollowsEnd: ViewModifier {
    @Binding var atEnd: Bool

    /// How close to the bottom still counts as there: a line or two, so a
    /// small nudge while reading the last message doesn't stop the follow.
    static let slack: CGFloat = 80

    func body(content: Content) -> some View {
        content
            .defaultScrollAnchor(.bottom, for: .initialOffset)
            .defaultScrollAnchor(atEnd ? .bottom : .top, for: .sizeChanges)
            .onScrollGeometryChange(for: Edge.self) { geometry in
                Edge(height: geometry.contentSize.height,
                     atEnd: geometry.visibleRect.maxY >= geometry.contentSize.height - Self.slack)
            } action: { old, new in
                // Only the reader leaves the end: growth that lands before
                // the view catches up mustn't stop it following.
                if new.atEnd {
                    atEnd = true
                } else if new.height == old.height {
                    atEnd = false
                }
            }
    }

    private struct Edge: Equatable {
        var height: CGFloat
        var atEnd: Bool
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
