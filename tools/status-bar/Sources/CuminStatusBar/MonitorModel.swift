import Foundation

/// A segment of the menu bar: one symbol and one count.
enum SegmentKind: Equatable {
    /// Agent runs that continue.
    case running
    /// Issues that wait for the approval of a Maintainer.
    case approval
    /// Issues that wait for the answer of a Maintainer.
    case answer
}

struct BarSegment: Equatable {
    let kind: SegmentKind
    let count: Int
}

/// A row of the opened menu. A click opens `url` in the browser.
struct MenuRow: Equatable {
    let title: String
    let url: String
    /// The segment that counts the row. A waiting issue of a kind that this
    /// app does not know has no segment.
    let segment: SegmentKind?
}

/// What the app shows. `stoppedReason` is set when the app shows no count:
/// the file is old, missing, unreadable, or of a newer version.
struct DisplayOutput: Equatable {
    var segments: [BarSegment] = []
    var agentRows: [MenuRow] = []
    var waitingRows: [MenuRow] = []
    /// The quota state, the stop request, the errors of the last poll, and
    /// the time of the last poll, one line each.
    var statusLines: [String] = []
    var stoppedReason: String?
}

/// The pure decision of the app: from the bytes of the monitor file and the
/// current time to what the app shows. It reads no clock and no file.
enum MonitorModel {
    /// The default limit after which the file is old: three times the default
    /// `poll_interval` of cumin.
    static let defaultStaleAfter: TimeInterval = 180

    static let approvalKinds: Set<String> = ["plan-review", "merge-decision", "acceptance"]
    static let answerKinds: Set<String> = ["decision"]

    /// `data` is nil when the file is missing or the app cannot read it.
    static func evaluate(_ data: Data?, now: Date,
                         staleAfter: TimeInterval = defaultStaleAfter,
                         timeZone: TimeZone = .current) -> DisplayOutput {
        guard let data else {
            return DisplayOutput(stoppedReason: "cumin stopped: no monitor file")
        }
        let file: MonitorFile
        switch MonitorFile.decode(data) {
        case .success(let decoded):
            file = decoded
        case .failure(.unreadable):
            return DisplayOutput(stoppedReason: "cumin stopped: the monitor file is unreadable")
        case .failure(.newerVersion(let version)):
            return DisplayOutput(
                stoppedReason: "The monitor file has version \(version). Update this app.")
        }
        let lastPoll = "Last poll: \(clock(file.lastPoll.at, timeZone))"
        if now.timeIntervalSince(file.lastPoll.at) > staleAfter {
            return DisplayOutput(statusLines: [lastPoll],
                                 stoppedReason: "cumin stopped: the monitor file is old")
        }

        var out = DisplayOutput()
        out.agentRows = file.agents.map { agent in
            MenuRow(
                title: "\(agent.repository)#\(agent.issue) \(agent.title) · \(agent.role), \(agent.request)",
                url: agent.url, segment: .running)
        }
        out.waitingRows = file.waiting.map { waiting in
            MenuRow(
                title: "\(waiting.repository)#\(waiting.issue) \(waiting.title) · \(waiting.kind)",
                url: waiting.url, segment: segment(ofKind: waiting.kind))
        }
        let counts: [(SegmentKind, Int)] = [
            (.running, out.agentRows.count),
            (.approval, out.waitingRows.filter { $0.segment == .approval }.count),
            (.answer, out.waitingRows.filter { $0.segment == .answer }.count),
        ]
        out.segments = counts.filter { $0.1 > 0 }.map { BarSegment(kind: $0.0, count: $0.1) }

        out.statusLines.append(quotaLine(file.quota, timeZone))
        if file.stopRequested {
            out.statusLines.append("Stop requested: cumin stops after the current runs")
        }
        for error in file.lastPoll.errors {
            out.statusLines.append("Poll error: \(error.repository): \(error.message)")
        }
        out.statusLines.append(lastPoll)
        return out
    }

    static func segment(ofKind kind: String) -> SegmentKind? {
        if approvalKinds.contains(kind) { return .approval }
        if answerKinds.contains(kind) { return .answer }
        return nil
    }

    static func quotaLine(_ quota: MonitorFile.Quota, _ timeZone: TimeZone) -> String {
        var line: String
        switch quota.state {
        case "open":
            line = "Quota: open"
        case "stopped":
            line = "Quota: agent starts stopped"
            if !quota.stoppedWindows.isEmpty {
                line += " (\(quota.stoppedWindows.joined(separator: ", ")))"
            }
        case "unread":
            line = "Quota: agent starts stopped (usage unread)"
        default:
            line = "Quota: \(quota.state)"
        }
        if let next = quota.nextTryAt {
            line += ", next try \(clock(next, timeZone))"
        }
        return line
    }

    /// Formats a time as hours and minutes in the given time zone.
    static func clock(_ date: Date, _ timeZone: TimeZone) -> String {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = timeZone
        formatter.dateFormat = "HH:mm"
        return formatter.string(from: date)
    }
}
