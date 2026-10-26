import AppKit
import os
import SwiftUI
import WebKit

/// The editor's text: Monaco, in a web view, one per editor window, with a
/// model for each file open in a tab (desktop/editor-web).
///
/// Swift keeps the files: it opens them, saves them and follows the disk as
/// agents change it. The page holds the live text and reports every edit,
/// so `EditorDocument.text` is always what a save writes.
struct MonacoView: NSViewRepresentable {
    let store: EditorStore
    /// The text files open in tabs, as the page needs them.
    var tabs: [Tab]
    var activeID: UUID?
    var fontSize: Double
    /// Vim's keys in the editor, as the setting says.
    var vim = false

    struct Tab: Equatable {
        let id: UUID
        let path: String
        let revision: Int
        let indent: String
        let readOnly: Bool
        let saves: Int

        @MainActor
        init(_ document: EditorDocument) {
            id = document.id
            path = document.path
            revision = document.revision
            indent = document.indent
            readOnly = document.readOnly
            saves = document.saves
        }
    }

    /// `-legacyEditor YES` brings back the text view the editor had before
    /// Monaco, for a release, in case something about it gets in the way.
    static var legacy: Bool { UserDefaults.standard.bool(forKey: "legacyEditor") }

    func makeNSView(context: Context) -> MonacoHost {
        let controller = store.monaco ?? MonacoController(store: store)
        store.monaco = controller
        let host = MonacoHost(controller: controller)
        controller.add(host)
        return host
    }

    func updateNSView(_ host: MonacoHost, context: Context) {
        host.controller?.sync(tabs: tabs, activeID: activeID, fontSize: fontSize, vim: vim)
    }

    static func dismantleNSView(_ host: MonacoHost, coordinator: ()) {
        host.controller?.remove(host)
    }
}

/// A place in a window for the page. The page belongs to the folder's
/// editor, not to one window, so it moves to wherever the editor is shown.
final class MonacoHost: NSView {
    private(set) weak var controller: MonacoController?

    init(controller: MonacoController) {
        self.controller = controller
        super.init(frame: .zero)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError() }

    func hold(_ webView: WKWebView) {
        webView.removeFromSuperview()
        webView.frame = bounds
        webView.autoresizingMask = [.width, .height]
        addSubview(webView)
    }

    /// In a window on screen, or one in the Dock.
    var isOpen: Bool {
        guard let window else { return false }
        return window.isVisible || window.isMiniaturized
    }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        controller?.place()
    }
}

/// The page, and the conversation with it.
@MainActor
final class MonacoController: NSObject, WKScriptMessageHandler, WKScriptMessageHandlerWithReply, WKNavigationDelegate, WKUIDelegate {
    let webView: EditorWebView
    private weak var store: EditorStore?
    private var ready = false
    /// What the page has open, as it was last sent.
    private var opened: [UUID: MonacoView.Tab] = [:]
    private var shown: UUID?
    private var fontSize = 0.0
    private var vim: Bool?
    private var wanted: (tabs: [MonacoView.Tab], activeID: UUID?, fontSize: Double, vim: Bool) = ([], nil, 13, false)
    private var release: Task<Void, Never>?
    /// Finding am serve's language servers, until it answers.
    private var languages: Task<Void, Never>?
    private var watcher: FolderWatcher?

    /// How long the page stays after its window closes.
    static let keep: Duration = .seconds(600)
    private static let log = Logger(subsystem: "dev.agentman.mac", category: "editor")

