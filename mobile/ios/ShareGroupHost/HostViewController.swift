import UIKit
import WebKit

/// Loads the embedded Go dashboard at 127.0.0.1. Room rules stay in Go.
final class HostViewController: UIViewController, WKNavigationDelegate, WKDownloadDelegate {
    private var web: WKWebView!
    private var wait: UILabel!
    private var retries = 0
    private var retryWork: DispatchWorkItem?

    override func loadView() {
        let conf = WKWebViewConfiguration()
        conf.defaultWebpagePreferences.allowsContentJavaScript = true
        let web = WKWebView(frame: .zero, configuration: conf)
        web.navigationDelegate = self
        web.allowsBackForwardNavigationGestures = true
        self.web = web
        view = web

        let wait = UILabel()
        wait.text = "กำลังเปิดเซิร์ฟเวอร์…"
        wait.textAlignment = .center
        wait.textColor = .secondaryLabel
        wait.translatesAutoresizingMaskIntoConstraints = false
        web.addSubview(wait)
        NSLayoutConstraint.activate([
            wait.centerXAnchor.constraint(equalTo: web.centerXAnchor),
            wait.centerYAnchor.constraint(equalTo: web.centerYAnchor),
        ])
        self.wait = wait
    }

    override func viewDidAppear(_ animated: Bool) {
        super.viewDidAppear(animated)
        UIApplication.shared.isIdleTimerDisabled = true
        startHost()
        loadDashboard()
    }

    override func viewWillDisappear(_ animated: Bool) {
        super.viewWillDisappear(animated)
        retryWork?.cancel()
        retryWork = nil
    }

    private func startHost() {
        let dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("ShareGroup", isDirectory: true)
        do {
            try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
            try GoHost.start(dataDir: dir.path)
        } catch {
            wait.text = error.localizedDescription
            wait.isHidden = false
        }
    }

    private func dashboardURL() -> URL {
        let raw = GoHost.url()
        if let url = URL(string: raw), !raw.isEmpty {
            return url
        }
        return URL(string: "http://127.0.0.1:\(GoHost.port)/")!
    }

    private func loadDashboard() {
        retries = 0
        wait.isHidden = false
        wait.text = "กำลังเปิดเซิร์ฟเวอร์…"
        web.load(URLRequest(url: dashboardURL(), cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 4))
    }

    private func scheduleRetry() {
        retries += 1
        guard retries <= 20 else {
            wait.text = GoHost.url().isEmpty
                ? (GoHostError.missingLibrary.errorDescription ?? "เปิดเซิร์ฟเวอร์ไม่สำเร็จ")
                : "เปิดเซิร์ฟเวอร์ไม่สำเร็จ"
            wait.isHidden = false
            return
        }
        retryWork?.cancel()
        let work = DispatchWorkItem { [weak self] in
            self?.web.reload()
        }
        retryWork = work
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.4, execute: work)
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        wait.isHidden = true
        retries = 0
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        scheduleRetry()
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        scheduleRetry()
    }

    func webView(
        _ webView: WKWebView,
        decidePolicyFor navigationAction: WKNavigationAction,
        decisionHandler: @escaping (WKNavigationActionPolicy) -> Void
    ) {
        if navigationAction.shouldPerformDownload {
            decisionHandler(.download)
            return
        }
        decisionHandler(.allow)
    }

    func webView(
        _ webView: WKWebView,
        decidePolicyFor navigationResponse: WKNavigationResponse,
        decisionHandler: @escaping (WKNavigationResponsePolicy) -> Void
    ) {
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

    func download(
        _ download: WKDownload,
        decideDestinationUsing response: URLResponse,
        suggestedFilename: String,
        completionHandler: @escaping (URL?) -> Void
    ) {
        let dir = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let name = suggestedFilename.isEmpty ? "download" : suggestedFilename
        completionHandler(uniqueURL(in: dir, name: name))
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
            if !FileManager.default.fileExists(atPath: url.path) {
                return url
            }
            n += 1
        }
        return dir.appendingPathComponent(UUID().uuidString)
    }
}
