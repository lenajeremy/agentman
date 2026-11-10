#if DEBUG
import AppKit

/// For comparisons with the cross-platform app (TestSnapshot.swift), Debug
/// builds only: AGENTMAN_TEST_TYPE=<text> types into the first message box
/// shown, AGENTMAN_TEST_TYPE_DELAY seconds after launch (default 4), as the
/// user would: the box takes the focus, then the letters. What a message box
/// shows as it is typed in (the files for @, the commands for /) can then be
/// pictured without anyone typing.
@MainActor
enum TestTyping {
    static func start() {
        let env = ProcessInfo.processInfo.environment
        guard let text = env["AGENTMAN_TEST_TYPE"], !text.isEmpty else { return }
        let delay = Double(env["AGENTMAN_TEST_TYPE_DELAY"] ?? "") ?? 4
        DispatchQueue.main.asyncAfter(deadline: .now() + delay) {
            MainActor.assumeIsolated {
                for window in NSApp.windows {
                    guard let box = messageBox(in: window.contentView) else { continue }
                    window.makeFirstResponder(box)
                    box.insertText(text, replacementRange: box.selectedRange())
                    return
                }
                fputs("agentman: no message box to type in\n", stderr)
            }
        }
    }

    private static func messageBox(in view: NSView?) -> MessageTextView? {
        guard let view else { return nil }
        if let box = view as? MessageTextView, box.isEditable { return box }
        for child in view.subviews {
            if let found = messageBox(in: child) { return found }
        }
        return nil
    }
}
#endif
