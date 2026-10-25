import AppKit
import SwiftUI

/// Swiping a card away with two fingers on the trackpad, the way a Mac
/// notification goes. The card follows the fingers; let go past a third of
/// its width and it leaves, short of that and it springs back. Up-and-down
/// swipes still scroll the conversation, and clicks, text selection and
/// typing in the card are untouched.
struct SwipeToDismiss: ViewModifier {
    /// Puts the card away. Returns whether it went; if not, it comes back.
    var onDismiss: () async -> Bool

    @State private var offset: CGFloat = 0
    @State private var width: CGFloat = 400
    @State private var monitor: Any?
    @State private var hovering = false
    /// The swipe in progress: nil until it has a direction, then whether it
    /// is sideways (the card's) or not (the conversation's).
    @State private var sideways: Bool?
    @State private var travel: CGSize = .zero
    @State private var leaving = false

    func body(content: Content) -> some View {
        content
            .offset(x: offset)
            .opacity(1 - min(0.7, abs(offset) / max(width, 1)))
            .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { width = $0 }
            .onHover { inside in
                hovering = inside
                if inside { install() } else if sideways != true { remove() }
            }
            .onDisappear { remove() }
    }

    private func install() {
        guard monitor == nil else { return }
        monitor = NSEvent.addLocalMonitorForEvents(matching: .scrollWheel) { event in
            let scroll = Scroll(event)
            let taken = MainActor.assumeIsolated { take(scroll) }
            return taken ? nil : event
        }
    }

    private func remove() {
        if let monitor { NSEvent.removeMonitor(monitor) }
        monitor = nil
    }

    /// One scroll event, as far as a swipe needs it.
    private struct Scroll: Sendable {
        var precise: Bool
        var momentum: Bool
        var phase: NSEvent.Phase.RawValue
        var dx: CGFloat
        var dy: CGFloat
        var natural: Bool

        init(_ event: NSEvent) {
            precise = event.hasPreciseScrollingDeltas
            momentum = event.momentumPhase != []
            phase = event.phase.rawValue
            dx = event.scrollingDeltaX
            dy = event.scrollingDeltaY
            natural = event.isDirectionInvertedFromDevice
        }
    }

    /// Takes the sideways part of a trackpad swipe over the card, and says
    /// whether it did; everything else passes through to the conversation.
    private func take(_ scroll: Scroll) -> Bool {
        guard scroll.precise, !leaving else { return false }
        // The glide after the fingers lift belongs to whoever had the swipe.
        if scroll.momentum {
            return sideways == true
        }
        let phase = NSEvent.Phase(rawValue: scroll.phase)
        if phase.contains(.began) {
            sideways = nil
            travel = .zero
            return false
        }
        if phase.contains(.changed) {
            travel.width += scroll.dx
            travel.height += scroll.dy
            if sideways == nil, abs(travel.width) + abs(travel.height) > 6 {
                sideways = abs(travel.width) > abs(travel.height) * 1.5
            }
            guard sideways == true else { return false }
            // With natural scrolling the deltas already run with the fingers.
            offset += scroll.natural ? scroll.dx : -scroll.dx
            return true
        }
        if phase.contains(.ended) || phase.contains(.cancelled) {
            let wasSideways = sideways == true
            sideways = nil
            if wasSideways { settle() }
            if !hovering { remove() }
            return wasSideways
        }
        return sideways == true
    }

    private func settle() {
        guard abs(offset) > width * 0.33 else {
            withAnimation(.spring(duration: 0.3, bounce: 0.25)) { offset = 0 }
            return
        }
        leaving = true
        let direction: CGFloat = offset > 0 ? 1 : -1
        withAnimation(.easeOut(duration: 0.18)) { offset = direction * width * 1.1 }
        Task {
            if !(await onDismiss()) {
                withAnimation(.spring(duration: 0.35, bounce: 0.2)) { offset = 0 }
            }
            leaving = false
        }
    }
}

extension View {
    /// Lets the user swipe this away with two fingers.
    func swipeToDismiss(_ onDismiss: @escaping () async -> Bool) -> some View {
        modifier(SwipeToDismiss(onDismiss: onDismiss))
    }
}