    init(store: EditorStore) {
        self.store = store
        let configuration = WKWebViewConfiguration()
        configuration.setURLSchemeHandler(EditorScheme(), forURLScheme: EditorScheme.scheme)
        let content = WKUserContentController()
        configuration.userContentController = content
        webView = EditorWebView(frame: .zero, configuration: configuration)
        super.init()
        content.add(WeakMessageHandler(self), name: "editor")
        content.addScriptMessageHandler(WeakMessageHandler(self), contentWorld: .page, name: "editorAsk")
        webView.navigationDelegate = self
        webView.uiDelegate = self
        // The window's surface shows until the page has its colours.
        webView.setValue(false, forKey: "drawsBackground")
        webView.allowsMagnification = false
        webView.onAppearance = { [weak self] in self?.sendTheme() }
        for name in [NSWindow.willCloseNotification, NSWindow.didBecomeMainNotification] {
            NotificationCenter.default.addObserver(self, selector: #selector(windowsChanged), name: name, object: nil)
        }
        #if DEBUG
        webView.isInspectable = true
        #endif
        load()
    }

    private func load() {
        ready = false
        webView.load(URLRequest(url: URL(string: "\(EditorScheme.scheme)://app/index.html")!))
    }

    deinit {
        languages?.cancel()
    }

    // MARK: - Language servers

    /// Asks am serve where the language servers are, and keeps asking while
    /// it isn't running: the editor works without them, and picks them up
    /// when it starts.
    private func connectLanguages(after delay: Duration = .zero) {
        languages?.cancel()
        languages = Task { [weak self] in
            try? await Task.sleep(for: delay)
            while !Task.isCancelled {
                guard let self, let client = self.store?.client, let root = self.store?.root else { return }
                if let reply = try? await client.request(DaemonRequest(.lspEndpoint), timeout: 10), let endpoint = reply.lsp {
                    guard !Task.isCancelled, self.ready else { return }
                    self.call("agentman.connect(url, token, folder)", ["url": endpoint.url, "token": endpoint.token, "folder": root.path])
                    self.watch(root)
                    return
                }
                try? await Task.sleep(for: .seconds(10))
            }
        }
    }

    private func watch(_ folder: URL) {
        guard watcher == nil else { return }
        watcher = FolderWatcher(folder: folder) { [weak self] changes in
            guard let self, self.ready else { return }
            self.call("agentman.filesChanged(changes)", ["changes": changes.map { ["path": $0.path, "type": $0.type] }])
        }
    }

    /// Starts a language server that stopped, for the status bar.
    func restartLanguage(for document: EditorDocument) {
        guard ready else { return }
        call("agentman.restartLanguage(id)", ["id": document.id.uuidString])
    }

    // MARK: - Where the page sits

    private struct WeakHost {
        weak var host: MonacoHost?
    }

    /// Every place made for the page, newest last.
    private var hosts: [WeakHost] = []

    func add(_ host: MonacoHost) {
        hosts.append(WeakHost(host: host))
        place()
    }

    func remove(_ host: MonacoHost) {
        hosts.removeAll { $0.host == nil || $0.host === host }
        place()
    }

    /// The page sits in the folder's own window when it has one (a second
    /// window macOS restores for the folder is about to close, and a web
    /// view moved out of a closed window may not draw again), else in the
    /// newest place whose window is open. With none left, its window has
    /// closed: the page goes after a while, unless the window opens again.
    /// Every tab's text is in Swift, so nothing unsaved goes with it; only
    /// undo does.
    func place() {
        hosts.removeAll { $0.host == nil }
        let own = store?.window
        let candidates = hosts.compactMap(\.host).filter(\.isOpen)
        guard let host = candidates.last(where: { own != nil && $0.window === own }) ?? candidates.last else {
            if release == nil { releaseLater() }
            return
        }
        release?.cancel()
        release = nil
        if webView.superview !== host { host.hold(webView) }
    }

    private func releaseLater() {
        release = Task { [weak self] in
            try? await Task.sleep(for: Self.keep)
            guard !Task.isCancelled, let self, !self.hosts.contains(where: { $0.host?.isOpen == true }) else { return }
            self.store?.monaco = nil
        }
    }

    /// A window closed or came to the front: a closed one keeps its views,
    /// so the page may have to move.
    @objc private func windowsChanged(_ notification: Notification) {
        DispatchQueue.main.async { [weak self] in self?.place() }
    }

    // MARK: - Keeping the page in step

    func sync(tabs: [MonacoView.Tab], activeID: UUID?, fontSize: Double, vim: Bool) {
        wanted = (tabs, activeID, fontSize, vim)
        guard ready, let store else { return }
        if fontSize != self.fontSize {
            self.fontSize = fontSize
            call("agentman.configure({ fontSize })", ["fontSize": fontSize])
        }
        if vim != self.vim {
            self.vim = vim
            call("agentman.configure({ vim })", ["vim": vim])
        }
        let ids = Set(tabs.map(\.id))
        for id in opened.keys where !ids.contains(id) {
            call("agentman.close(id)", ["id": id.uuidString])
            opened[id] = nil
            if shown == id { shown = nil }
        }
        for tab in tabs {
            guard let document = store.documents.first(where: { $0.id == tab.id }) else { continue }
            if let was = opened[tab.id] {
                if was.path != tab.path {
                    call("agentman.rename(id, uri)", ["id": tab.id.uuidString, "uri": document.url.absoluteString])
                }
                if was.revision != tab.revision {
                    call("return agentman.setText(id, text, gen)",
                         ["id": tab.id.uuidString, "text": document.text, "gen": tab.revision]) { [weak self] length in
                        self?.check(document, length: length, revision: tab.revision)
                    }
                }
                if was.indent != tab.indent {
                    call("agentman.setOptions(id, tabSize, insertSpaces)",
                         ["id": tab.id.uuidString, "tabSize": Self.tabSize(tab.indent), "insertSpaces": tab.indent != "\t"])
                }
                if was.saves != tab.saves {
                    call("agentman.saved(id)", ["id": tab.id.uuidString])
                }
            } else {
                let doc: [String: Any] = [
                    "id": tab.id.uuidString, "uri": document.url.absoluteString, "text": document.text,
                    "tabSize": Self.tabSize(tab.indent), "insertSpaces": tab.indent != "\t", "gen": tab.revision,
                    "readOnly": tab.readOnly,
                ]
                call("return agentman.open(doc)", ["doc": doc]) { [weak self] length in
                    self?.check(document, length: length, revision: tab.revision)
                }
            }
            opened[tab.id] = tab
        }
        let front = tabs.contains { $0.id == activeID } ? activeID : nil
        if front != shown {
            shown = front
            call("agentman.show(id)", ["id": front?.uuidString ?? NSNull()])
            if front != nil {
                // A tab brought to the front takes the keyboard.
                DispatchQueue.main.async { [weak self] in
                    guard let self, let window = self.webView.window else { return }
                    window.makeFirstResponder(self.webView)
                }
            }
        }
    }

    private static func tabSize(_ indent: String) -> Int {
        indent == "\t" ? 4 : max(indent.count, 1)
    }

    /// The page answers an opened or replaced text with its length; a
    /// different one means it evened out mixed line endings, and its text is
    /// the one to keep.
    private func check(_ document: EditorDocument, length: Any?, revision: Int) {
        guard let length = length as? Int, length >= 0, length != document.text.utf16.count else { return }
        refetch(document, revision: revision)
    }

    private func refetch(_ document: EditorDocument, revision: Int) {
        call("return agentman.text(id)", ["id": document.id.uuidString]) { text in
            guard let text = text as? String, document.revision == revision else { return }
            document.adopt(text)
        }
    }

    // MARK: - Commands

    /// Gives the editor the keyboard, as Vim's Ctrl-W l does.
    func focus() {
        guard ready else { return }
        webView.window?.makeFirstResponder(webView)
        call("agentman.focus()")
    }

    /// Runs one of Monaco's actions in the file in front.
    func run(_ action: String) {
        guard ready else { return }
        webView.window?.makeFirstResponder(webView)
        call("agentman.run(action)", ["action": action])
    }

    @objc private func runMenuAction(_ item: NSMenuItem) {
        guard let action = item.representedObject as? String else { return }
        run(action)
    }

    // MARK: - The page's messages

    func userContentController(_ controller: WKUserContentController, didReceive message: WKScriptMessage) {
        guard let body = message.body as? [String: Any], let type = body["type"] as? String else { return }
        switch type {
        case "ready":
            ready = true
            opened = [:]
            shown = nil
            fontSize = 0
            vim = nil
            sendTheme()
            sync(tabs: wanted.tabs, activeID: wanted.activeID, fontSize: wanted.fontSize, vim: wanted.vim)
            connectLanguages()
        case "changes":
            guard let document = document(body["id"]), let gen = body["gen"] as? Int,
                  let raw = body["changes"] as? [[String: Any]], let length = body["length"] as? Int
            else { return }
            // Edits made before the text was replaced from outside are gone
            // from the page too.
            guard gen == document.revision else { return }
            let changes = raw.compactMap { change -> EditorDocument.Change? in
                guard let offset = change["offset"] as? Int, let count = change["length"] as? Int,
                      let text = change["text"] as? String else { return nil }
                return EditorDocument.Change(offset: offset, length: count, text: text)
            }
            if changes.count != raw.count || !document.apply(changes, expecting: length) {
                refetch(document, revision: gen)
            }
        case "cursor":
            guard let document = document(body["id"]), let line = body["line"] as? Int, let column = body["column"] as? Int
            else { return }
            document.line = line
            document.column = column
        case "menu":
            guard let x = body["x"] as? Double, let y = body["y"] as? Double, let items = body["items"] as? [[String: Any]]
            else { return }
            // After this message returns: the menu tracks the mouse until it closes.
            DispatchQueue.main.async { [weak self] in self?.showMenu(items, at: NSPoint(x: x, y: y)) }
        case "language":
            guard let document = document(body["id"]), let name = body["name"] as? String,
                  let state = body["state"] as? String else { return }
            let status = LanguageStatus(name: name, language: body["language"] as? String ?? "", state: state,
                                        detail: body["detail"] as? String ?? "")
            if document.language != status { document.language = status }
        case "problems":
            guard let document = document(body["id"]) else { return }
            let problems = Problems(errors: body["errors"] as? Int ?? 0, warnings: body["warnings"] as? Int ?? 0)
            if document.problems != problems { document.problems = problems }
        case "open":
            // Go to Definition, into another file.
            guard let path = body["path"] as? String else { return }
            store?.open(fileAt: URL(fileURLWithPath: path))
        case "vim":
            // The mode, or none once Vim is off.
            store?.vimMode = body["mode"] as? String
            store?.vimKeys = ""
        case "vimKeys":
            store?.vimKeys = body["keys"] as? String ?? ""
        case "command":
            guard let name = body["name"] as? String else { return }
            store?.vimCommand(name, path: body["path"] as? String)
        case "clipboard":
            // A Vim yank, on the Mac's clipboard too.
            guard let text = body["text"] as? String else { return }
            Clipboard.copy(text)
        case "lspClosed":
            // am serve went away, or restarted: find it again.
            connectLanguages(after: .seconds(2))
        case "log":
            Self.log.error("page: \(String(describing: body["message"] ?? ""), privacy: .public)")
        default:
            break
        }
    }

    /// What the page asks and waits for: a file's text to preview, or edits
    /// to files that aren't open.
    func userContentController(_ controller: WKUserContentController, didReceive message: WKScriptMessage,
                               replyHandler: @escaping @MainActor @Sendable (Any?, String?) -> Void) {
        guard let body = message.body as? [String: Any], let type = body["type"] as? String else {
            return replyHandler(nil, nil)
        }
        switch type {
        case "read":
            guard let path = body["path"] as? String else { return replyHandler(nil, nil) }
            replyHandler(read(path), nil)
        case "edit":
            guard let files = body["files"] as? [[String: Any]] else { return replyHandler(false, nil) }
            replyHandler(edit(files), nil)
        default:
            replyHandler(nil, nil)
        }
    }

    /// A file's text, as the editor has it when it's open, else from disk.
    private func read(_ path: String) -> String? {
        let url = URL(fileURLWithPath: path).standardizedFileURL
        if let open = store?.documents.first(where: { $0.url.standardizedFileURL == url && $0.kind == .text }) {
            return open.text
        }
        guard let size = (try? FileManager.default.attributesOfItem(atPath: url.path))?[.size] as? Int,
              size <= EditorDocument.maxBytes, let data = try? Data(contentsOf: url)
        else { return nil }
        return String(data: data, encoding: .utf8)
    }

    /// Edits from a language server to files in the folder that aren't open,
    /// a rename's say: they open in tabs behind, unsaved, for the user to save.
    private func edit(_ files: [[String: Any]]) -> Bool {
        guard let store else { return false }
        var targets: [(EditorDocument, [EditorDocument.LineEdit])] = []
        for file in files {
            guard let path = file["path"] as? String, let inside = store.relativePath(of: URL(fileURLWithPath: path)),
                  let raw = file["edits"] as? [[String: Any]]
            else { return false }
            let edits = raw.compactMap { edit -> EditorDocument.LineEdit? in
                guard let range = edit["range"] as? [String: Any], let start = range["start"] as? [String: Any],
                      let end = range["end"] as? [String: Any], let text = edit["newText"] as? String,
                      let startLine = start["line"] as? Int, let startCharacter = start["character"] as? Int,
                      let endLine = end["line"] as? Int, let endCharacter = end["character"] as? Int
                else { return nil }
                return EditorDocument.LineEdit(startLine: startLine, startCharacter: startCharacter,
                                               endLine: endLine, endCharacter: endCharacter, text: text)
            }
            guard edits.count == raw.count else { return false }
            targets.append((store.openInBackground(inside), edits))
        }
        return targets.allSatisfy { $0.0.apply($0.1) }
    }

    private func document(_ id: Any?) -> EditorDocument? {
        guard let id = id as? String, let uuid = UUID(uuidString: id) else { return nil }
        return store?.documents.first { $0.id == uuid }
    }

    /// The right-click menu, native, from what the page says applies.
    private func showMenu(_ items: [[String: Any]], at point: NSPoint) {
        let menu = NSMenu()
        menu.autoenablesItems = false
        for item in items {
            let id = item["id"] as? String ?? "-"
            if id == "-" {
                menu.addItem(.separator())
                continue
            }
            let title = item["title"] as? String ?? id
            let entry: NSMenuItem
            switch id {
            // The web view's own, as from the Edit menu.
            case "cut": entry = NSMenuItem(title: title, action: #selector(NSText.cut(_:)), keyEquivalent: "x")
            case "copy": entry = NSMenuItem(title: title, action: #selector(NSText.copy(_:)), keyEquivalent: "c")
            case "paste": entry = NSMenuItem(title: title, action: #selector(NSText.paste(_:)), keyEquivalent: "v")
            default:
                entry = NSMenuItem(title: title, action: #selector(runMenuAction(_:)), keyEquivalent: "")
                entry.target = self
                entry.representedObject = id
            }
            entry.isEnabled = item["enabled"] as? Bool ?? true
            menu.addItem(entry)
        }
        guard !menu.items.isEmpty else { return }
        let location = NSPoint(x: point.x, y: webView.isFlipped ? point.y : webView.bounds.height - point.y)
        menu.popUp(positioning: nil, at: location, in: webView)
    }

    // MARK: - Look

    private func sendTheme() {
        guard ready else { return }
        call("agentman.configure({ theme })", ["theme": EditorTheme.make(for: webView.effectiveAppearance)])
    }

    // MARK: - The web view

    private func call(_ body: String, _ arguments: [String: Any] = [:], then done: ((Any?) -> Void)? = nil) {
        webView.callAsyncJavaScript(body, arguments: arguments, in: nil, in: .page) { result in
            switch result {
            case .success(let value): done?(value)
            case .failure(let error):
                Self.log.error("\(body, privacy: .public) failed: \(error.localizedDescription, privacy: .public)")
                done?(nil)
            }
        }
    }

    /// The page is the editor's alone: links in it open in the browser.
    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction,
                 decisionHandler: @escaping @MainActor (WKNavigationActionPolicy) -> Void) {
        guard let url = action.request.url, url.scheme != EditorScheme.scheme else { return decisionHandler(.allow) }
        if ["http", "https"].contains(url.scheme) { NSWorkspace.shared.open(url) }
        decisionHandler(.cancel)
    }

    func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
                 for action: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
        if let url = action.request.url, ["http", "https"].contains(url.scheme) { NSWorkspace.shared.open(url) }
        return nil
    }

    /// WebKit ended the page's process (memory pressure, a crash): load it
    /// again and reopen every tab from Swift's text.
    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
        load()
    }
}

/// The web view, telling its controller when light and dark change.
final class EditorWebView: WKWebView {
    var onAppearance: (() -> Void)?

    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        onAppearance?()
    }
}

/// WebKit keeps its message handlers; this keeps the controller free to go.
private final class WeakMessageHandler: NSObject, WKScriptMessageHandler, WKScriptMessageHandlerWithReply {
    private weak var target: MonacoController?

