import Charts
import SwiftUI

/// What the agents on this Mac spent, from their own records: the total,
/// each agent's share, a chart of each day (each hour, over the last day),
/// the tokens by kind, and each model's or day's part. Costs are what the
/// API would charge: an estimate, as a subscription doesn't bill per token.
struct UsageView: View {
    @Environment(AgentsStore.self) private var store
    @Environment(\.detailHeaderLeading) private var headerLeading
    @AppStorage("usageDays") private var days = 30
    @AppStorage("usageMetric") private var metric = UsageMetric.cost
    @AppStorage("usageBreakdown") private var breakdown = UsageBreakdown.model
    /// Each range read so far, shown at once when it is picked again while
    /// it is read afresh.
    @State private var summaries: [Int: Usage]
    @State private var loading = false
    @State private var problem: String?
    @State private var width: CGFloat = 0
    /// False for pictures of the page, which hold what they show.
    private let reads: Bool

    init(showing usage: [Int: Usage] = [:]) {
        _summaries = State(initialValue: usage)
        reads = usage.isEmpty
    }

    static let ranges = [1, 7, 30, 90]

    var body: some View {
        VStack(spacing: 0) {
            header
            content
        }
        .navigationTitle("")
        .task(id: days) { await load() }
    }

    private var header: some View {
        HStack(spacing: 10) {
            Text("Usage").font(Fonts.sans(14, .semibold))
            Spacer()
            Segments(options: Self.ranges, selection: $days) { $0 == 1 ? "Past 24h" : "\($0) days" }
            Segments(options: UsageMetric.allCases, selection: $metric) { $0.title }
            ChromeButton(symbol: "arrow.clockwise", help: "Read again") {
                Task { await load() }
            }
            .disabled(loading || !reads)
            .opacity(loading ? 0.4 : 1)
        }
        .padding(.horizontal, 16)
        .padding(.leading, headerLeading)
        .frame(height: 32)
    }

