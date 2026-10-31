import AppKit
import SwiftUI

/// A file named with `@` in a message box, drawn as its icon and its name
/// in place of `@` and its path: one character in the text, so the caret
/// steps over it and Backspace takes it whole. The path is what the message
/// sends, and what a copy of it holds.
final class FileChip: NSTextAttachmentCell {
    /// The file's path in the folder, as `@` named it.
    let path: String
    private let label: NSAttributedString
    private let icon: NSImage?
    private let textFont: NSFont

    static let iconSize: CGFloat = 14
    static let gap: CGFloat = 4

    init(path: String, font: NSFont) {
        self.path = path
        textFont = font
        let name = (path as NSString).lastPathComponent
        label = NSAttributedString(string: name, attributes: [.font: font, .foregroundColor: NSColor(Palette.mention)])
        icon = NSImage(named: FileIcon.asset(name))
        super.init(textCell: "")
    }

    @available(*, unavailable)
    required init(coder: NSCoder) { fatalError("init(coder:) is not used") }

    /// The chip for a path, with the box's own text attributes about it.
    static func text(_ path: String, attributes: [NSAttributedString.Key: Any]) -> NSAttributedString {
        let font = attributes[.font] as? NSFont ?? .systemFont(ofSize: NSFont.systemFontSize)
        let attachment = NSTextAttachment()
        attachment.attachmentCell = FileChip(path: path, font: font)
        let chip = NSMutableAttributedString(attachment: attachment)
        chip.addAttributes(attributes, range: NSRange(location: 0, length: chip.length))
        return chip
    }

    /// Text as it is sent: each chip back to `@` and its path.
    static func plain(_ text: NSAttributedString) -> String {
        guard text.containsAttachments else { return text.string }
        var plain = ""
        let string = text.string as NSString
        text.enumerateAttribute(.attachment, in: NSRange(location: 0, length: text.length)) { value, range, _ in
            if let chip = (value as? NSTextAttachment)?.attachmentCell as? FileChip {
                plain += String(repeating: "@" + chip.path, count: range.length)
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
