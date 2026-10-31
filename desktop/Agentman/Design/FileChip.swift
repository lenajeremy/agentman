import AppKit
import SwiftUI

/// A file named with `@` in a message box, drawn as its icon and its name
/// in place of `@` and its path: one character in the text, so the caret
/// steps over it and Backspace takes it whole. The path is what the message
/// sends, and what a copy of it holds.
/// A chip in a message box, and the text it stands for in what is sent.
@MainActor protocol PlainChip {
    var plain: String { get }
}

final class FileChip: NSTextAttachmentCell, PlainChip {
    /// The file's path in the folder, as `@` named it.
    let path: String
    var plain: String { "@" + path }
    private let label: NSAttributedString
    private let icon: NSImage?
    private let textFont: NSFont

    static let iconSize: CGFloat = 14
    static let gap: CGFloat = 2

    init(path: String, directory: Bool = false, font: NSFont) {
        self.path = path
        textFont = font
        let name = (path as NSString).lastPathComponent
        label = NSAttributedString(string: name, attributes: [.font: font, .foregroundColor: NSColor(Palette.mention)])
        icon = NSImage(named: FileIcon.asset(name, directory: directory))
        super.init(textCell: "")
    }

    @available(*, unavailable)
    required init(coder: NSCoder) { fatalError("init(coder:) is not used") }

    /// The chip for a path, with the box's own text attributes about it.
    static func text(_ path: String, directory: Bool = false, attributes: [NSAttributedString.Key: Any]) -> NSAttributedString {
        let font = attributes[.font] as? NSFont ?? .systemFont(ofSize: NSFont.systemFontSize)
        let attachment = NSTextAttachment()
        attachment.attachmentCell = FileChip(path: path, directory: directory, font: font)
        let chip = NSMutableAttributedString(attachment: attachment)
        chip.addAttributes(attributes, range: NSRange(location: 0, length: chip.length))
        return chip
    }

    /// Text as it is sent: each chip back to its words, `@` and a file's
    /// path, or a skill's prefix and name.
    static func plain(_ text: NSAttributedString) -> String {
        guard text.containsAttachments else { return text.string }
        var plain = ""
        let string = text.string as NSString
        text.enumerateAttribute(.attachment, in: NSRange(location: 0, length: text.length)) { value, range, _ in
            if let chip = (value as? NSTextAttachment)?.attachmentCell as? PlainChip {
                plain += String(repeating: chip.plain, count: range.length)
            } else {
                plain += string.substring(with: range)
            }
        }
        return plain
    }

    // MARK: Drawing

    override func cellSize() -> NSSize {
        NSSize(width: ceil(Self.iconSize + Self.gap + label.size().width + 1), height: ceil(textFont.ascender - textFont.descender))
    }

    /// On the line's baseline, as the words beside it.
    override func cellBaselineOffset() -> NSPoint {
        NSPoint(x: 0, y: textFont.descender)
    }

    override func draw(withFrame cellFrame: NSRect, in controlView: NSView?) {
        // The icon level with the name's letters, the name on the line.
        let middle = cellFrame.minY + textFont.ascender - textFont.capHeight / 2
        icon?.draw(in: NSRect(x: cellFrame.minX, y: (middle - Self.iconSize / 2).rounded(), width: Self.iconSize, height: Self.iconSize),
                   from: .zero, operation: .sourceOver, fraction: 1, respectFlipped: true, hints: nil)
        label.draw(at: NSPoint(x: cellFrame.minX + Self.iconSize + Self.gap, y: cellFrame.minY))
    }

    override func draw(withFrame cellFrame: NSRect, in controlView: NSView?, characterIndex charIndex: Int,
                       layoutManager: NSLayoutManager) {
        draw(withFrame: cellFrame, in: controlView)
    }

    /// Clicks place the caret, as on any letter; a chip isn't dragged.
    override func wantsToTrackMouse() -> Bool { false }
}

/// A skill named in a message, drawn as a cube and its name in words
/// ("Frontend Design") in place of its prefix and name: one character in
/// the text, as a file's chip is. The prefix and name are what is sent; the
/// daemon puts them in the form the agent takes.
final class SkillChip: NSTextAttachmentCell, PlainChip {
    let command: SlashCommand
    private let label: NSAttributedString
    private let textFont: NSFont
    var plain: String { command.label }

    init(command: SlashCommand, font: NSFont) {
        self.command = command
        textFont = font
        label = NSAttributedString(string: Self.title(command.name),
                                   attributes: [.font: font, .foregroundColor: NSColor(Palette.skillTag)])
        super.init(textCell: "")
    }

    @available(*, unavailable)
    required init(coder: NSCoder) { fatalError("init(coder:) is not used") }

    /// A skill's name in words: "frontend-design" reads "Frontend Design",
    /// "vercel:deploy" "Vercel Deploy".
    static func title(_ name: String) -> String {
        name.split(whereSeparator: { "-_:".contains($0) })
            .map { word in word.prefix(1).uppercased() + word.dropFirst() }
            .joined(separator: " ")
    }

    /// The chip for a skill, with the box's own text attributes about it.
    static func text(_ command: SlashCommand, attributes: [NSAttributedString.Key: Any]) -> NSAttributedString {
        let font = attributes[.font] as? NSFont ?? .systemFont(ofSize: NSFont.systemFontSize)
        let attachment = NSTextAttachment()
        attachment.attachmentCell = SkillChip(command: command, font: font)
        let chip = NSMutableAttributedString(attachment: attachment)
        chip.addAttributes(attributes, range: NSRange(location: 0, length: chip.length))
        return chip
    }