    @ViewBuilder private var content: some View {
        if let usage = summaries[days] {
            if usage.tokens.total == 0 {
                NothingUsed(days: days)
            } else {
                ScrollView {
                    UsagePage(usage: usage, metric: metric, breakdown: $breakdown, width: width)
                        .frame(maxWidth: 1080, alignment: .leading)
                        .padding(.horizontal, 36)
                        .padding(.top, 18)
                        .padding(.bottom, 48)
                        .frame(maxWidth: .infinity)
                }
                .onGeometryChange(for: CGFloat.self) { min($0.size.width - 72, 1080) } action: { width = $0 }
            }
        } else if let problem {
            VStack(spacing: 10) {
                Text(problem)
                    .font(TextStyle.body)
                    .foregroundStyle(Palette.muted)
                    .multilineTextAlignment(.center)
                Button("Try Again") { Task { await load() } }
                    .buttonStyle(.link)
            }
            .padding(40)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            VStack(spacing: 12) {
                Loader(size: 16)
                Text("Reading what the agents spent…")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    private func load() async {
        guard reads else { return }
        let asked = days
        loading = true
        defer { loading = false }
        do {
            let usage = try await store.usage(days: asked)
            summaries[asked] = usage
            problem = nil
        } catch is CancellationError {
        } catch {
            if summaries[asked] == nil { problem = error.localizedDescription }
        }
    }
}

enum UsageMetric: String, CaseIterable {
    case cost, tokens

    var title: String { self == .cost ? "Cost" : "Tokens" }
}

enum UsageBreakdown: String, CaseIterable {
    case model, day
}

/// Nothing used in the range.
private struct NothingUsed: View {
    let days: Int

    var body: some View {
        VStack(spacing: 8) {
            Image(systemName: "chart.xyaxis.line")
                .font(.system(size: 26))
                .foregroundStyle(Palette.faint)
            Text(days == 1 ? "Nothing used in the past day" : "Nothing used in the past \(days) days")
                .font(TextStyle.title)
                .foregroundStyle(Palette.text)
            Text("Usage is read from Claude Code, Codex and OpenCode's own records on this Mac.")
                .font(TextStyle.caption)
                .foregroundStyle(Palette.muted)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

/// The page, for one range.
struct UsagePage: View {
    let usage: Usage
    let metric: UsageMetric
    @Binding var breakdown: UsageBreakdown
    /// The room across, to put the chart beside the totals or below them.
    var width: CGFloat
    /// A bucket pointed at, for pictures of the chart's tooltip.
    var pointing: Int?

    private var cost: Bool { metric == .cost }
    private var hourly: Bool { usage.hourly == true }

    var body: some View {
        VStack(alignment: .leading, spacing: 34) {
            if let skipped = usage.skipped, skipped > 0 {
                Text(skipped == 1 ? "1 record couldn't be read; usage may be short."
                                  : "\(skipped.formatted()) records couldn't be read; usage may be short.")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
            }
            if width >= 760 {
                HStack(alignment: .top, spacing: 40) {
                    summary.frame(width: 330)
                    chart
                }
            } else {
                VStack(alignment: .leading, spacing: 30) {
                    summary
                    chart
                }
            }
            totals
            byKind
            UsageBreakdownTable(usage: usage, metric: metric, breakdown: $breakdown)
        }
    }

    // MARK: The total and each agent's share

    private var agents: [Usage.Agent] {
        let agents = usage.agents ?? []
        return cost ? agents : agents.sorted { $0.tokens.total > $1.tokens.total }
    }

    private var summary: some View {
        VStack(alignment: .leading, spacing: 20) {
            VStack(alignment: .leading, spacing: 3) {
                Text(cost ? UsageFormat.money(usage.cost.total) : UsageFormat.tokens(usage.tokens.total))
                    .font(Fonts.sans(36, .semibold))
                    .monospacedDigit()
                    .foregroundStyle(Palette.text)
                    .lineLimit(1)
                    .minimumScaleFactor(0.6)
                HStack(spacing: 4) {
                    Text(UsageFormat.sessions(usage.sessions) + (cost ? " · API estimate" : ""))
                    if cost {
                        Image(systemName: "info.circle")
                            .font(.system(size: 11))
                            .help(UsageFormat.estimate)
                    }
                }
                .font(TextStyle.caption)
                .foregroundStyle(Palette.muted)
            }
            VStack(alignment: .leading, spacing: 14) {
                ForEach(agents, id: \.kind) { agent in
                    agentRow(agent)
                }
            }
        }
    }

    private func agentRow(_ agent: Usage.Agent) -> some View {
        let share = cost ? fraction(agent.cost, of: usage.cost.total)
                         : fraction(Double(agent.tokens.total), of: Double(usage.tokens.total))
        let other = cost ? UsageFormat.tokens(agent.tokens.total) + " tokens" : UsageFormat.money(agent.cost)
        return VStack(alignment: .leading, spacing: 3) {
            HStack(spacing: 7) {
                Circle().fill(Palette.agent(agent.kind)).frame(width: 7, height: 7)
                AgentMark(kind: agent.kind, size: 15)
                Text(AgentName.of(agent.kind))
                    .font(Fonts.sans(13.5, .medium))
                    .foregroundStyle(Palette.text)
                    .lineLimit(1)
                    .layoutPriority(1)
                Text(UsageFormat.sessions(agent.sessions))
                    .font(Fonts.sans(11.5))
                    .foregroundStyle(Palette.muted)
                    .lineLimit(1)
                Spacer(minLength: 8)
                Text(cost ? UsageFormat.money(agent.cost) : UsageFormat.tokens(agent.tokens.total))
                    .font(Fonts.sans(13.5, .medium))
                    .monospacedDigit()
                    .foregroundStyle(Palette.text)
            }
            Text(UsageFormat.percent(share) + (cost ? " of cost · " : " of tokens · ") + other)
                .font(Fonts.sans(12))
                .foregroundStyle(Palette.muted)
                .padding(.leading, 14)
        }
    }

    // MARK: The chart

    private var chart: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text((hourly ? "Hourly " : "Daily ") + (cost ? "cost" : "processed tokens"))
                .font(Fonts.sans(14, .semibold))
                .foregroundStyle(Palette.text)
            UsageChart(usage: usage, metric: metric, agents: agents.map(\.kind), pointing: pointing)
                .frame(height: 250)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    // MARK: Totals

    private var totals: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Totals")
                .font(Fonts.sans(14, .semibold))
                .foregroundStyle(Palette.text)
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 150), spacing: 20, alignment: .leading)], alignment: .leading, spacing: 18) {
                stat("Processed tokens", UsageFormat.tokens(usage.tokens.total))
                stat("Cached input", UsageFormat.tokens(usage.tokens.cacheRead))
                stat("Uncached input", UsageFormat.tokens(usage.tokens.input))
                stat("Output", UsageFormat.tokens(usage.tokens.output))
                stat("Cache savings", UsageFormat.money(usage.cacheSavings))
            }
        }
    }

