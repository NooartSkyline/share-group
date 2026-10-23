import UniformTypeIdentifiers
import UIKit
import WebKit

/// Loads the embedded Go dashboard at 127.0.0.1. Room rules stay in Go.
final class HostViewController: UIViewController, WKNavigationDelegate, WKUIDelegate, WKDownloadDelegate, UIDocumentPickerDelegate {
    private var web: WKWebView!
    private var wait: UILabel!
    private var retries = 0
    private var retryWork: DispatchWorkItem?
    private var openPanel: (([URL]?) -> Void)?

    override func loadView() {
        let root = UIView()
        root.backgroundColor = .systemBackground
        view = root

        let conf = WKWebViewConfiguration()
        conf.defaultWebpagePreferences.allowsContentJavaScript = true
        let web = WKWebView(frame: .zero, configuration: conf)
        web.navigationDelegate = self
        web.uiDelegate = self
        if #available(iOS 16.4, *) {
            web.isInspectable = true
        }
        web.allowsBackForwardNavigationGestures = true
        web.scrollView.contentInsetAdjustmentBehavior = .never
        web.translatesAutoresizingMaskIntoConstraints = false
        root.addSubview(web)
        NSLayoutConstraint.activate([
            web.topAnchor.constraint(equalTo: root.safeAreaLayoutGuide.topAnchor),
            web.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            web.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            web.bottomAnchor.constraint(equalTo: root.bottomAnchor),
        ])
        self.web = web

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

    func resumeHost() {
        startHost()
        loadDashboard()
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

    func webView(
        _ webView: WKWebView,
        runJavaScriptAlertPanelWithMessage message: String,
        initiatedByFrame frame: WKFrameInfo,
        completionHandler: @escaping () -> Void
    ) {
        let ac = UIAlertController(title: nil, message: message, preferredStyle: .alert)
        ac.addAction(UIAlertAction(title: "ตกลง", style: .default) { _ in completionHandler() })
        present(ac, animated: true)
    }

    func webView(
        _ webView: WKWebView,
        runJavaScriptConfirmPanelWithMessage message: String,
        initiatedByFrame frame: WKFrameInfo,
        completionHandler: @escaping (Bool) -> Void
    ) {
        let ac = UIAlertController(title: nil, message: message, preferredStyle: .alert)
        ac.addAction(UIAlertAction(title: "ยกเลิก", style: .cancel) { _ in completionHandler(false) })
        ac.addAction(UIAlertAction(title: "ตกลง", style: .default) { _ in completionHandler(true) })
        present(ac, animated: true)
    }

    func webView(
        _ webView: WKWebView,
        runJavaScriptTextInputPanelWithPrompt prompt: String,
        defaultText: String?,
        initiatedByFrame frame: WKFrameInfo,
        completionHandler: @escaping (String?) -> Void
    ) {
        let ac = UIAlertController(title: nil, message: prompt, preferredStyle: .alert)
        ac.addTextField { $0.text = defaultText }
        ac.addAction(UIAlertAction(title: "ยกเลิก", style: .cancel) { _ in completionHandler(nil) })
        ac.addAction(UIAlertAction(title: "ตกลง", style: .default) { _ in
            completionHandler(ac.textFields?.first?.text)
        })
        present(ac, animated: true)
    }

    @available(iOS 18.4, *)
    func webView(
        _ webView: WKWebView,
        runOpenPanelWith parameters: WKOpenPanelParameters,
        initiatedByFrame frame: WKFrameInfo,
        completionHandler: @escaping ([URL]?) -> Void
    ) {
        openPanel?(nil)
        openPanel = completionHandler
        var types: [UTType] = [.item]
        if parameters.allowsDirectories {
            types.append(.folder)
        }
        let picker = UIDocumentPickerViewController(forOpeningContentTypes: types, asCopy: true)
        picker.allowsMultipleSelection = parameters.allowsMultipleSelection
        picker.delegate = self
        present(picker, animated: true)
    }

    func documentPicker(_ controller: UIDocumentPickerViewController, didPickDocumentsAt urls: [URL]) {
        openPanel?(stagePicked(urls))
        openPanel = nil
    }

    func documentPickerWasCancelled(_ controller: UIDocumentPickerViewController) {
        openPanel?(nil)
        openPanel = nil
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

    func downloadDidFinish(_ download: WKDownload) {}

    func download(_ download: WKDownload, didFailWithError error: Error, resumeData: Data?) {}

    private func stagePicked(_ urls: [URL]) -> [URL] {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("open-panel", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        var out: [URL] = []
        for url in urls {
            let access = url.startAccessingSecurityScopedResource()
            defer {
                if access {
                    url.stopAccessingSecurityScopedResource()
                }
            }
            let dest = uniqueURL(in: dir, name: url.lastPathComponent)
            do {
                if FileManager.default.fileExists(atPath: dest.path) {
                    try FileManager.default.removeItem(at: dest)
                }
                try FileManager.default.copyItem(at: url, to: dest)
                out.append(dest)
            } catch {
                out.append(url)
            }
        }
        return out
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
