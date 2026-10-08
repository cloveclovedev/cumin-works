import XCTest
@testable import CuminStatusBar

/// Reads the golden file of the Go acceptance tests by its path in the
/// repository, so that one file checks the writer and the reader.
func goldenMonitorFile(file: StaticString = #filePath) throws -> Data {
    var url = URL(fileURLWithPath: "\(file)")
    // Tests/CuminStatusBarTests/<file> -> tools/status-bar -> repository root.
    for _ in 0..<5 { url.deleteLastPathComponent() }
    return try Data(contentsOf: url.appendingPathComponent(
        "internal/workflow/testdata/monitor-file.json"))
}

final class MonitorFileTests: XCTestCase {
    func testDecodesTheGoldenFileOfCumin() throws {
        let file = try MonitorFile.decode(try goldenMonitorFile()).get()
        XCTAssertEqual(file.version, 1)
        XCTAssertEqual(file.lastPoll.at, MonitorFile.parseTime("2026-10-04T07:00:05Z"))
        XCTAssertEqual(file.lastPoll.errors, [
            .init(repository: "example/app", message: "read the snapshot: GitHub returned 502"),
        ])
        XCTAssertTrue(file.stopRequested)
        XCTAssertEqual(file.quota, .init(
            state: "stopped", stoppedWindows: ["5h"],
            nextTryAt: MonitorFile.parseTime("2026-10-04T09:00:00Z")))
        XCTAssertEqual(file.agents.map(\.issue), [31, 12])
        XCTAssertEqual(file.agents[0], .init(
            repository: "example/app", issue: 31, role: "planner",
            request: "acceptance check", title: "Show the history of an item",
            url: "https://github.com/example/app/issues/31"))
        XCTAssertEqual(file.waiting.map(\.kind), ["merge-decision", "decision"])
        XCTAssertEqual(file.waiting[0].url, "https://github.com/example/tool/pull/14")
    }

    func testSkipsUnknownFieldsAndAcceptsAMissingNextTry() throws {
        let json = """
        {"version": 1, "added": {"later": true},
         "last_poll": {"at": "2026-10-04T07:00:05.123456789Z", "errors": []},
         "stop_requested": false,
         "quota": {"state": "open", "stopped_windows": []},
         "agents": [], "waiting": []}
        """
        let file = try MonitorFile.decode(Data(json.utf8)).get()
        XCTAssertNil(file.quota.nextTryAt)
        XCTAssertEqual(file.lastPoll.at.timeIntervalSince1970, 1_791_097_205.123, accuracy: 0.001)
    }

    func testReportsANewerVersionEvenWhenTheShapeChanged() {
        let result = MonitorFile.decode(Data(#"{"version": 2, "polls": []}"#.utf8))
        XCTAssertEqual(result, .failure(.newerVersion(2)))
    }

    func testReportsBrokenBytesAsUnreadable() {
        XCTAssertEqual(MonitorFile.decode(Data("{".utf8)), .failure(.unreadable))
        XCTAssertEqual(MonitorFile.decode(Data(#"{"version": 1}"#.utf8)), .failure(.unreadable))
    }
}
