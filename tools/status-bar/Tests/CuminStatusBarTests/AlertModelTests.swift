import XCTest
@testable import CuminStatusBar

final class AlertModelTests: XCTestCase {
    let polledAt = MonitorFile.parseTime("2026-10-04T07:00:05Z")!

    func file(_ waiting: [(Int, String)], version: Int = 1) -> Data {
        let items = waiting.map { issue, kind in
            """
            {"repository": "example/app", "issue": \(issue), "kind": "\(kind)",
             "title": "T\(issue)", "url": "https://github.com/example/app/issues/\(issue)"}
            """
        }.joined(separator: ",")
        return Data("""
        {"version": \(version),
         "last_poll": {"at": "2026-10-04T07:00:05Z", "errors": []},
         "stop_requested": false, "quota": {"state": "open", "stopped_windows": []},
         "agents": [], "waiting": [\(items)]}
        """.utf8)
    }

    func key(_ issue: Int, _ kind: String) -> WaitingKey {
        WaitingKey(repository: "example/app", issue: issue, kind: kind)
    }

    /// Reads the files one after another, as the app does, and returns the
    /// alert of each read.
    func read(_ files: [Data?], config: Config = Config(),
              secondsAfterPoll: TimeInterval = 10) -> [AlertOutput] {
        var state = AlertState()
        return files.map { data in
            let out = AlertModel.evaluate(
                data, now: polledAt.addingTimeInterval(secondsAfterPoll),
                config: config, previous: state)
            state = out.state
            return out
        }
    }

    func testANewWaitingIssueGivesOneAlert() {
        let outs = read([file([(1, "decision")]), file([(1, "decision"), (2, "merge-decision")])])
        XCTAssertEqual(outs[1].newItems, [key(2, "merge-decision")])
        XCTAssertEqual(outs[1].sounds, ["Glass"])
        XCTAssertEqual(outs[1].blinking, [.approval])
    }

    func testTheSameIssueInTheNextReadGivesNoAlert() {
        let both = file([(1, "decision"), (2, "merge-decision")])
        let outs = read([file([(1, "decision")]), both, both])
        XCTAssertEqual(outs[2].newItems, [])
        XCTAssertEqual(outs[2].sounds, [])
    }

    func testTheFirstReadGivesNoAlert() {
        let outs = read([file([(1, "decision"), (2, "merge-decision")])])
        XCTAssertEqual(outs[0].newItems, [])
        XCTAssertEqual(outs[0].sounds, [])
        XCTAssertEqual(outs[0].blinking, [])
    }

    func testAnIssueThatLeavesAndComesBackGivesANewAlert() {
        let one = file([(1, "decision")])
        let outs = read([one, file([]), one])
        XCTAssertEqual(outs[1].newItems, [])
        XCTAssertEqual(outs[2].newItems, [key(1, "decision")])
        XCTAssertEqual(outs[2].sounds, ["Tink"])
    }

    func testTheSameIssueWithAnotherKindIsANewItem() {
        let outs = read([file([(1, "plan-review")]), file([(1, "decision")])])
        XCTAssertEqual(outs[1].newItems, [key(1, "decision")])
        XCTAssertEqual(outs[1].sounds, ["Tink"])
    }

    func testEachWaitingSegmentPlaysItsOwnSoundOnce() {
        let outs = read([file([]), file([
            (1, "plan-review"), (2, "merge-decision"), (3, "acceptance"), (4, "decision"),
        ])])
        XCTAssertEqual(outs[1].newItems.count, 4)
        XCTAssertEqual(outs[1].sounds, ["Glass", "Tink"])
        XCTAssertEqual(outs[1].blinking, [.approval, .answer])
    }

    func testAnEmptySoundNameGivesNoSound() {
        var config = Config()
        config.soundApproval = ""
        let outs = read([file([]), file([(1, "merge-decision"), (2, "decision")])], config: config)
        XCTAssertEqual(outs[1].newItems.count, 2)
        XCTAssertEqual(outs[1].sounds, ["Tink"])
        XCTAssertEqual(outs[1].blinking, [.approval, .answer])
    }