    private func stat(_ title: String, _ value: String) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(title)
                .font(Fonts.sans(12))
                .foregroundStyle(Palette.muted)
            Text(value)
                .font(Fonts.sans(18, .medium))
                .monospacedDigit()
                .foregroundStyle(Palette.text)
        }
    }

    // MARK: By kind

    private struct Kind: Identifiable {
        let name: String
        let color: Color
        let value: Double
        var id: String { name }
    }

    private var kinds: [Kind] {
        let t = usage.tokens, c = usage.cost
        return [
            Kind(name: "Input", color: Palette.tokenInput, value: cost ? c.input : Double(t.input)),
            Kind(name: "Cache read", color: Palette.tokenCacheRead, value: cost ? c.cacheRead : Double(t.cacheRead)),
            Kind(name: "Cache write", color: Palette.tokenCacheWrite, value: cost ? c.cacheWrite : Double(t.cacheWrite)),
            Kind(name: "Output", color: Palette.tokenOutput, value: cost ? c.output : Double(t.output)),
        ]
    }

    private var byKind: some View {
        let kinds = kinds
        let total = kinds.reduce(0) { $0 + $1.value }
        return VStack(alignment: .leading, spacing: 12) {
            Text(cost ? "Cost by type" : "Tokens by type")
                .font(Fonts.sans(14, .semibold))
                .foregroundStyle(Palette.text)
            GeometryReader { geometry in
                let shown = kinds.filter { $0.value > 0 }
                let room = geometry.size.width - CGFloat(max(shown.count - 1, 0)) * 3
                HStack(spacing: 3) {
                    ForEach(shown) { kind in
                        Capsule()
                            .fill(kind.color)
                            .frame(width: max(room * kind.value / max(total, 1e-9), 6))
                    }
                }
            }
            .frame(width: min(max(width * 0.5, 280), 560), height: 8)
            FlowRow(spacing: 18, lineSpacing: 8) {
                ForEach(kinds) { kind in
                    HStack(spacing: 6) {
                        RoundedRectangle(cornerRadius: 2).fill(kind.color).frame(width: 9, height: 9)
                        Text(kind.name).foregroundStyle(Palette.muted)
                        Text(cost ? UsageFormat.money(kind.value) : UsageFormat.tokens(Int64(kind.value)))
                            .monospacedDigit()
                            .foregroundStyle(Palette.text)
                    }
                    .font(Fonts.sans(12))
                }
            }
        }
    }
}

/// Each agent's day (or hour) as a line over its own soft area, not
/// stacked, with what each spent where the pointer is.
private struct UsageChart: View {
    let usage: Usage
    let metric: UsageMetric
    let agents: [String]
    @State private var hovered: Usage.Bucket?
    @Environment(\.colorScheme) private var scheme

    init(usage: Usage, metric: UsageMetric, agents: [String], pointing: Int? = nil) {
        self.usage = usage
        self.metric = metric
        self.agents = agents
        let buckets = usage.buckets ?? []
        _hovered = State(initialValue: pointing.flatMap { buckets.indices.contains($0) ? buckets[$0] : nil })
    }

    private var buckets: [Usage.Bucket] { usage.buckets ?? [] }
    private var hourly: Bool { usage.hourly == true }

