import AppKit
import UserNotifications

/// Agentman's notifications on the Mac: its replies, and cards that need
/// the user, while they are looking elsewhere. Clicking one opens it; a card
/// answered anywhere takes its notification back.
@MainActor
final class Notifier: NSObject, UNUserNotificationCenterDelegate {
    /// Opens what a clicked notification is about.
    var onOpen: ((URL) -> Void)?

    private var center: UNUserNotificationCenter { .current() }
    private var asked = false

    func start() {
        center.delegate = self
    }

    /// Tells the user about news from Agentman; agentName is the agent an
    /// entry is from, when it is one's.
    func announce(_ entry: ManagerEntry, agentName: String? = nil) {
        let content = UNMutableNotificationContent()
        content.threadIdentifier = "agentman"
        content.sound = .default
        var identifier = entry.id
        var link = "agentman://home/conversation"
        if let card = entry.card, card.kind == "note" {
            identifier = card.id
            content.title = "Agentman suggests a note"
            content.body = card.note?.title ?? card.title
            content.sound = nil
        } else if let card = entry.card {
            identifier = card.id
            content.title = "\(agentName ?? card.agentName) needs you"
            content.body = [card.title, card.detail].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: "\n")
            if card.suggested != nil {
                content.subtitle = "Agentman marked its pick for you to confirm"
            }
        } else if entry.role == "brief" || entry.role == "agent" {
            content.title = entry.role == "agent" ? "\(agentName ?? "An agent") says" : "Agentman"
            content.body = entry.text
            if let sessionId = entry.sessionId,
               let encoded = sessionId.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) {
                link = "agentman://agent/\(encoded)"
            }
        } else {
            content.title = "Agentman"
            content.body = Self.plain(entry.text)
        }
        content.body = String(content.body.prefix(400))
        content.userInfo = ["link": link]
        let request = UNNotificationRequest(identifier: identifier, content: content, trigger: nil)
        Task {
            guard await allowed() else { return }
            try? await center.add(request)
        }
    }

    /// Takes back the notification for a card that no longer needs the user.
    func withdraw(_ card: ManagerCard) {
        center.removeDeliveredNotifications(withIdentifiers: [card.id])
        center.removePendingNotificationRequests(withIdentifiers: [card.id])
    }

    /// The user is looking at the conversation: what it notified is seen.
    func clearAll() {
        center.removeAllDeliveredNotifications()
    }

    /// The count on the Dock icon.
    func setBadge(_ count: Int) {
        NSApp.dockTile.badgeLabel = count > 0 ? String(count) : nil
    }

    /// Asks once, the first time there is something to say, rather than at
    /// launch.
    private func allowed() async -> Bool {
        let settings = await center.notificationSettings()
        switch settings.authorizationStatus {
        case .authorized, .provisional:
            return true
        case .notDetermined where !asked:
            asked = true
            return (try? await center.requestAuthorization(options: [.alert, .sound, .badge])) ?? false
        default:
            return false
        }
    }

    /// Markdown flattened to a line of plain text for a notification.
    static func plain(_ markdown: String) -> String {
        var text = markdown
        for mark in ["**", "__", "`", "#"] {
            text = text.replacingOccurrences(of: mark, with: "")
        }
        let lines = text.split(separator: "\n").map { line in
            var line = line.trimmingCharacters(in: .whitespaces)
            for bullet in ["- ", "* ", "• "] where line.hasPrefix(bullet) {
                line = String(line.dropFirst(bullet.count))
            }
            return line
        }
        return lines.filter { !$0.isEmpty }.joined(separator: " ")
    }

    // MARK: - UNUserNotificationCenterDelegate

    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter, willPresent notification: UNNotification
    ) async -> UNNotificationPresentationOptions {
        // The app only posts when the user isn't looking at the
        // conversation, so a notice that arrives in front is still news.
        [.banner, .list, .sound]
    }

    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse
    ) async {
        guard let link = response.notification.request.content.userInfo["link"] as? String,
              let url = URL(string: link) else { return }
        await MainActor.run {
            NSApp.activate()
            onOpen?(url)
        }
    }
}
