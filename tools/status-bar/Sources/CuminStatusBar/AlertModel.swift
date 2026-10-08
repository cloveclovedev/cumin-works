import Foundation

/// What identifies an item of `waiting`. The same issue with another kind is
/// a new item.
struct WaitingKey: Hashable {
    let repository: String
    let issue: Int
    let kind: String
}

/// What the app remembers between two reads. The app keeps it in memory
/// only.
struct AlertState: Equatable {
    /// The items of the last read of a file that was not old. It is nil
    /// until the first such read, so that the first read is silent.
    var seen: Set<WaitingKey>?
    /// The waiting segments that got a new item after the menu last opened.
    var unopened: Set<SegmentKind> = []

    /// The menu opened: the Maintainer saw the new items.
    mutating func menuOpened() {
        unopened = []
    }
}

/// How the app tells of a new item after one read.
struct AlertOutput: Equatable {
    /// The waiting items that the last read did not hold, in the order of
    /// the file.
    var newItems: [WaitingKey] = []
    /// The names of the system sounds to play, one for each waiting segment
    /// with a new item.
    var sounds: [String] = []
    /// The segments that blink.
    var blinking: Set<SegmentKind> = []
    /// The state for the next read.
    var state: AlertState
}

/// The pure decision of the alert: from the bytes of the monitor file, the
/// configuration, the current time, and the items of the last read to the
/// sounds and the segments that blink. It plays no sound and reads no clock.
enum AlertModel {
    static func evaluate(_ data: Data?, now: Date, config: Config,
                         previous: AlertState) -> AlertOutput {
        // An old, missing, unreadable, or newer file shows no segment and
        // counts no new item. The app keeps what it saw before.
        guard let data, case .success(let file) = MonitorFile.decode(data),
              now.timeIntervalSince(file.lastPoll.at) <= config.staleAfterSec else {
            return AlertOutput(state: previous)
        }

        let current = file.waiting.map {
            WaitingKey(repository: $0.repository, issue: $0.issue, kind: $0.kind)
        }
        var out = AlertOutput(state: previous)
        if let seen = previous.seen {
            out.newItems = current.filter { !seen.contains($0) }
        }
        out.state.seen = Set(current)

        let shown = Set(current.compactMap { MonitorModel.segment(ofKind: $0.kind) })
        let alerted = Set(out.newItems.compactMap { MonitorModel.segment(ofKind: $0.kind) })
        let sounds: [(SegmentKind, String)] = [
            (.approval, config.soundApproval),
            (.answer, config.soundAnswer),
        ]
        out.sounds = sounds.filter { alerted.contains($0.0) && !$0.1.isEmpty }.map { $0.1 }
        // A segment that the app no longer shows has nothing left to open.
        out.state.unopened = previous.unopened.union(alerted).intersection(shown)

        switch config.blink {
        case .off: out.blinking = []
        case .new: out.blinking = out.state.unopened
        case .always: out.blinking = shown
        }
        return out
    }
}