    private struct Point: Identifiable {
        var id: String { kind + String(date.timeIntervalSince1970) }
        let kind: String
        let date: Date
        let value: Double
    }

    private var points: [Point] {
        buckets.flatMap { bucket in
            agents.map { Point(kind: $0, date: bucket.date, value: value(bucket, $0)) }
        }
    }

    private func value(_ bucket: Usage.Bucket, _ kind: String) -> Double {
        metric == .cost ? bucket.cost?[kind] ?? 0 : Double(bucket.tokens?[kind] ?? 0)
    }

    private var top: Double {
        buckets.map { b in agents.map { value(b, $0) }.max() ?? 0 }.max() ?? 0
    }

    /// The axis: a round step, three or four of them up to past the top.
    private var axis: (top: Double, step: Double) {
        let top = max(top, metric == .cost ? 0.01 : 10)
        let rough = top / 3
        let magnitude = pow(10, floor(log10(rough)))
        let step = [1, 2, 2.5, 5, 10].map { $0 * magnitude }.first { $0 >= rough } ?? rough
        return (ceil(top / step) * step, step)
    }

    var body: some View {
        let names = agents.map(AgentName.of)
        Chart {
            ForEach(points) { point in
                AreaMark(x: .value("When", point.date), y: .value("Spent", point.value), stacking: .unstacked)
                    .foregroundStyle(by: .value("Agent", AgentName.of(point.kind)))
                    .interpolationMethod(.monotone)
                    .opacity(0.09)
                LineMark(x: .value("When", point.date), y: .value("Spent", point.value))
                    .foregroundStyle(by: .value("Agent", AgentName.of(point.kind)))
                    .interpolationMethod(.monotone)
                    .lineStyle(StrokeStyle(lineWidth: 1.6, lineCap: .round, lineJoin: .round))
            }
            if let hovered {
                RuleMark(x: .value("When", hovered.date))
                    .foregroundStyle(Palette.muted.opacity(0.6))
                    .lineStyle(StrokeStyle(lineWidth: 1))
                    .annotation(position: .trailing, alignment: .top, spacing: 10,
                                overflowResolution: .init(x: .fit(to: .chart), y: .disabled)) {
                        tooltip(hovered)
                    }
            }
        }
        .chartForegroundStyleScale(domain: names, range: agents.map(Palette.agent))
        .chartLegend(.hidden)
        .chartYScale(domain: 0...axis.top)
        .chartYAxis {
            AxisMarks(position: .leading, values: .stride(by: axis.step)) { value in
                AxisGridLine(stroke: StrokeStyle(lineWidth: 1)).foregroundStyle(Palette.line)
                AxisValueLabel {
                    if let v = value.as(Double.self) {
                        Text(metric == .cost ? UsageFormat.axisMoney(v, step: axis.step) : UsageFormat.tokens(Int64(v)))
                            .font(Fonts.sans(10.5))
                            .foregroundStyle(Palette.muted)
                    }
                }
            }
        }
        .chartXAxis {
            // The first day, the middle one and the last, as T3's.
            let marks = marks
            AxisMarks(values: [marks[0]]) { _ in
                AxisValueLabel(anchor: .topLeading) { axisLabel(marks[0]) }
            }
            AxisMarks(values: [marks[1]]) { _ in
                AxisValueLabel { axisLabel(marks[1]) }
            }
            AxisMarks(values: [marks[2]]) { _ in
                AxisValueLabel(anchor: .topTrailing) { axisLabel(marks[2]) }
            }
        }
        .chartOverlay { proxy in
            GeometryReader { geometry in
                Rectangle().fill(.clear).contentShape(Rectangle())
                    .onContinuousHover { phase in
                        switch phase {
                        case .active(let at):
                            guard let plot = proxy.plotFrame else { return }
                            let x = at.x - geometry[plot].origin.x
                            guard let date: Date = proxy.value(atX: x) else { return }
                            hovered = nearest(date)
                        case .ended:
                            hovered = nil
                        }
                    }
            }
        }
        .id(scheme)
    }

