import SwiftUI

/// One row in a palette: a file to open, a command to run.
struct PaletteItem: Identifiable {
    let id: String
    let title: String
    var detail: String? = nil
    var icon: String = "doc"
    var tint: Color = Palette.muted
    /// A file's own icon from the asset catalogue, shown instead of `icon`.
    var fileIcon: String? = nil
    /// A shortcut shown at the row's end, such as "⌘P".
    var shortcut: String? = nil
    /// A line of text shown instead of the title, with what matched marked,
    /// as a search's result is; the detail then says where it is.
    var line: AttributedString? = nil
}

/// A floating search box with its results under it, in the manner of the
/// command palettes in VS Code, Linear and Raycast: type, move with the
/// arrow keys, Return to choose, Escape or a click outside to close.
struct PaletteView: View {
    let placeholder: String
    /// The rows for what has been typed, best first.
    let results: (String) -> [PaletteItem]
    let choose: (PaletteItem) -> Void
    let dismiss: () -> Void
    /// Told of every change to what is typed, for results that take a
    /// moment to find, as a search's do.
    var typed: ((String) -> Void)? = nil
    /// Return before choosing: true when it was taken, as a search not run
    /// yet takes it.
    var submit: ((String) -> Bool)? = nil
    /// Beside the box: a search's options.
    var accessory: AnyView? = nil
    /// Under the rows, or instead of them: "Searching…", "12 results".
    var status: String? = nil
    @State var query = ""
    @State private var selected = 0
    @FocusState private var focused: Bool

    var body: some View {
        let items = results(query)
        ZStack(alignment: .top) {
            Color.black.opacity(0.001)
                .contentShape(Rectangle())
                .onTapGesture(perform: dismiss)
            VStack(spacing: 0) {
                HStack(spacing: 10) {
                    Image(systemName: "magnifyingglass")
                        .font(.system(size: 13, weight: .medium))
                        .foregroundStyle(Palette.muted)
                    TextField(placeholder, text: $query)
                        .textFieldStyle(.plain)
                        .font(Fonts.sans(15))
                        .focused($focused)
                        .onSubmit {
                            if submit?(query) == true { return }
                            if items.indices.contains(selected) { choose(items[selected]) }
                        }
                        .onKeyPress(.downArrow) {
                            selected = min(selected + 1, max(items.count - 1, 0))
                            return .handled
                        }
                        .onKeyPress(.upArrow) {
                            selected = max(selected - 1, 0)
                            return .handled
                        }
                        .onKeyPress(.escape) {
                            dismiss()
                            return .handled
                        }
                    if let accessory { accessory }
                }
                .padding(.horizontal, 14)
                .frame(height: 46)
                if !items.isEmpty {
                    Divider()
                    // Rows are known by what they show, never by their place
                    // in the list, or a new search would show the old rows.
                    ScrollViewReader { proxy in
                        ScrollView {
                            VStack(spacing: 2) {
                                ForEach(Array(items.enumerated()), id: \.element.id) { index, item in
                                    row(item, selected: index == selected)
                                        .onTapGesture { choose(item) }
                                        .onHover { if $0 { selected = index } }
                                }
                            }
                            .padding(6)
                        }
                        .frame(height: min(CGFloat(items.count) * 34 + 12, 380))
                        .onChange(of: selected) { _, now in
                            if items.indices.contains(now) { proxy.scrollTo(items[now].id) }
                        }
                    }
                    if let status {
                        Divider()
                        Text(status)
                            .font(TextStyle.caption)
                            .foregroundStyle(Palette.faint)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(.horizontal, 16)
                            .padding(.vertical, 7)
                    }
                } else if !query.isEmpty {
                    Divider()
                    Text(status ?? "Nothing matches")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.muted)
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 16)
                }
            }
            .frame(width: 580)
            .background(Palette.surface, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 14, style: .continuous).strokeBorder(Palette.line))
            .shadow(color: .black.opacity(0.18), radius: 30, y: 12)
            .padding(.top, 70)
        }
        .onAppear {
            focused = true
            typed?(query)
        }
        .onChange(of: query) {
            selected = 0
            typed?(query)
        }
    }

    private func row(_ item: PaletteItem, selected: Bool) -> some View {
        HStack(spacing: 10) {
            Group {
                if let fileIcon = item.fileIcon {
                    Image(fileIcon)
                        .resizable()
                        .frame(width: 15, height: 15)
                } else {
                    Image(systemName: item.icon)
                        .font(.system(size: 12))
                        .foregroundStyle(item.tint)
                }
            }
            .frame(width: 18)
            if let line = item.line {
                Text(line)
                    .font(Fonts.mono(12))
                    .foregroundStyle(Palette.textSecondary)
                    .lineLimit(1)
                    .layoutPriority(1)
            } else {
                Text(item.title)
                    .font(Fonts.sans(13.5, .medium))
                    .foregroundStyle(Palette.text)
                    .lineLimit(1)
            }
            if let detail = item.detail {
                Text(detail)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.faint)
                    .lineLimit(1)
                    .truncationMode(.head)
                    // Where a line is: it keeps its room, the line gives way.
                    .fixedSize(horizontal: item.line != nil, vertical: false)
            }
            Spacer(minLength: 8)
            if let shortcut = item.shortcut {
                Text(shortcut)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
            }
        }
        .padding(.horizontal, 10)
        .frame(height: 32)
        .background(selected ? Palette.fill : .clear, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
        .contentShape(Rectangle())
    }
}
