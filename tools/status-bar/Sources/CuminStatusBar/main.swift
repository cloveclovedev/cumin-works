import AppKit

// MARK: - Monitor file (the contract location)

/// `~/.local/state/cumin/monitor.json`, the file that `cumin run` writes.
/// The app only reads it.
let monitorFileURL = FileManager.default.homeDirectoryForCurrentUser
    .appendingPathComponent(".local/state/cumin/monitor.json")

// MARK: - Bar rendering (composed template image, monochrome)

enum BarRenderer {
    static let glyphs: [SegmentKind: String] = [
        .running: "play.fill",
        .approval: "hand.raised.fill",
        .answer: "questionmark.circle.fill",
    ]
    static let neutralGlyph = "circle"

    static func symbol(_ name: String) -> NSImage? {
        NSImage(systemSymbolName: name, accessibilityDescription: name)?
            .withSymbolConfiguration(.init(pointSize: 12, weight: .regular))
    }

    /// Draws glyph+count segments in black into a single image and marks it
    /// as a template, so the menu bar tints it (white on dark, black on
    /// light) automatically. With no segment it draws the neutral glyph,
    /// dimmed when cumin stopped, so that the item never disappears. Blink
    /// is a per-segment alpha dip: template rendering derives shape from the
    /// alpha channel, so 0.25-alpha drawing shows as dimmed.
    static func image(for segments: [BarSegment], stopped: Bool,
                      blinking: Set<SegmentKind> = [], blinkOn: Bool = true) -> NSImage {
        let font = NSFont.monospacedDigitSystemFont(ofSize: 12, weight: .medium)
        let height: CGFloat = 18
        let gap: CGFloat = 7
        let innerGap: CGFloat = 2

        var items: [(glyph: NSImage, count: NSAttributedString, alpha: CGFloat)] = []
        if segments.isEmpty {
            if let g = symbol(neutralGlyph) {
                items.append((g, NSAttributedString(), stopped ? 0.35 : 1.0))
            }
        } else {
            for seg in segments {
                guard let g = symbol(glyphs[seg.kind]!) else { continue }
                let count = NSAttributedString(
                    string: "\(seg.count)",
                    attributes: [.font: font, .foregroundColor: NSColor.black])
                let alpha: CGFloat = (blinking.contains(seg.kind) && !blinkOn) ? 0.25 : 1.0
                items.append((g, count, alpha))
            }
        }

        var width: CGFloat = 0
        for item in items {
            width += item.glyph.size.width
            if item.count.length > 0 { width += innerGap + item.count.size().width }
            width += gap
        }
        width = max(width - gap, 1)

        let image = NSImage(size: NSSize(width: width, height: height), flipped: false) { _ in
            var x: CGFloat = 0
            for item in items {
                let glyphY = (height - item.glyph.size.height) / 2
                item.glyph.draw(at: NSPoint(x: x, y: glyphY), from: .zero,
                                operation: .sourceOver, fraction: item.alpha)
                x += item.glyph.size.width
                if item.count.length > 0 {
                    x += innerGap
                    let faded = NSMutableAttributedString(attributedString: item.count)
                    faded.addAttribute(
                        .foregroundColor,
                        value: NSColor.black.withAlphaComponent(item.alpha),
                        range: NSRange(location: 0, length: faded.length))
                    let size = faded.size()
                    faded.draw(at: NSPoint(x: x, y: (height - size.height) / 2))
                    x += size.width
                }
                x += gap
            }
            return true
        }
        image.isTemplate = true
        return image
    }
}

// MARK: - Controller

final class StatusController: NSObject, NSApplicationDelegate, NSMenuDelegate {
    private var statusItem: NSStatusItem!
    private var pollTimer: Timer?
    private var blinkTimer: Timer?
    private var blinkOn = true
    private var lastOutput = DisplayOutput()
    private var config = Config()
    private var alertState = AlertState()
    private var blinking: Set<SegmentKind> = []

    func applicationDidFinishLaunching(_ notification: Notification) {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        // .common mode keeps both timers firing while the dropdown menu is
        // open (menu tracking runs the run loop outside .default mode).
        let poll = Timer(timeInterval: 5, repeats: true) { [weak self] _ in
            self?.refresh()
        }
        RunLoop.main.add(poll, forMode: .common)
        pollTimer = poll
        let blink = Timer(timeInterval: 0.5, repeats: true) { [weak self] _ in
            guard let self, !self.blinking.isEmpty else { return }
            self.blinkOn.toggle()
            self.render()
        }
        RunLoop.main.add(blink, forMode: .common)
        blinkTimer = blink
        refresh()
    }

    private func refresh() {
        let data = try? Data(contentsOf: monitorFileURL)
        config = Config.load()
        let now = Date()
        let out = MonitorModel.evaluate(data, now: now, staleAfter: config.staleAfterSec)
        let alert = AlertModel.evaluate(data, now: now, config: config, previous: alertState)
        for name in alert.sounds { NSSound(named: name)?.play() }
        alertState = alert.state
        blinking = alert.blinking
        if blinking.isEmpty { blinkOn = true }
        // An open menu keeps its items, so rebuild only on a change.
        let changed = statusItem.menu == nil || out != lastOutput
        lastOutput = out
        render()
        if changed { rebuildMenu() }
    }

    private func render() {
        statusItem.button?.image = BarRenderer.image(
            for: lastOutput.segments, stopped: lastOutput.stoppedReason != nil,
            blinking: blinking, blinkOn: blinkOn)
    }

    /// The Maintainer opened the menu, so a blink for a new item stops.
    func menuWillOpen(_ menu: NSMenu) {
        alertState.menuOpened()
        if config.blink == .new { blinking = [] }
        if blinking.isEmpty { blinkOn = true }
        render()
    }

    private func rebuildMenu() {
        let menu = NSMenu()
        menu.delegate = self
        if let reason = lastOutput.stoppedReason {
            menu.addItem(NSMenuItem(title: reason, action: nil, keyEquivalent: ""))
        } else {
            let rows = lastOutput.agentRows + lastOutput.waitingRows
            for row in rows {
                let item = NSMenuItem(title: row.title, action: #selector(openRow(_:)),
                                      keyEquivalent: "")
                item.target = self
                item.representedObject = row.url
                if let segment = row.segment {
                    item.image = BarRenderer.symbol(BarRenderer.glyphs[segment]!)
                }
                menu.addItem(item)
            }
            if rows.isEmpty {
                menu.addItem(NSMenuItem(title: "No running agent, no waiting issue",
                                        action: nil, keyEquivalent: ""))
            }
        }
        menu.addItem(.separator())
        for line in lastOutput.statusLines {
            menu.addItem(NSMenuItem(title: line, action: nil, keyEquivalent: ""))
        }
        if !lastOutput.statusLines.isEmpty { menu.addItem(.separator()) }
        menu.addItem(NSMenuItem(title: "Quit",
                                action: #selector(NSApplication.terminate(_:)),
                                keyEquivalent: "q"))
        statusItem.menu = menu
    }

    /// Opens the link of the row in the default browser. The app itself
    /// makes no network call.
    @objc private func openRow(_ sender: NSMenuItem) {
        guard let text = sender.representedObject as? String,
              let url = URL(string: text),
              url.scheme == "https" else { return }
        NSWorkspace.shared.open(url)
    }
}

// MARK: - Entry point

let app = NSApplication.shared
let controller = StatusController()
app.delegate = controller
app.setActivationPolicy(.accessory)
app.run()