    private var marks: [Date] {
        guard let first = buckets.first?.date, let last = buckets.last?.date else { return [Date(), Date(), Date()] }
        return [first, buckets[buckets.count / 2].date, last]
    }

    private func nearest(_ date: Date) -> Usage.Bucket? {
        buckets.min { abs($0.date.timeIntervalSince(date)) < abs($1.date.timeIntervalSince(date)) }
    }

    private func axisLabel(_ date: Date) -> some View {
        Text(hourly ? date.formatted(.dateTime.hour()) : date.formatted(.dateTime.month(.abbreviated).day()).uppercased())
            .font(Fonts.sans(10.5))
            .foregroundStyle(Palette.muted)
    }

    private func tooltip(_ bucket: Usage.Bucket) -> some View {
        let total = agents.reduce(0) { $0 + value(bucket, $1) }
        return VStack(alignment: .leading, spacing: 5) {
            Text(hourly ? bucket.date.formatted(.dateTime.weekday(.abbreviated).hour()) : bucket.date.formatted(.dateTime.month(.abbreviated).day()))
                .font(Fonts.sans(11.5))
                .foregroundStyle(Palette.muted)
            ForEach(agents, id: \.self) { kind in
                HStack(spacing: 6) {
                    AgentMark(kind: kind, size: 12)
                    Text(AgentName.of(kind)).foregroundStyle(Palette.textSecondary)
                    Spacer(minLength: 14)
                    Text(format(value(bucket, kind))).monospacedDigit().foregroundStyle(Palette.text)
                }
            }
            Rectangle().fill(Palette.line).frame(height: 1).padding(.vertical, 2)
            HStack {
                Text("Total").foregroundStyle(Palette.textSecondary)
                Spacer(minLength: 14)
                Text(format(total)).monospacedDigit().foregroundStyle(Palette.text)
            }
        }
        .font(Fonts.sans(12))
        .padding(.horizontal, 11)
        .padding(.vertical, 9)
        .frame(width: 190)
        .background(Palette.surface, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 8, style: .continuous).strokeBorder(Palette.line))
        .shadow(color: .black.opacity(0.08), radius: 8, y: 2)
    }

    private func format(_ value: Double) -> String {
        metric == .cost ? UsageFormat.money(value) : UsageFormat.tokens(Int64(value))
    }
}

/// Each model's part, or each day's.
private struct UsageBreakdownTable: View {
    let usage: Usage
    let metric: UsageMetric
    @Binding var breakdown: UsageBreakdown

    private var cost: Bool { metric == .cost }
    private var hourly: Bool { usage.hourly == true }

    private struct Row: Identifiable {
        let id: String
        let name: String
        let kind: String?
        let cost: Double?
        let tokens: Int64
        /// Each agent's part, for a day's bar.
        let parts: [Part]
    }

    private struct Part: Identifiable {
        let kind: String
        let value: Double
        var id: String { kind }
    }

    private var rows: [Row] {
        switch breakdown {
        case .model:
            let models = (usage.models ?? []).sorted {
                cost ? ($0.cost, $0.tokens.total) > ($1.cost, $1.tokens.total) : $0.tokens.total > $1.tokens.total
            }
            return models.map {
                Row(id: $0.kind + "/" + $0.model, name: $0.model.isEmpty ? "Unknown model" : $0.model, kind: $0.kind,
                    cost: $0.priced ? $0.cost : nil, tokens: $0.tokens.total, parts: [])
            }
        case .day:
            let kinds = (usage.agents ?? []).map(\.kind)
            return (usage.buckets ?? []).reversed().compactMap { bucket in
                let tokens = bucket.tokens?.values.reduce(0, +) ?? 0
                guard tokens > 0 else { return nil }
                let parts = kinds.map { Part(kind: $0, value: cost ? bucket.cost?[$0] ?? 0 : Double(bucket.tokens?[$0] ?? 0)) }
                return Row(id: String(bucket.start), name: name(of: bucket.date), kind: nil,
                           cost: bucket.cost?.values.reduce(0, +) ?? 0, tokens: tokens, parts: parts)
            }
        }
    }