    init(_ target: MonacoController) {
        self.target = target
    }

    func userContentController(_ controller: WKUserContentController, didReceive message: WKScriptMessage) {
        target?.userContentController(controller, didReceive: message)
    }

    func userContentController(_ controller: WKUserContentController, didReceive message: WKScriptMessage,
                               replyHandler: @escaping @MainActor @Sendable (Any?, String?) -> Void) {
        guard let target else { return replyHandler(nil, nil) }
        target.userContentController(controller, didReceive: message, replyHandler: replyHandler)
    }
}

/// agentman-editor://app/…: the page from Resources/EditorWeb, and the
/// app's own code font under fonts/.
final class EditorScheme: NSObject, WKURLSchemeHandler {
    static let scheme = "agentman-editor"

    func webView(_ webView: WKWebView, start task: any WKURLSchemeTask) {
        guard let url = task.request.url, let file = Self.file(for: url), let data = try? Data(contentsOf: file) else {
            task.didFailWithError(URLError(.fileDoesNotExist))
            return
        }
        let headers = ["Content-Type": Self.type(of: file), "Content-Length": String(data.count), "Cache-Control": "no-cache"]
        task.didReceive(HTTPURLResponse(url: url, statusCode: 200, httpVersion: "HTTP/1.1", headerFields: headers)!)
        task.didReceive(data)
        task.didFinish()
    }

