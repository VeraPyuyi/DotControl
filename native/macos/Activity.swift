// Installed inside DotControlActivity.app. Only activity metadata is emitted.
import AppKit
import CoreGraphics
import Foundation

if CommandLine.arguments.contains("--request-permission") {
    _ = CGRequestListenEventAccess()
    exit(0)
}

struct Activity: Encodable {
    let available: Bool
    let locked: Bool
    let idle_ms: Int64
    let generation: UInt64
    let foreground_pid: Int32
    let foreground_window: String
}
var generation: UInt64 = 0
var previousIdle: Double = 0
let timer = Timer(timeInterval: 0.02, repeats: true) { _ in
    let idle = CGEventSource.secondsSinceLastEventType(.hidSystemState, eventType: CGEventType(rawValue: UInt32.max)!)
    // HIDSystemState excludes posted application events. A decrease signals
    // hardware activity; no event payload, key code or typed content is read.
    if idle < previousIdle { generation &+= 1 }
    previousIdle = idle
    let session = CGSessionCopyCurrentDictionary() as? [String: Any]
    let locked = session == nil || (session?["CGSSessionScreenIsLocked"] as? Bool ?? false)
    let app = NSWorkspace.shared.frontmostApplication
    let state = Activity(available: idle.isFinite && idle >= 0 && app != nil && CGPreflightListenEventAccess(), locked: locked,
                         idle_ms: idle.isFinite ? Int64(min(idle * 1000, Double(Int64.max / 2))) : 0,
                         generation: generation, foreground_pid: app?.processIdentifier ?? 0,
                         foreground_window: "")
    if let data = try? JSONEncoder().encode(state) {
        FileHandle.standardOutput.write(data)
        FileHandle.standardOutput.write(Data([10]))
    }
}
RunLoop.main.add(timer, forMode: .common)
RunLoop.main.run()