    func testANewItemOfAnUnknownKindGivesNoSoundAndNoBlink() {
        let outs = read([file([]), file([(1, "later-kind")])])
        XCTAssertEqual(outs[1].newItems, [key(1, "later-kind")])
        XCTAssertEqual(outs[1].sounds, [])
        XCTAssertEqual(outs[1].blinking, [])
    }

    func testTheBlinkOfANewItemLastsUntilTheMenuOpens() {
        let both = file([(1, "decision"), (2, "merge-decision")])
        var state = AlertState()
        func next(_ data: Data) -> AlertOutput {
            let out = AlertModel.evaluate(data, now: polledAt, config: Config(), previous: state)
            state = out.state
            return out
        }
        _ = next(file([(1, "decision")]))
        XCTAssertEqual(next(both).blinking, [.approval])
        XCTAssertEqual(next(both).blinking, [.approval])
        state.menuOpened()
        XCTAssertEqual(next(both).blinking, [])
    }

    func testTheBlinkStopsWhenTheSegmentLeaves() {
        let outs = read([file([]), file([(1, "decision")]), file([]), file([(2, "merge-decision")])])
        XCTAssertEqual(outs[2].blinking, [])
        XCTAssertEqual(outs[3].blinking, [.approval])
    }

    func testBlinkOffNeverBlinksAndStillPlaysTheSound() {
        var config = Config()
        config.blink = .off
        let outs = read([file([]), file([(1, "decision")])], config: config)
        XCTAssertEqual(outs[1].sounds, ["Tink"])
        XCTAssertEqual(outs[1].blinking, [])
    }

    func testBlinkAlwaysBlinksEveryShownWaitingSegment() {
        var config = Config()
        config.blink = .always
        let outs = read([file([(1, "decision")]), file([(1, "decision")]), file([])],
                        config: config)
        XCTAssertEqual(outs[0].blinking, [.answer])
        XCTAssertEqual(outs[0].sounds, [])
        XCTAssertEqual(outs[1].blinking, [.answer])
        XCTAssertEqual(outs[2].blinking, [])
    }

    func testAnOldFileCountsNoNewItem() {
        var state = AlertState()
        func next(_ data: Data?, secondsAfterPoll: TimeInterval) -> AlertOutput {
            let out = AlertModel.evaluate(
                data, now: polledAt.addingTimeInterval(secondsAfterPoll),
                config: Config(), previous: state)
            state = out.state
            return out
        }
        _ = next(file([]), secondsAfterPoll: 10)
        let old = next(file([(1, "decision")]), secondsAfterPoll: 181)
        XCTAssertEqual(old.newItems, [])
        XCTAssertEqual(old.sounds, [])
        XCTAssertEqual(old.blinking, [])
        // The same item in a file that is not old is still new.
        XCTAssertEqual(next(file([(1, "decision")]), secondsAfterPoll: 180).sounds, ["Tink"])
    }

    func testTheFirstReadAfterAnOldFileIsSilent() {
        var state = AlertState()
        let old = AlertModel.evaluate(file([]), now: polledAt.addingTimeInterval(181),
                                      config: Config(), previous: state)
        state = old.state
        let first = AlertModel.evaluate(file([(1, "decision")]), now: polledAt,
                                        config: Config(), previous: state)
        XCTAssertEqual(first.newItems, [])
        XCTAssertEqual(first.sounds, [])
    }

    func testTheLimitOfAnOldFileComesFromTheConfiguration() {
        var config = Config()
        config.staleAfterSec = 600
        let outs = read([file([]), file([(1, "decision")])], config: config, secondsAfterPoll: 400)
        XCTAssertEqual(outs[1].sounds, ["Tink"])
    }

    func testAMissingUnreadableOrNewerFileKeepsWhatTheAppSaw() {
        let one = file([(1, "decision")])
        let outs = read([one, nil, Data("{".utf8), file([], version: 2), one])
        for out in outs[1...3] {
            XCTAssertEqual(out.newItems, [])
            XCTAssertEqual(out.state.seen, [key(1, "decision")])
        }
        XCTAssertEqual(outs[4].newItems, [])
        XCTAssertEqual(outs[4].sounds, [])
    }
}