    func webView(_ webView: WKWebView, stop task: any WKURLSchemeTask) {}

    static func file(for url: URL) -> URL? {
        let path = url.path
        if path.hasPrefix("/fonts/") {
            let name = (path as NSString).lastPathComponent
            return Bundle.main.url(forResource: (name as NSString).deletingPathExtension, withExtension: "ttf")
        }
        guard let root = Bundle.main.resourceURL?.appendingPathComponent("EditorWeb", isDirectory: true).standardizedFileURL
        else { return nil }
        let file = root.appendingPathComponent(String(path.drop { $0 == "/" })).standardizedFileURL
        // Nothing outside the page's folder.
        guard file.path.hasPrefix(root.path + "/") else { return nil }
        return file
    }

    static func type(of file: URL) -> String {
        switch file.pathExtension.lowercased() {
        case "html": "text/html; charset=utf-8"
        case "js", "mjs": "text/javascript; charset=utf-8"
        case "css": "text/css; charset=utf-8"
        case "ttf": "font/ttf"
        case "json", "map": "application/json"
        case "svg": "image/svg+xml"
        case "png": "image/png"
        default: "application/octet-stream"
        }
    }
}

/// Monaco's colours, from the app's palette, for one appearance.
enum EditorTheme {
    static func make(for appearance: NSAppearance) -> [String: Any] {
        var theme: [String: Any] = [:]
        appearance.performAsCurrentDrawingAppearance {
            let dark = appearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
            let clear = "#00000000"
            let colors: [String: String] = [
                "editor.background": hex(Palette.surface),
                "editor.foreground": hex(Palette.text),
                "editorGutter.background": hex(Palette.surface),
                "editorLineNumber.foreground": hex(Palette.faint),
                "editorLineNumber.activeForeground": hex(Palette.textSecondary),
                "editor.lineHighlightBackground": hex(Palette.text, 0.045),
                "editor.lineHighlightBorder": clear,
                "editorCursor.foreground": hex(Palette.text),
                "editor.selectionBackground": hex(Palette.working, 0.25),
                "editor.inactiveSelectionBackground": hex(Palette.working, 0.14),
                "editor.selectionHighlightBackground": hex(Palette.working, 0.1),
                "editor.wordHighlightBackground": hex(Palette.working, 0.1),
                "editor.wordHighlightStrongBackground": hex(Palette.working, 0.16),
                "editor.findMatchBackground": hex(Palette.needsYou, 0.4),
                "editor.findMatchHighlightBackground": hex(Palette.needsYou, 0.18),
                "editorBracketMatch.background": hex(Palette.working, 0.12),
                "editorBracketMatch.border": clear,
                "editorIndentGuide.background1": hex(Palette.line),
                "editorIndentGuide.activeBackground1": hex(Palette.faint, 0.6),
                "editorWhitespace.foreground": hex(Palette.faint, 0.5),
                "editorOverviewRuler.border": clear,
                "editorError.foreground": hex(Palette.error),
                "editorWarning.foreground": hex(Palette.needsYou),
                "editorInfo.foreground": hex(Palette.working),
                "editorLink.activeForeground": hex(Palette.workingText),
                "editorCodeLens.foreground": hex(Palette.muted),
                "editorWidget.background": hex(Palette.surface),
                "editorWidget.foreground": hex(Palette.text),
                "editorWidget.border": hex(Palette.line),
                "editorHoverWidget.background": hex(Palette.surface),
                "editorHoverWidget.border": hex(Palette.line),
                "editorSuggestWidget.background": hex(Palette.surface),
                "editorSuggestWidget.border": hex(Palette.line),
                "editorSuggestWidget.selectedBackground": hex(Palette.working, 0.12),
                "editorSuggestWidget.highlightForeground": hex(Palette.workingText),
                "list.hoverBackground": hex(Palette.fill),
                "list.highlightForeground": hex(Palette.workingText),
                "input.background": hex(Palette.paper),
                "input.border": hex(Palette.line),
                "inputOption.activeBorder": hex(Palette.working),
                "focusBorder": hex(Palette.working, 0.6),
                "widget.shadow": dark ? "#00000066" : "#0000001F",
                "scrollbarSlider.background": hex(Palette.text, 0.12),
                "scrollbarSlider.hoverBackground": hex(Palette.text, 0.2),
                "scrollbarSlider.activeBackground": hex(Palette.text, 0.28),
                "peekView.border": hex(Palette.working, 0.6),
                "peekViewTitle.background": hex(Palette.paper),
                "peekViewEditor.background": hex(Palette.paper),
                "peekViewEditorGutter.background": hex(Palette.paper),
                "peekViewResult.background": hex(Palette.surface),
                "peekViewEditor.matchHighlightBackground": hex(Palette.needsYou, 0.3),
                "peekViewResult.matchHighlightBackground": hex(Palette.needsYou, 0.3),
                "peekViewResult.selectionBackground": hex(Palette.working, 0.14),
            ]
            // What each piece of code is, by role (editor-web/src/roles.ts):
            // keywords and built-in types violet, types blue, functions gold,
            // properties and keys cyan, strings green, numbers orange.
            let syntax: [String: String] = [
                "text": hex(Palette.text),
                "comment": hex(Palette.codeComment),
                "string": hex(Palette.codeString),
                "string.escape": hex(Palette.codeNumber),
                "regexp": hex(Palette.codeString),
                "number": hex(Palette.codeNumber),
                "constant": hex(Palette.codeKeyword),
                "keyword": hex(Palette.codeKeyword),
                "operator": hex(Palette.text),
                "type": hex(Palette.codeType),
                "type.primitive": hex(Palette.codeKeyword),
                "function": hex(Palette.codeFunction),
                "property": hex(Palette.codeProperty),
                "tag": hex(Palette.codeKeyword),
                "delimiter.tag": hex(Palette.faint),
                "heading": hex(Palette.codeKeyword),
                "markup.bold": hex(Palette.text),
                "markup.italic": hex(Palette.text),
                "markup.strike": hex(Palette.muted),
                "link": hex(Palette.codeType),
                "diff.inserted": hex(Palette.codeString),
                "diff.deleted": hex(Palette.errorText),
                "diff.changed": hex(Palette.codeNumber),
                "invalid": hex(Palette.errorText),
            ]
            theme = ["dark": dark, "colors": colors, "syntax": syntax]
        }
        return theme
    }

    /// #RRGGBBAA, as the appearance in use draws the colour.
    private static func hex(_ color: Color, _ alpha: Double = 1) -> String {
        guard let rgb = NSColor(color).usingColorSpace(.sRGB) else { return "#000000" }
        let channels = [rgb.redComponent, rgb.greenComponent, rgb.blueComponent, rgb.alphaComponent * alpha]
        return "#" + channels.map { String(format: "%02X", Int(($0 * 255).rounded())) }.joined()
    }
}

