import Foundation

/// The monitor file (`monitor.json`) that `cumin run` writes. It is the only
/// contract between cumin and this app (docs/ja/designs/status-menu-bar.md).
/// The decoder skips the fields that it does not know.
struct MonitorFile: Decodable, Equatable {
    /// The newest version of the format that this app shows.
    static let contractVersion = 1

    let version: Int
    let lastPoll: LastPoll
    let stopRequested: Bool
    let quota: Quota
    let agents: [Agent]
    let waiting: [Waiting]

    struct LastPoll: Decodable, Equatable {
        let at: Date
        let errors: [PollError]
    }

    struct PollError: Decodable, Equatable {
        let repository: String
        let message: String
    }

    struct Quota: Decodable, Equatable {
        let state: String
        let stoppedWindows: [String]
        let nextTryAt: Date?
    }

    struct Agent: Decodable, Equatable {
        let repository: String
        let issue: Int
        let role: String
        let request: String
        let title: String
        let url: String
    }

    struct Waiting: Decodable, Equatable {
        let repository: String
        let issue: Int
        let kind: String
        let title: String
        let url: String
    }

    enum DecodeError: Error, Equatable {
        /// The file is not a JSON object with an integer `version`, or a
        /// field of a known version is missing or has another type.
        case unreadable
        /// The file has a version that is newer than `contractVersion`.
        case newerVersion(Int)
    }

    private struct VersionOnly: Decodable {
        let version: Int
    }

    /// Reads the version first, so that a newer file is reported as newer
    /// even when its other fields changed.
    static func decode(_ data: Data) -> Result<MonitorFile, DecodeError> {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        decoder.dateDecodingStrategy = .custom { decoder in
            let text = try decoder.singleValueContainer().decode(String.self)
            guard let date = parseTime(text) else {
                throw DecodeError.unreadable
            }
            return date
        }
        guard let head = try? decoder.decode(VersionOnly.self, from: data) else {
            return .failure(.unreadable)
        }
        if head.version > contractVersion {
            return .failure(.newerVersion(head.version))
        }
        guard let file = try? decoder.decode(MonitorFile.self, from: data) else {
            return .failure(.unreadable)
        }
        return .success(file)
    }

    /// Parses an RFC 3339 time. cumin writes whole seconds or a fraction of
    /// a second, as Go formats a time.
    static func parseTime(_ text: String) -> Date? {
        let whole = ISO8601DateFormatter()
        whole.formatOptions = [.withInternetDateTime]
        if let date = whole.date(from: text) { return date }
        let fraction = ISO8601DateFormatter()
        fraction.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return fraction.date(from: text)
    }
}
