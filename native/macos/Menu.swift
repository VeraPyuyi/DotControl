// User-session menu. It never receives runtime keys or desktop content.
import AppKit
import Foundation

final class MenuDelegate: NSObject, NSApplicationDelegate {
    var item: NSStatusItem!
    var executable = ""
    var state = ""
    func applicationDidFinishLaunching(_ notification: Notification) {
        let arguments = CommandLine.arguments
        guard arguments.count == 3 else { NSApp.terminate(nil); return }
        executable = arguments[1]; state = arguments[2]
        item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        item.button?.title = "◉"
        item.button?.toolTip = "DotControl"
        let menu = NSMenu()
        for (label, selector) in [("DotControl · 状态与连接", #selector(openWizard)),
                                   ("暂停桌面任务", #selector(pause)),
                                   ("继续桌面任务", #selector(resume)),
                                   ("修复连接", #selector(repair)),
                                   ("退出 DotControl", #selector(quit))] {
            let action = NSMenuItem(title: label, action: selector, keyEquivalent: "")
            action.target = self; menu.addItem(action)
        }
        item.menu = menu
    }
    func run(_ command: String) {
        let process = Process(); process.executableURL = URL(fileURLWithPath: executable)
        process.arguments = ["--state", state, command]
        process.standardOutput = FileHandle.nullDevice; process.standardError = FileHandle.nullDevice
        try? process.run()
    }
    @objc func openWizard() { run("onboard") }
    @objc func pause() { run("pause") }
    @objc func resume() { run("resume") }
    @objc func repair() { run("repair") }
    @objc func quit() { run("quit"); NSApp.terminate(nil) }
}
let app = NSApplication.shared
let delegate = MenuDelegate()
app.delegate = delegate
app.setActivationPolicy(.accessory)
app.run()