    /// The cube, in the skill's colour.
    static func icon() -> NSImage? {
        NSImage(systemSymbolName: "cube", accessibilityDescription: "Skill")?
            .withSymbolConfiguration(NSImage.SymbolConfiguration(pointSize: 11.5, weight: .regular)
                .applying(NSImage.SymbolConfiguration(paletteColors: [NSColor(Palette.skillTag)])))
    }

    override func cellSize() -> NSSize {
        NSSize(width: ceil(FileChip.iconSize + FileChip.gap + label.size().width + 1), height: ceil(textFont.ascender - textFont.descender))
    }

    override func cellBaselineOffset() -> NSPoint {
        NSPoint(x: 0, y: textFont.descender)
    }

    override func draw(withFrame cellFrame: NSRect, in controlView: NSView?) {
        let middle = cellFrame.minY + textFont.ascender - textFont.capHeight / 2
        let size = FileChip.iconSize
        if let icon = Self.icon() {
            let scale = min(size / max(icon.size.width, 1), size / max(icon.size.height, 1))
            let drawn = NSSize(width: icon.size.width * scale, height: icon.size.height * scale)
            icon.draw(in: NSRect(x: cellFrame.minX + (size - drawn.width) / 2, y: (middle - drawn.height / 2).rounded(),
                                 width: drawn.width, height: drawn.height),
                      from: .zero, operation: .sourceOver, fraction: 1, respectFlipped: true, hints: nil)
        }
        label.draw(at: NSPoint(x: cellFrame.minX + size + FileChip.gap, y: cellFrame.minY))
    }

    override func draw(withFrame cellFrame: NSRect, in controlView: NSView?, characterIndex charIndex: Int,
                       layoutManager: NSLayoutManager) {
        draw(withFrame: cellFrame, in: controlView)
    }

    override func wantsToTrackMouse() -> Bool { false }
}

/// Sent text with each `@path` naming a file drawn as the message box drew
/// it: the file's icon and name. Paths are as the reader got them, inside
/// the agent's folder or in full; a word that names no file stays as typed.
enum FileTags {
    enum Segment: Equatable {
        case text(String)
        case file(path: String, directory: Bool)
        case skill(SlashCommand)
    }

    private static let fileWord = try! NSRegularExpression(pattern: #"(?<![^\s])@([^\s@]+)"#)
    private static let skillWord = try! NSRegularExpression(pattern: #"(?<![^\s])([/$][A-Za-z0-9][A-Za-z0-9._:\-]*)"#)
    @MainActor private static var cache: [String: [Segment]] = [:]

    /// Whether the text names a file, or one of these skills.
    @MainActor static func namesTags(_ text: String, folder: String?, skills: [SlashCommand] = []) -> Bool {
        segments(text, folder: folder, skills: skills).contains { if case .text = $0 { false } else { true } }
    }

    /// The text cut into words, files and skills, remembered: a
    /// conversation draws its messages again and again. A skill is one of
    /// those given, by its prefix and name.
    @MainActor static func segments(_ text: String, folder: String?, skills: [SlashCommand] = []) -> [Segment] {
        let known = Dictionary(skills.filter { $0.skill == true }.map { ($0.label, $0) }, uniquingKeysWith: { first, _ in first })
        guard text.contains("@") || (!known.isEmpty && text.contains(where: { $0 == "/" || $0 == "$" })) else { return [.text(text)] }
        let key = (folder ?? "") + "\u{0}" + known.keys.sorted().joined(separator: ",") + "\u{0}" + text
        if let cached = cache[key] { return cached }
        let ns = text as NSString
        let all = NSRange(location: 0, length: ns.length)
        // Each tag found, where it is.
        var found: [(range: NSRange, segment: Segment)] = []
        for match in fileWord.matches(in: text, range: all) {
            var range = match.range
            var path = ns.substring(with: match.range(at: 1))
            var directory = Self.exists(path, in: folder)
            while directory == nil, let last = path.last, ".,;:!?)]}'\"".contains(last) {
                path.removeLast()
                range.length -= 1
                directory = Self.exists(path, in: folder)
            }
            if let directory { found.append((range, .file(path: path, directory: directory))) }
        }
        if !known.isEmpty {
            for match in skillWord.matches(in: text, range: all) {
                var range = match.range
                var word = ns.substring(with: range)
                while known[word] == nil, let last = word.last, ".,;:!?)]}'\"".contains(last) {
                    word.removeLast()
                    range.length -= 1
                }
                if let command = known[word] { found.append((range, .skill(command))) }
            }
        }
        var parts: [Segment] = []
        var at = 0
        for tag in found.sorted(by: { $0.range.location < $1.range.location }) where tag.range.location >= at {
            if tag.range.location > at { parts.append(.text(ns.substring(with: NSRange(location: at, length: tag.range.location - at)))) }
            parts.append(tag.segment)
            at = NSMaxRange(tag.range)
        }
        if parts.isEmpty { parts = [.text(text)] } else if at < ns.length { parts.append(.text(ns.substring(from: at))) }
        if cache.count > 500 { cache.removeAll() }
        cache[key] = parts
        return parts
    }

    /// Whether a path names something, and if so whether a folder.
    private static func exists(_ path: String, in folder: String?) -> Bool? {
        guard !path.isEmpty else { return nil }
        let full: String
        if path.hasPrefix("/") {
            full = path
        } else if let folder {
            full = (folder as NSString).appendingPathComponent(path)
        } else {
            return nil
        }
        var directory: ObjCBool = false
        return FileManager.default.fileExists(atPath: full, isDirectory: &directory) ? directory.boolValue : nil
    }
}
