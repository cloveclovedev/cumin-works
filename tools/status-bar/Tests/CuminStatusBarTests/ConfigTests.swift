import XCTest
@testable import CuminStatusBar

final class ConfigTests: XCTestCase {
    func testDefaults() {
        let c = Config()
        XCTAssertEqual(c.staleAfterSec, 180)
        XCTAssertEqual(c.soundApproval, "Glass")
        XCTAssertEqual(c.soundAnswer, "Tink")
        XCTAssertEqual(c.blink, .new)
    }

    func testAMissingKeyKeepsItsDefault() {
        let c = Config(raw: ["sound_answer": "Ping", "blink": "off"])
        XCTAssertEqual(c.soundAnswer, "Ping")
        XCTAssertEqual(c.blink, .off)
        XCTAssertEqual(c.staleAfterSec, 180)
        XCTAssertEqual(c.soundApproval, "Glass")
    }

    func testReadsEveryKey() {
        let c = Config(raw: ["stale_after_sec": 600, "sound_approval": "",
                             "sound_answer": "Pop", "blink": "always"])
        XCTAssertEqual(c.staleAfterSec, 600)
        XCTAssertEqual(c.soundApproval, "")
        XCTAssertEqual(c.soundAnswer, "Pop")
        XCTAssertEqual(c.blink, .always)
    }

    func testAnUnknownKeyOrValueKeepsTheDefaults() throws {
        let c = Config(raw: ["later_key": true, "blink": "sometimes", "stale_after_sec": 0,
                             "sound_approval": 3])
        XCTAssertEqual(c, Config())
        XCTAssertEqual(Config.load(from: try write(#"{"blink": true, "stale_after_sec": true}"#)),
                       Config())
        XCTAssertEqual(Config.load(from: try write(#"{"stale_after_sec": 1}"#)).staleAfterSec, 1)
    }

    func testAMissingFileGivesTheDefaults() {
        let c = Config.load(from: URL(fileURLWithPath: "/nonexistent/status-bar.json"))
        XCTAssertEqual(c, Config())
    }

    func testAnUnreadableFileGivesTheDefaults() throws {
        XCTAssertEqual(Config.load(from: try write("{")), Config())
        XCTAssertEqual(Config.load(from: try write("[]")), Config())
    }

    func testLoadsAFile() throws {
        let c = Config.load(from: try write(#"{"sound_approval": "Ping", "stale_after_sec": 90.5}"#))
        XCTAssertEqual(c.soundApproval, "Ping")
        XCTAssertEqual(c.staleAfterSec, 90.5)
    }

    func write(_ text: String) throws -> URL {
        let dir = FileManager.default.temporaryDirectory
            .appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: dir) }
        let url = dir.appendingPathComponent("status-bar.json")
        try Data(text.utf8).write(to: url)
        return url
    }
}
