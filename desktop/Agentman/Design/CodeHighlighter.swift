import AppKit
import SwiftUI

/// Light syntax colouring for code: comments, strings, numbers, keywords and
/// type names, by the file's extension. One pass of one expression, so a
/// string inside a comment stays a comment.
enum CodeHighlighter {
    enum Token { case comment, string, number, keyword, type }

    struct Language {
        var keywords: Set<String>
        var lineComment: [String] = ["//"]
        var blockComment = true
        var hashComment = false
        var backtick = false
    }

    static func language(for path: String) -> Language? {
        let ext = (path as NSString).pathExtension.lowercased()
        let name = (path as NSString).lastPathComponent.lowercased()
        switch ext {
        case "swift": return Language(keywords: words("""
            let var func struct class enum protocol extension import return if else guard switch case default for in while repeat
            break continue throw throws rethrows try catch do async await actor init deinit self Self super nil true false some any
            static private fileprivate public internal open final override mutating nonisolated where as is inout defer
            """))
        case "go": return Language(keywords: words("""
            package import func return if else for range switch case default break continue go defer select chan map struct
            interface type var const nil true false fallthrough goto
            """), backtick: true)
        case "ts", "tsx", "js", "jsx", "mjs", "cjs", "mts", "cts": return Language(keywords: words("""
            const let var function return if else for while do switch case default break continue new class extends super this
            import export from as async await try catch finally throw typeof instanceof in of null undefined true false void
            interface type enum implements public private protected readonly static yield delete
            """), backtick: true)
        case "py": return Language(keywords: words("""
            def class return if elif else for while in not and or is import from as with try except finally raise pass break
            continue lambda yield global nonlocal None True False async await assert del
            """), lineComment: [], blockComment: false, hashComment: true)
        case "rs": return Language(keywords: words("""
            fn let mut const static struct enum impl trait pub use mod crate self Self super return if else match for in while loop
            break continue move ref as where async await dyn unsafe true false type
            """))
        case "java", "kt", "kts", "scala": return Language(keywords: words("""
            class interface object fun val var public private protected internal static final abstract override return if else when
            for while do switch case default break continue new this super import package try catch finally throw null true false
            void extends implements
            """))
        case "c", "h", "cpp", "cc", "hpp", "m", "mm": return Language(keywords: words("""
            int char float double void long short unsigned signed const static struct union enum typedef return if else for while do
            switch case default break continue sizeof include define ifdef ifndef endif class public private protected namespace
            template typename new delete nullptr true false auto
            """))
        case "rb": return Language(keywords: words("""
            def class module end return if elsif else unless while until for in do begin rescue ensure yield self nil true false and
            or not require
            """), lineComment: [], blockComment: false, hashComment: true)
        case "sh", "bash", "zsh": return Language(keywords: words("""
            if then else elif fi for in do done while until case esac function return export local set echo exit
            """), lineComment: [], blockComment: false, hashComment: true)
        case "yaml", "yml", "toml": return Language(keywords: words("true false null yes no"), lineComment: [], blockComment: false, hashComment: true)
        case "json": return Language(keywords: words("true false null"), lineComment: [], blockComment: false)
        case "css", "scss": return Language(keywords: words("important media import"), lineComment: [], blockComment: true)
        case "sql": return Language(keywords: words("""
            select from where insert into values update set delete create table index drop alter join left right inner outer on and
            or not null as order by group having limit primary key references
            """), lineComment: ["--"], blockComment: true)
        default:
            if name == "dockerfile" || name == "makefile" {
                return Language(keywords: words("FROM RUN COPY ADD CMD ENTRYPOINT ENV WORKDIR EXPOSE ARG"), lineComment: [], blockComment: false, hashComment: true)
            }
            return nil
        }
    }

    private static func words(_ text: String) -> Set<String> {
        Set(text.split(whereSeparator: { $0 == " " || $0 == "\n" }).map(String.init))
    }

    /// Guarded by lock.
    nonisolated(unsafe) private static var expressions: [String: NSRegularExpression] = [:]
    private static let lock = NSLock()

    /// Where each kind of token is in text.
    static func tokens(in text: String, language: Language) -> [(NSRange, Token)] {
        var comment: [String] = language.lineComment.map { NSRegularExpression.escapedPattern(for: $0) + "[^\\n]*" }
        if language.hashComment { comment.append("#[^\\n]*") }
        if language.blockComment { comment.append("/\\*[\\s\\S]*?\\*/") }
        var strings = [#""(?:\\.|[^"\\\n])*""#, #"'(?:\\.|[^'\\\n])*'"#]
        if language.backtick { strings.append("`[^`]*`") }
        let keywords = language.keywords.sorted { $0.count > $1.count }.map(NSRegularExpression.escapedPattern(for:)).joined(separator: "|")
        var pattern = ""
        if !comment.isEmpty { pattern += "(?<comment>" + comment.joined(separator: "|") + ")|" }
        pattern += "(?<string>" + strings.joined(separator: "|") + ")"
        pattern += "|(?<number>\\b\\d[\\d_]*(?:\\.\\d+)?\\b)"
        if !keywords.isEmpty { pattern += "|(?<keyword>\\b(?:" + keywords + ")\\b)" }
        pattern += "|(?<type>\\b[A-Z][A-Za-z0-9]+\\b)"

        lock.lock()
        let expression: NSRegularExpression
        if let cached = expressions[pattern] {
            expression = cached
        } else {
            guard let made = try? NSRegularExpression(pattern: pattern) else { lock.unlock(); return [] }
            expressions[pattern] = made
            expression = made
        }
        lock.unlock()

        var found: [(NSRange, Token)] = []
        let whole = NSRange(location: 0, length: (text as NSString).length)
        for match in expression.matches(in: text, range: whole) {
            for (name, token) in [("comment", Token.comment), ("string", .string), ("number", .number), ("keyword", .keyword), ("type", .type)] {
                let range = match.range(withName: name)
                if range.location != NSNotFound {
                    found.append((range, token))
                    break
                }
            }
        }
        return found
    }

    static func color(_ token: Token) -> NSColor {
        switch token {
        case .comment: NSColor(Palette.codeComment)
        case .string: NSColor(Palette.codeString)
        case .number: NSColor(Palette.codeNumber)
        case .keyword: NSColor(Palette.codeKeyword)
        case .type: NSColor(Palette.codeType)
        }
    }
}