    private func name(of date: Date) -> String {
        hourly ? date.formatted(.dateTime.weekday(.abbreviated).hour())
               : date.formatted(.dateTime.weekday(.abbreviated).month(.abbreviated).day())
    }

    private func share(_ row: Row) -> Double {
        cost ? fraction(row.cost ?? 0, of: usage.cost.total) : fraction(Double(row.tokens), of: Double(usage.tokens.total))
    }

    var body: some View {
        let rows = rows
        let most = rows.map(share).max() ?? 0
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("Breakdown")
                    .font(Fonts.sans(14, .semibold))
                    .foregroundStyle(Palette.text)
                Spacer()
                Segments(options: UsageBreakdown.allCases, selection: $breakdown) {
                    $0 == .model ? "Model" : hourly ? "Hour" : "Day"
                }
            }
            VStack(spacing: 0) {
                HStack(spacing: 0) {
                    Text("#").frame(width: 30, alignment: .leading)
                    Text(breakdown == .model ? "Model" : hourly ? "Hour" : "Day")
                    Spacer(minLength: 12)
                    Text("Cost").frame(width: 100, alignment: .trailing)
                    Text("Share").frame(width: 70, alignment: .trailing)
                    Text("Tokens").frame(width: 76, alignment: .trailing)
                }
                .font(Fonts.sans(12))
                .foregroundStyle(Palette.muted)
                .padding(.bottom, 9)
                Rectangle().fill(Palette.line).frame(height: 1)
                ForEach(Array(rows.enumerated()), id: \.element.id) { index, row in
                    line(index + 1, row, scale: most)
                    if index < rows.count - 1 {
                        Rectangle().fill(Palette.line.opacity(0.7)).frame(height: 1)
                    }
                }
            }
        }
    }

    private func line(_ rank: Int, _ row: Row, scale: Double) -> some View {
        let share = share(row)
        return HStack(alignment: .center, spacing: 0) {
            Text("\(rank)")
                .font(Fonts.sans(12))
                .foregroundStyle(Palette.muted)
                .monospacedDigit()
                .frame(width: 30, alignment: .leading)
            VStack(alignment: .leading, spacing: 6) {
                HStack(spacing: 7) {
                    if let kind = row.kind { AgentMark(kind: kind, size: 14) }
                    Text(row.name)
                        .font(Fonts.sans(13.5))
                        .foregroundStyle(Palette.text)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
                bar(row, share: share, scale: scale)
            }
            Spacer(minLength: 12)
            Group {
                if let cost = row.cost {
                    Text(UsageFormat.money(cost))
                } else {
                    Text("—").help("No known price for this model")
                }
            }
            .frame(width: 100, alignment: .trailing)
            Text(cost && row.cost == nil ? "—" : UsageFormat.percent(share))
                .foregroundStyle(Palette.muted)
                .frame(width: 70, alignment: .trailing)
            Text(UsageFormat.tokens(row.tokens))
                .foregroundStyle(Palette.muted)
                .frame(width: 76, alignment: .trailing)
        }
        .font(Fonts.sans(13.5))
        .monospacedDigit()
        .foregroundStyle(Palette.text)
        .padding(.vertical, 11)
    }

    /// A line under the name as long as its share, against the largest:
    /// the agent's colour, or each agent's part of a day.
    private func bar(_ row: Row, share: Double, scale: Double) -> some View {
        let length = max(240 * share / max(scale, 1e-9), 4)
        let total = row.parts.reduce(0) { $0 + $1.value }
        return HStack(spacing: 0) {
            if let kind = row.kind {
                Capsule().fill(Palette.agent(kind)).frame(width: length)
            } else {
                ForEach(row.parts.filter { $0.value > 0 }) { part in
                    Rectangle().fill(Palette.agent(part.kind)).frame(width: length * part.value / max(total, 1e-9))
                }
            }
        }
        .frame(height: 2.5)
        .clipShape(Capsule())
    }
}

/// A row of choices, the chosen one raised.
struct Segments<Value: Hashable>: View {
    let options: [Value]
    @Binding var selection: Value
    let title: (Value) -> String

