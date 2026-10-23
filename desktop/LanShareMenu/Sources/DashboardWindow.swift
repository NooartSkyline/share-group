import AppKit
import WebKit

/// หน้าต่างโปรแกรมที่โชว์แดชบอร์ดในแอป ไม่เปิดเบราว์เซอร์
final class DashboardWindow: NSObject, NSWindowDelegate, WKUIDelegate, WKNavigationDelegate, WKDownloadDelegate {
    var onClose: (() -> Void)?

    private var window: NSWindow?
    private var web: WKWebView?
    private var waitLabel: NSTextField?
    private var port = 8080
    private var retries = 0
    private var retryWork: DispatchWorkItem?

    func show(port: Int) {
        self.port = port
        let win = ensureWindow()
        win.makeKeyAndOrderFront(nil)
        load()
    }

    var isVisible: Bool { window?.isVisible == true }

    func windowWillClose(_ notification: Notification) {
        retryWork?.cancel()
        retryWork = nil
        onClose?()
    }

    func windowShouldClose(_ sender: NSWindow) -> Bool {
        sender.orderOut(nil)
        onClose?()
        return false
    }

    private func ensureWindow() -> NSWindow {
        if let window { return window }
        let web = makeWebView()
        let wait = NSTextField(labelWithString: "กำลังเปิดเซิร์ฟเวอร์…")
        wait.font = .systemFont(ofSize: 14)
        wait.textColor = .secondaryLabelColor
        wait.alignment = .center
        wait.translatesAutoresizingMaskIntoConstraints = false

        let win = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 1280, height: 840),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered,
            defer: false
        )
        win.title = "Share Group"
        win.minSize = NSSize(width: 900, height: 600)
        win.isReleasedWhenClosed = false
        win.tabbingMode = .disallowed
        win.delegate = self
        win.contentView = web
        web.addSubview(wait)
        NSLayoutConstraint.activate([
            wait.centerXAnchor.constraint(equalTo: web.centerXAnchor),
            wait.centerYAnchor.constraint(equalTo: web.centerYAnchor),
        ])
        win.center()
        self.window = win
        self.web = web
        self.waitLabel = wait
        return win
    }

    private func makeWebView() -> WKWebView {
        let conf = WKWebViewConfiguration()
        conf.preferences.isElementFullscreenEnabled = true
        conf.defaultWebpagePreferences.preferredContentMode = .desktop
        let view = WKWebView(frame: .zero, configuration: conf)
        view.uiDelegate = self
        view.navigationDelegate = self
        view.allowsBackForwardNavigationGestures = false
        view.allowsMagnification = true
        return view
    }

    private func dashboardURL() -> URL {
        URL(string: "http://127.0.0.1:\(port)/?app=1")!
    }

    private func load() {
        retries = 0
        waitLabel?.isHidden = false
        web?.load(URLRequest(url: dashboardURL(), cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 4))
    }

    private func scheduleRetry() {
        retries += 1
        guard retries <= 12 else {
            waitLabel?.stringValue = "เปิดเซิร์ฟเวอร์ไม่สำเร็จ"
            waitLabel?.isHidden = false
            return
        }
        retryWork?.cancel()
        let work = DispatchWorkItem { [weak self] in self?.web?.reload() }
        retryWork = work
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.6, execute: work)
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        waitLabel?.isHidden = true
        retries = 0
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        scheduleRetry()
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        scheduleRetry()
    }

    func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration, for navigationAction: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
        if navigationAction.targetFrame == nil, let url = navigationAction.request.url {
            webView.load(URLRequest(url: url))
        }
        return nil
    }

    func webView(_ webView: WKWebView, runOpenPanelWith parameters: WKOpenPanelParameters, initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping ([URL]?) -> Void) {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = parameters.allowsMultipleSelection
        panel.canChooseDirectories = parameters.allowsDirectories
        panel.canChooseFiles = true
        panel.begin { resp in
            completionHandler(resp == .OK ? panel.urls : nil)
        }
    }

    func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction, decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        if navigationAction.shouldPerformDownload {
            decisionHandler(.download)
            return
        }
        decisionHandler(.allow)
    }

    func webView(_ webView: WKWebView, decidePolicyFor navigationResponse: WKNavigationResponse, decisionHandler: @escaping (WKNavigationResponsePolicy) -> Void) {
        if navigationResponse.canShowMIMEType {
            decisionHandler(.allow)
        } else {
            decisionHandler(.download)
        }
    }

    func webView(_ webView: WKWebView, navigationAction: WKNavigationAction, didBecome download: WKDownload) {
        download.delegate = self
    }

    func webView(_ webView: WKWebView, navigationResponse: WKNavigationResponse, didBecome download: WKDownload) {
        download.delegate = self
    }

    func download(_ download: WKDownload, decideDestinationUsing response: URLResponse, suggestedFilename: String, completionHandler: @escaping (URL?) -> Void) {
        let dir = FileManager.default.urls(for: .downloadsDirectory, in: .userDomainMask).first
            ?? FileManager.default.temporaryDirectory
        completionHandler(uniqueURL(in: dir, name: suggestedFilename.isEmpty ? "download" : suggestedFilename))
    }

    private func uniqueURL(in dir: URL, name: String) -> URL {
        let base = (name as NSString).deletingPathExtension
        let ext = (name as NSString).pathExtension
        var n = 0
        while n < 200 {
            let filename: String
            if n == 0 {
                filename = name
            } else if ext.isEmpty {
                filename = "\(base) (\(n))"
            } else {
                filename = "\(base) (\(n)).\(ext)"
            }
            let url = dir.appendingPathComponent(filename)
            if !FileManager.default.fileExists(atPath: url.path) { return url }
            n += 1
        }
        return dir.appendingPathComponent(UUID().uuidString)
    }
}
