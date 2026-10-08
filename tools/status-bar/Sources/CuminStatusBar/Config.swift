import Foundation

/// The configuration of the app (`status-bar.json`), which the Operator
/// edits. Every key is optional, and a missing key keeps its default
/// (docs/ja/designs/status-menu-bar.md).
struct Config: Equatable {
    /// When the two waiting segments blink.
    enum Blink: String {
        /// The segments never blink.
        case off
        /// A segment blinks after a new item, until the menu opens.
        case new
        /// A segment blinks while the app shows it.
        case always
    }

    /// The limit after which the monitor file is old, in seconds.
    var staleAfterSec: Double = MonitorModel.defaultStaleAfter
    /// The system sound for a new item that waits for an approval. An empty
    /// name plays no sound.
    var soundApproval: String = "Glass"
    /// The system sound for a new item that waits for an answer. An empty
    /// name plays no sound.
    var soundAnswer: String = "Tink"
    var blink: Blink = .new

    /// `~/.config/cumin/status-bar.json`. The app only reads it.
    static let defaultURL = FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent(".config/cumin/status-bar.json")

    init() {}

    /// A key with another type, or with a value that the app does not know,
    /// keeps its default.
    init(raw: [String: Any]) {
        if let v = raw["stale_after_sec"] as? NSNumber,
           CFGetTypeID(v) != CFBooleanGetTypeID(), v.doubleValue > 0 {
            staleAfterSec = v.doubleValue
        }
        if let v = raw["sound_approval"] as? String { soundApproval = v }
        if let v = raw["sound_answer"] as? String { soundAnswer = v }
        if let v = raw["blink"] as? String, let mode = Blink(rawValue: v) { blink = mode }
    }

    /// A missing or unreadable file gives the defaults.
    static func load(from url: URL = defaultURL) -> Config {
        guard let data = try? Data(contentsOf: url),
              let raw = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
        else { return Config() }
        return Config(raw: raw)
    }
}
