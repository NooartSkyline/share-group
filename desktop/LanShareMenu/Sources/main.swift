import AppKit
import Foundation

final class AppDelegate: NSObject, NSApplicationDelegate {
    private let status = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
    private let menu = NSMenu()
    private var server: Process?
    private var poll: Timer?
    private var info = Info()
    private var launching = false
    private let dashboard = DashboardWindow()

    private let roomItem = NSMenuItem(title: "ห้อง: —", action: nil, keyEquivalent: "")
    private let pinItem = NSMenuItem(title: "PIN: —", action: #selector(copyPIN), keyEquivalent: "")
    private let urlItem = NSMenuItem(title: "คัดลอก URL", action: #selector(copyURL), keyEquivalent: "")
    private let nicItem = NSMenuItem(title: "เครือข่าย: —", action: nil, keyEquivalent: "")
    private let devicesItem = NSMenuItem(title: "อุปกรณ์: —", action: nil, keyEquivalent: "")
    private let pendingItem = NSMenuItem(title: "รออนุมัติ: —", action: nil, keyEquivalent: "")
    private let toggleItem = NSMenuItem(title: "เปิดเซิร์ฟเวอร์", action: #selector(toggleServer), keyEquivalent: "")
    private let loginItem = NSMenuItem(title: "เปิดเมื่อเข้าสู่ระบบ", action: #selector(toggleLogin), keyEquivalent: "")

    func applicationDidFinishLaunching(_ notification: Notification) {
        if let btn = status.button {
            btn.toolTip = "Share Group"
            applyStatusIcon(btn, running: true)
        }
        roomItem.isEnabled = false
        nicItem.isEnabled = false
        devicesItem.isEnabled = false
        pendingItem.isEnabled = false
        for item in [roomItem, pinItem, urlItem, nicItem, devicesItem, pendingItem] {
            menu.addItem(item)
        }
        menu.addItem(.separator())
        let dash = NSMenuItem(title: "เปิดโปรแกรม", action: #selector(openDashboard), keyEquivalent: "o")
        let web = NSMenuItem(title: "เปิดในเบราว์เซอร์", action: #selector(openInBrowser), keyEquivalent: "b")
        let quit = NSMenuItem(title: "ออก", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        for item in [dash, web, toggleItem, loginItem] { item.target = self }
        quit.target = NSApp
        menu.addItem(dash)
        menu.addItem(web)
        menu.addItem(toggleItem)
        menu.addItem(loginItem)
        menu.addItem(.separator())
        menu.addItem(quit)
        pinItem.target = self
        urlItem.target = self
        status.menu = menu
        dashboard.onClose = { [weak self] in self?.hideAppWindow() }
        refreshLoginState()
        startServerIfNeeded()
        if ProcessInfo.processInfo.environment["LAUNCHED_AT_LOGIN"] != "1" {
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.4) { [weak self] in
                self?.openDashboard()
            }
        }
        poll = Timer.scheduledTimer(withTimeInterval: 2, repeats: true) { [weak self] _ in
            self?.refresh()
        }
        poll?.tolerance = 0.5
        refresh()
        NSApp.servicesProvider = self
        NSUpdateDynamicServices()
    }

    func applicationWillTerminate(_ notification: Notification) {
        poll?.invalidate()
        stopServer()
    }

    @objc func sendToLANShare(_ pboard: NSPasteboard, userData: String, error: AutoreleasingUnsafeMutablePointer<NSString?>) {
        let urls = (pboard.readObjects(forClasses: [NSURL.self], options: [.urlReadingFileURLsOnly: true]) as? [URL]) ?? []
        sendFiles(urls)
    }

    func application(_ application: NSApplication, open urls: [URL]) {
        sendFiles(urls)
    }

    private func sendFiles(_ urls: [URL]) {
        guard let share = shareBinary() else { return }
        let proc = Process()
        proc.executableURL = share
        proc.arguments = ["send"] + urls.map(\.path)
        proc.standardOutput = FileHandle.nullDevice
        proc.standardError = FileHandle.nullDevice
        try? proc.run()
    }

    @objc private func copyPIN() {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(info.pin, forType: .string)
    }

    @objc private func copyURL() {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(info.url, forType: .string)
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        openDashboard()
        return true
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        false
    }

    @objc private func openDashboard() {
        startServerIfNeeded()
        showAppWindow()
        dashboard.show(port: port())
    }

    @objc private func openInBrowser() {
        startServerIfNeeded()
        NSWorkspace.shared.open(URL(string: "http://127.0.0.1:\(port())/")!)
    }

    private func showAppWindow() {
        NSApp.setActivationPolicy(.regular)
        NSApp.activate(ignoringOtherApps: true)
    }

    private func hideAppWindow() {
        if !dashboard.isVisible {
            NSApp.setActivationPolicy(.accessory)
        }
    }

    @objc private func toggleServer() {
        if serverRunning() {
            stopServer()
        } else {
            startServerIfNeeded()
        }
        refresh()
    }

    @objc private func toggleLogin() {
        setLaunchAtLogin(!isLaunchAtLogin())
        refreshLoginState()
    }

    private func refreshLoginState() {
        loginItem.state = isLaunchAtLogin() ? .on : .off
    }

    private func refresh() {
        applyMenu()
        let url = URL(string: "http://127.0.0.1:\(port())/api/shortcuts/info")!
        var req = URLRequest(url: url, timeoutInterval: 1.5)
        req.cachePolicy = .reloadIgnoringLocalCacheData
        URLSession.shared.dataTask(with: req) { [weak self] data, resp, _ in
            var next = Info()
            if let data, (resp as? HTTPURLResponse)?.statusCode == 200,
               let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] {
                next.ok = true
                next.roomName = obj["roomName"] as? String ?? ""
                next.pin = obj["pin"] as? String ?? ""
                next.url = obj["url"] as? String ?? ""
                if let iface = obj["interface"] as? [String: Any] {
                    let name = iface["name"] as? String ?? ""
                    let ip = iface["ip"] as? String ?? ""
                    next.iface = [name, ip].filter { !$0.isEmpty }.joined(separator: " • ")
                }
                next.deviceCount = (obj["deviceCount"] as? NSNumber)?.intValue ?? 0
                next.pendingCount = (obj["pendingCount"] as? NSNumber)?.intValue ?? 0
            }
            DispatchQueue.main.async {
                self?.info = next
                self?.applyMenu()
            }
        }.resume()
    }

    private func applyStatusIcon(_ btn: NSStatusBarButton, running: Bool) {
        if let url = Bundle.main.url(forResource: "MenuTemplate", withExtension: "png"),
           let img = NSImage(contentsOf: url) {
            img.isTemplate = true
            img.size = NSSize(width: 18, height: 18)
            btn.image = img
            btn.imagePosition = .imageOnly
            btn.title = ""
            btn.appearsDisabled = !running
            return
        }
        btn.image = nil
        btn.title = running ? "▢" : "—"
        btn.appearsDisabled = false
    }

    private func applyMenu() {
        let running = serverRunning() || info.ok
        if let btn = status.button {
            applyStatusIcon(btn, running: running)
        }
        roomItem.title = "ห้อง: \(info.roomName.isEmpty ? "—" : info.roomName)"
        pinItem.title = info.pin.isEmpty ? "PIN: —" : "PIN: \(info.pin)  (คัดลอก)"
        urlItem.isEnabled = !info.url.isEmpty
        nicItem.title = info.iface.isEmpty ? "เครือข่าย: —" : "เครือข่าย: \(info.iface)"
        devicesItem.title = "อุปกรณ์: \(info.deviceCount)"
        pendingItem.title = "รออนุมัติ: \(info.pendingCount)"
        pendingItem.isHidden = info.pendingCount == 0
        toggleItem.title = running ? "หยุดเซิร์ฟเวอร์" : "เปิดเซิร์ฟเวอร์"
    }

    private func startServerIfNeeded() {
        if serverRunning() || launching { return }
        guard let bin = lanshareBinary() else { return }
        launching = true
        let proc = Process()
        proc.executableURL = bin
        proc.arguments = []
        proc.standardOutput = FileHandle.nullDevice
        proc.standardError = FileHandle.nullDevice
        proc.terminationHandler = { [weak self] _ in
            DispatchQueue.main.async { self?.server = nil }
        }
        do {
            try proc.run()
            server = proc
        } catch {
            launching = false
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 1) { [weak self] in
            self?.launching = false
            self?.refresh()
        }
    }

    private func stopServer() {
        // หยุดเฉพาะโปรเซสที่แอปนี้เปิดเอง ถ้าผู้ใช้รัน lanshare ในเทอร์มินัลไว้แล้วจะไม่ถูกปิด
        server?.terminate()
        server = nil
    }

    private func serverRunning() -> Bool {
        if let s = server, s.isRunning { return true }
        return false
    }

    private func port() -> Int { 8080 }

    private func bundleDir() -> URL {
        Bundle.main.bundleURL.deletingLastPathComponent()
    }

    private func lanshareBinary() -> URL? {
        let names = ["lanshare"]
        let dirs = [
            Bundle.main.resourceURL,
            Bundle.main.bundleURL.appendingPathComponent("Contents/Resources"),
            bundleDir(),
            URL(fileURLWithPath: NSHomeDirectory()).appendingPathComponent("LANShare/bin"),
        ].compactMap { $0 }
        for dir in dirs {
            for name in names {
                let u = dir.appendingPathComponent(name)
                if FileManager.default.isExecutableFile(atPath: u.path) { return u }
            }
        }
        return nil
    }

    private func shareBinary() -> URL? {
        let u = URL(fileURLWithPath: NSHomeDirectory()).appendingPathComponent("LANShare/bin/share")
        if FileManager.default.isExecutableFile(atPath: u.path) { return u }
        return lanshareBinary()?.deletingLastPathComponent().appendingPathComponent("share")
    }

    private func launchAgentURL() -> URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/LaunchAgents/app.lanshare.menubar.plist")
    }

    private func isLaunchAtLogin() -> Bool {
        FileManager.default.fileExists(atPath: launchAgentURL().path)
    }

    private func setLaunchAtLogin(_ on: Bool) {
        let dest = launchAgentURL()
        if !on {
            try? FileManager.default.removeItem(at: dest)
            return
        }
        let exe = Bundle.main.executableURL?.path ?? ProcessInfo.processInfo.arguments[0]
        let plist = """
        <?xml version="1.0" encoding="UTF-8"?>
        <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
        <plist version="1.0">
        <dict>
            <key>Label</key><string>app.lanshare.menubar</string>
            <key>ProgramArguments</key>
            <array><string>\(exe)</string></array>
            <key>RunAtLoad</key><true/>
            <key>EnvironmentVariables</key>
            <dict>
                <key>LAUNCHED_AT_LOGIN</key>
                <string>1</string>
            </dict>
        </dict>
        </plist>
        """
        try? FileManager.default.createDirectory(at: dest.deletingLastPathComponent(), withIntermediateDirectories: true)
        try? plist.write(to: dest, atomically: true, encoding: .utf8)
    }
}

private struct Info {
    var ok = false
    var roomName = ""
    var pin = ""
    var url = ""
    var iface = ""
    var deviceCount = 0
    var pendingCount = 0
}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.accessory)
app.run()
