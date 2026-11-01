import SwiftUI

/// The words of a search, found again in what it found, to be marked: in
/// the sidebar's snippets, the palette's, and the thread a hit opens.
enum SearchWords {
    /// The query's words worth marking. One letter would mark half a line.
    static func of(_ query: String) -> [String] {
        let words = query.split(whereSeparator: \.isWhitespace).map(String.init).filter { $0.count >= 2 }
        // Longest first, so "notarize" is marked whole rather than as "no".
        return words.sorted { $0.count > $1.count }
    }

    /// Where the words are in text, ignoring case and accents, as the
    /// daemon matched them. Overlapping places are kept once.
    static func ranges(of words: [String], in text: String) -> [Range<String.Index>] {
        var found: [Range<String.Index>] = []
        for word in words {
            var from = text.startIndex
            while from < text.endIndex,
                  let range = text.range(of: word, options: [.caseInsensitive, .diacriticInsensitive], range: from..<text.endIndex) {
                if !found.contains(where: { $0.overlaps(range) }) { found.append(range) }
                from = range.upperBound
            }
        }
        return found.sorted { $0.lowerBound < $1.lowerBound }
    }

    /// A hit's snippet, who said it first, the words in the text colour and
    /// heavier than the rest, as T3 Code shows them.
    static func snippet(_ hit: SearchHit, query: String) -> AttributedString {
        var who = AttributedString(hit.role == .user ? "You: " : "Agent: ")
        who.foregroundColor = hit.role == .user ? Palette.workingText : Palette.ok
        return who + marked(hit.snippet, query: query)
    }

    /// Text in the muted colour with the query's words standing out.
    static func marked(_ text: String, query: String) -> AttributedString {
        var out = AttributedString(text)
        out.foregroundColor = Palette.muted
        for range in ranges(of: of(query), in: text) {
            guard let span = Range(range, in: out) else { continue }
            out[span].foregroundColor = Palette.text
            out[span].inlinePresentationIntent = .stronglyEmphasized
        }
        return out
    }

    /// Text with the query's words washed in the find colours: the match
    /// being looked at stronger than the rest.
    static func highlighted(_ text: AttributedString, words: [String], current: Bool) -> AttributedString {
        guard !words.isEmpty else { return text }
        var out = text
        let plain = String(out.characters)
        for range in ranges(of: words, in: plain) {
            let start = plain.distance(from: plain.startIndex, to: range.lowerBound)
            let length = plain.distance(from: range.lowerBound, to: range.upperBound)
            let lower = out.characters.index(out.startIndex, offsetBy: start)
            let upper = out.characters.index(lower, offsetBy: length)
            out[lower..<upper].backgroundColor = current ? Palette.findCurrent : Palette.findMatch
        }
        return out
    }
}

/// What a thread marks while its find bar is open: the words, and the
/// message being looked at.
struct FindMarks: Equatable {
    var words: [String]
    var current: String?
}

/// What one message marks: the words, and whether it is the match shown.
struct FindHighlight: Equatable {
    var words: [String]
    var current: Bool

    func mark(_ text: AttributedString) -> AttributedString {
        SearchWords.highlighted(text, words: words, current: current)
    }
}

extension EnvironmentValues {
    @Entry var findMarks: FindMarks?
    @Entry var findHighlight: FindHighlight?
}
