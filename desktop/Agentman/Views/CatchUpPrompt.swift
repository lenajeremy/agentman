import SwiftUI

/// The pop-up over the main window when am serve was updated past the app
/// (AppCatchUp): update the app to match, now or not, and whether later
/// ones update without asking. It looks like the setup screen, smaller.
struct CatchUpPrompt: View {
    @Environment(AgentsStore.self) private var store
    /// am serve's version, which the app would update to.
    let version: String
    @State private var automatically = false

    static let width: CGFloat = 440

    var body: some View {
        let catchUp = store.catchUp
        ZStack {
            Color.black.opacity(0.22)
                .ignoresSafeArea()
            VStack(alignment: .leading, spacing: 18) {
                HStack(alignment: .top, spacing: 14) {
                    AgentmanBadge(size: 40)
                    VStack(alignment: .leading, spacing: 5) {
                        Text("am serve was updated to \(version)")
                            .font(Fonts.heading(17))
                            .foregroundStyle(Palette.text)
                        Text(subtitle(app: catchUp.appVersion))
                            .font(TextStyle.body)
                            .foregroundStyle(Palette.muted)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                Toggle("Update automatically when am serve is newer", isOn: $automatically)
                    .toggleStyle(.checkbox)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.textSecondary)
                    .padding(.leading, 54)
                HStack(spacing: 8) {
                    Spacer()
                    SmallButton(title: "Not Now") { catchUp.notNow(automatically: automatically) }
                        .keyboardShortcut(.cancelAction)
                    SmallButton(title: "Update Agentman", primary: true) { catchUp.accept(automatically: automatically) }
                        .keyboardShortcut(.defaultAction)
                }
            }
            .padding(24)
            .frame(width: Self.width)
            .background(Palette.surface, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 18, style: .continuous).strokeBorder(Palette.edge, lineWidth: 1.5))
            .shadow(color: .black.opacity(0.2), radius: 40, y: 16)
        }
        .transition(.opacity)
    }

    private func subtitle(app: String) -> String {
        let now = app.isEmpty ? "" : " This app is \(app), and may not work properly with it until it's updated."
        return "Update Agentman to match?" + now
    }
}
