import XCTest
@testable import CuminStatusBar

final class MonitorModelTests: XCTestCase {
    let utc = TimeZone(identifier: "UTC")!
    /// The golden file has `last_poll.at` 2026-10-04T07:00:05Z.
    let polledAt = MonitorFile.parseTime("2026-10-04T07:00:05Z")!

    func evaluate(_ data: Data?, secondsAfterPoll: TimeInterval = 10) -> DisplayOutput {
        MonitorModel.evaluate(data, now: polledAt.addingTimeInterval(secondsAfterPoll),
                              timeZone: utc)
    }

    func file(version: Int = 1, waiting: String = "[]", agents: String = "[]",
              quota: String = #"{"state": "open", "stopped_windows": []}"#) -> Data {
        Data("""
        {"version": \(version),
         "last_poll": {"at": "2026-10-04T07:00:05Z", "errors": []},
         "stop_requested": false, "quota": \(quota),
         "agents": \(agents), "waiting": \(waiting)}
        """.utf8)
    }

    func waiting(_ issue: Int, _ kind: String) -> String {
        """
        {"repository": "example/app", "issue": \(issue), "kind": "\(kind)",
         "title": "T\(issue)", "url": "https://github.com/example/app/issues/\(issue)"}
        """
    }

    func testCountsRunningAgentsAndWaitingIssues() throws {
        let out = evaluate(try goldenMonitorFile())
        XCTAssertNil(out.stoppedReason)
        XCTAssertEqual(out.segments, [
            BarSegment(kind: .running, count: 2),
            BarSegment(kind: .approval, count: 1),
            BarSegment(kind: .answer, count: 1),
        ])
    }

    func testCountsTheThreeApprovalKindsTogetherAndHidesAZeroCount() {
        let out = evaluate(file(waiting: "[" + [
            waiting(1, "plan-review"), waiting(2, "merge-decision"), waiting(3, "acceptance"),
        ].joined(separator: ",") + "]"))
        XCTAssertEqual(out.segments, [BarSegment(kind: .approval, count: 3)])
    }

    func testListsAnUnknownKindWithoutCountingIt() {
        let out = evaluate(file(waiting: "[" + waiting(4, "later-kind") + "]"))
        XCTAssertEqual(out.segments, [])
        XCTAssertEqual(out.waitingRows, [
            MenuRow(title: "example/app#4 T4 · later-kind",
                    url: "https://github.com/example/app/issues/4", segment: nil),
        ])
    }

    func testListsTheRowsWithTheirLinks() throws {
        let out = evaluate(try goldenMonitorFile())
        XCTAssertEqual(out.agentRows, [
            MenuRow(title: "example/app#31 Show the history of an item · planner, acceptance check",
                    url: "https://github.com/example/app/issues/31", segment: .running),
            MenuRow(title: "example/tool#12 feat(api): add the list endpoint · implementer, implement",
                    url: "https://github.com/example/tool/issues/12", segment: .running),
        ])
        XCTAssertEqual(out.waitingRows, [
            MenuRow(title: "example/tool#9 fix(api): return 404 for a missing item · merge-decision",
                    url: "https://github.com/example/tool/pull/14", segment: .approval),
            MenuRow(title: "example/app#31 Show the history of an item · decision",
                    url: "https://github.com/example/app/issues/31", segment: .answer),
        ])
    }

    func testShowsTheQuotaTheStopRequestAndThePollError() throws {
        let out = evaluate(try goldenMonitorFile())
        XCTAssertEqual(out.statusLines, [
            "Quota: agent starts stopped (5h), next try 09:00",
            "Stop requested: cumin stops after the current runs",
            "Poll error: example/app: read the snapshot: GitHub returned 502",
            "Last poll: 07:00",
        ])
    }

    func testShowsAnOpenAndAnUnreadQuota() {
        XCTAssertEqual(evaluate(file()).statusLines, ["Quota: open", "Last poll: 07:00"])
        let unread = file(quota: """
        {"state": "unread", "stopped_windows": [], "next_try_at": "2026-10-04T07:10:00Z"}
        """)
        XCTAssertEqual(evaluate(unread).statusLines.first,
                       "Quota: agent starts stopped (usage unread), next try 07:10")
    }

    func testShowsAnEmptyFileWithoutSegments() {
        let out = evaluate(file())
        XCTAssertNil(out.stoppedReason)
        XCTAssertEqual(out.segments, [])
        XCTAssertEqual(out.agentRows + out.waitingRows, [])
    }

    func testShowsAnOldFileAsStopped() throws {
        let golden = try goldenMonitorFile()
        XCTAssertNil(evaluate(golden, secondsAfterPoll: 180).stoppedReason)
        let out = evaluate(golden, secondsAfterPoll: 181)
        XCTAssertEqual(out.stoppedReason, "cumin stopped: the monitor file is old")
        XCTAssertEqual(out.segments, [])
        XCTAssertEqual(out.agentRows + out.waitingRows, [])
        XCTAssertEqual(out.statusLines, ["Last poll: 07:00"])
    }

    func testTakesTheLimitOfAnOldFileAsAnArgument() throws {
        let out = MonitorModel.evaluate(try goldenMonitorFile(),
                                        now: polledAt.addingTimeInterval(400),
                                        staleAfter: 600, timeZone: utc)
        XCTAssertNil(out.stoppedReason)
    }

    func testShowsAMissingFileAsStopped() {
        XCTAssertEqual(evaluate(nil),
                       DisplayOutput(stoppedReason: "cumin stopped: no monitor file"))
    }

    func testShowsAnUnreadableFileAsStopped() {
        XCTAssertEqual(evaluate(Data("{".utf8)),
                       DisplayOutput(stoppedReason: "cumin stopped: the monitor file is unreadable"))
    }

    func testDoesNotShowAFileWithANewerVersion() {
        let out = evaluate(file(version: 2, waiting: "[" + waiting(1, "decision") + "]"))
        XCTAssertEqual(out, DisplayOutput(
            stoppedReason: "The monitor file has version 2. Update this app."))
    }
}
