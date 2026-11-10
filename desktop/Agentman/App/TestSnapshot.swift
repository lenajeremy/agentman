#if DEBUG
import AppKit
import WebKit

/// A test run (Debug builds only): AGENTMAN_TEST_SNAPSHOT=<file.png> compares
/// this app with the cross-platform one (crossplatform/scripts/teststack/
/// compare.sh) without showing anything to whoever is using the Mac. The app
/// never becomes the active app; its windows are transparent, let clicks
/// through and sit behind everything; after AGENTMAN_TEST_DELAY seconds
/// (default 10) it draws a window into the file — AGENTMAN_TEST_WINDOW names
/// it by title, else the largest — and quits.
@MainActor
enum TestSnapshot {
    private static let env = ProcessInfo.processInfo.environment
    private static var quietWebViews = Set<ObjectIdentifier>()

    static func start() {
        guard let path = env["AGENTMAN_TEST_SNAPSHOT"], !path.isEmpty else { return }
        NSApp.setActivationPolicy(.accessory)
        // AGENTMAN_TEST_TERMINAL=<folder>: a shell open there, so the
        // terminal under the conversation (or in the panel) has one to show.
        if let folder = env["AGENTMAN_TEST_TERMINAL"], !folder.isEmpty {
            RightPanel.startTerminal(in: folder)
            // AGENTMAN_TEST_TERMINAL_SPLIT=1: and a second shell beside it.
            if env["AGENTMAN_TEST_TERMINAL_SPLIT"] == "1" {
                EditorRegistry.shared.store(for: folder).splitTerminal()
            }
        }
        TestTyping.start()
        // Every window is hidden the moment it exists, the editor's too.
        let timer = Timer(timeInterval: 0.01, repeats: true) { _ in
            MainActor.assumeIsolated { hideWindows() }
        }
        RunLoop.main.add(timer, forMode: .common)
        let delay = Double(env["AGENTMAN_TEST_DELAY"] ?? "") ?? 10
        DispatchQueue.main.asyncAfter(deadline: .now() + delay) {
            MainActor.assumeIsolated { snapshot(to: path) }
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + delay + 20) {
            fputs("agentman: test snapshot timed out\n", stderr)
            exit(4)
        }
    }

    private static func hideWindows() {
        for window in NSApp.windows {
            if window.alphaValue != 0 {
                window.alphaValue = 0
                window.ignoresMouseEvents = true
                window.hasShadow = false
                window.orderBack(nil)
            }
            // WebKit stops drawing a page it thinks is covered; the editor's
            // must keep drawing for its picture.
            for web in webViews(in: window.contentView) where !quietWebViews.contains(ObjectIdentifier(web)) {
                quietWebViews.insert(ObjectIdentifier(web))
                let occlusion = NSSelectorFromString("_setWindowOcclusionDetectionEnabled:")
                if web.responds(to: occlusion), let imp = web.method(for: occlusion) {
                    typealias Setter = @convention(c) (AnyObject, Selector, Bool) -> Void
                    unsafeBitCast(imp, to: Setter.self)(web, occlusion, false)
                }
            }
        }
    }

    private static func snapshot(to path: String) {
        let windows = NSApp.windows.filter { $0.isVisible && $0.contentView != nil && $0.frame.width > 300 && $0.frame.height > 200 }
        let wanted = env["AGENTMAN_TEST_WINDOW"]
        let window = windows.first { wanted != nil && $0.title == wanted }
            ?? windows.max { $0.frame.width * $0.frame.height < $1.frame.width * $1.frame.height }
        guard let view = window?.contentView, let rep = view.bitmapImageRepForCachingDisplay(in: view.bounds) else {
            fputs("agentman: no window to take a picture of\n", stderr)
            exit(3)
        }
        view.cacheDisplay(in: view.bounds, to: rep)
        // A web view doesn't draw itself into the cache: its own snapshot
        // goes over its place.
        draw(webViews(in: view), into: rep, from: view) {
            guard let png = rep.representation(using: .png, properties: [:]),
                  (try? png.write(to: URL(fileURLWithPath: path))) != nil else {
                fputs("agentman: could not write \(path)\n", stderr)
                exit(4)
            }
            exit(0)
        }
    }

    private static func draw(_ list: [WKWebView], into rep: NSBitmapImageRep, from root: NSView, then done: @escaping @MainActor () -> Void) {
        guard let web = list.first else { return done() }
        let config = WKSnapshotConfiguration()
        config.afterScreenUpdates = true
        web.takeSnapshot(with: config) { image, _ in
            MainActor.assumeIsolated {
                if let image {
                    var rect = root.convert(web.bounds, from: web)
                    if root.isFlipped { rect.origin.y = root.bounds.height - rect.maxY }
                    NSGraphicsContext.saveGraphicsState()
                    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
                    image.draw(in: rect)
                    NSGraphicsContext.restoreGraphicsState()
                }
                draw(Array(list.dropFirst()), into: rep, from: root, then: done)
            }
        }
    }

    private static func webViews(in view: NSView?) -> [WKWebView] {
        guard let view else { return [] }
        if let web = view as? WKWebView { return [web] }
        return view.subviews.flatMap { webViews(in: $0) }
    }
}
#endif
