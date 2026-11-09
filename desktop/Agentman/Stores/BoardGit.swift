import Foundation

/// How far a git button on a Board card has got.
struct BoardGitRun: Equatable {
    enum Phase: Equatable {
        case running(String)
        case done(String, detail: String, url: String?)
        case failed(String, detail: String, retry: GitAction)
    }

    var id = UUID()
    var phase: Phase

    var isRunning: Bool {
        if case .running = phase { true } else { false }
    }
}

extension AgentsStore {
    /// Pushes or opens a pull request from the card, which says how it went.
    /// Committing has its own modal: there is a message to write.
    func perform(_ action: GitAction, on session: String) {
        let step: GitStep
        let doneLabel: String
        switch action {
        case .push: (step, doneLabel) = (.push, "Pushed")
        case .createPR: (step, doneLabel) = (.createPR, "Pull request open")
        case .commit, .idle: return
        }
        guard boardGitRuns[session]?.isRunning != true else { return }
        let run = BoardGitRun(phase: .running(step.running))
        boardGitRuns[session] = run
        Task {
            let outcome = await self.run([step], session: session) { _ in }
            let phase: BoardGitRun.Phase = outcome.failed
                ? .failed(step.failed, detail: outcome.text, retry: action)
                : .done(doneLabel, detail: outcome.text, url: outcome.url)
            boardGitRuns[session] = BoardGitRun(id: run.id, phase: phase)
            guard !outcome.failed else { return }
            // A failure waits to be read; this goes by itself, a link for longer.
            try? await Task.sleep(for: .seconds(outcome.url == nil ? 10 : 30))
            if boardGitRuns[session]?.id == run.id { boardGitRuns[session] = nil }
        }
    }
}