    var body: some View {
        HStack(spacing: 2) {
            ForEach(options, id: \.self) { option in
                let chosen = option == selection
                Button { selection = option } label: {
                    Text(title(option))
                        .font(Fonts.sans(12, chosen ? .medium : .regular))
                        .foregroundStyle(chosen ? Palette.text : Palette.muted)
                        .padding(.horizontal, 9)
                        .frame(height: 22)
                        .background {
                            if chosen {
                                RoundedRectangle(cornerRadius: 5, style: .continuous)
                                    .fill(Palette.segment)
                                    .shadow(color: .black.opacity(0.1), radius: 0.5, y: 0.5)
                            }
                        }
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityAddTraits(chosen ? .isSelected : [])
            }
        }
        .padding(2)
        .background(Palette.fill, in: RoundedRectangle(cornerRadius: 7, style: .continuous))
        .fixedSize()
    }
}

/// Lays its items out in a row, wrapping onto more as the room runs out.
private struct FlowRow: Layout {
    var spacing: CGFloat
    var lineSpacing: CGFloat

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let rows = arrange(subviews, in: proposal.width ?? .infinity)
        let width = rows.map { $0.reduce(0) { $0 + $1.width } + spacing * CGFloat(max($0.count - 1, 0)) }.max() ?? 0
        let height = rows.map { $0.map(\.height).max() ?? 0 }.reduce(0, +) + lineSpacing * CGFloat(max(rows.count - 1, 0))
        return CGSize(width: width, height: height)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var y = bounds.minY
        var index = 0
        for row in arrange(subviews, in: bounds.width) {
            var x = bounds.minX
            let height = row.map(\.height).max() ?? 0
            for size in row {
                subviews[index].place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(size))
                x += size.width + spacing
                index += 1
            }
            y += height + lineSpacing
        }
    }

    private func arrange(_ subviews: Subviews, in width: CGFloat) -> [[CGSize]] {
        var rows: [[CGSize]] = [[]]
        var x: CGFloat = 0
        for subview in subviews {
            let size = subview.sizeThatFits(.unspecified)
            if x > 0, x + size.width > width {
                rows.append([])
                x = 0
            }
            rows[rows.count - 1].append(size)
            x += size.width + spacing
        }
        return rows
    }
}

private func fraction(_ part: Double, of whole: Double) -> Double {
    whole > 0 ? part / whole : 0
}

/// How the usage page writes its numbers.
enum UsageFormat {
    static let estimate = "What these tokens would cost through each provider's API. A subscription like Claude Max doesn't bill per token."

    private static let dollars = FloatingPointFormatStyle<Double>.Currency(code: "USD", locale: Locale(identifier: "en_US"))

    /// Dollars and cents; a little under a cent is "<$0.01".
    static func money(_ value: Double) -> String {
        if value > 0, value < 0.005 { return "<$0.01" }
        return value.formatted(dollars.precision(.fractionLength(2)))
    }

    /// A chart's axis: whole dollars, unless its steps aren't.
    static func axisMoney(_ value: Double, step: Double) -> String {
        value.formatted(dollars.precision(.fractionLength(step.rounded() == step ? 0 : 2)))
    }

    /// Three figures, in thousands, millions or billions: 12.6K, 410M, 2.30B.
    static func tokens(_ count: Int64) -> String {
        let value = Double(count)
        for (size, unit) in [(1e9, "B"), (1e6, "M"), (1e3, "K")] where abs(value) >= size * 0.9995 {
            let scaled = value / size
            return String(format: scaled >= 99.95 ? "%.0f" : scaled >= 9.995 ? "%.1f" : "%.2f", scaled) + unit
        }
        return String(count)
    }

    /// A share, to a tenth of a percent; under that, "<0.1%".
    static func percent(_ share: Double) -> String {
        if share <= 0 { return "0%" }
        if share < 0.0005 { return "<0.1%" }
        return String(format: "%.1f%%", share * 100)
    }

    static func sessions(_ count: Int) -> String {
        count == 1 ? "1 session" : "\(count.formatted()) sessions"
    }
}
