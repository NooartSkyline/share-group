package app.lanshare.host

import android.Manifest
import android.app.Activity
import android.app.DownloadManager
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Environment
import android.os.Handler
import android.os.Looper
import android.webkit.CookieManager
import android.webkit.URLUtil
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Toast

class MainActivity : Activity() {
    private lateinit var web: WebView
    private var fileCallback: ValueCallback<Array<Uri>>? = null
    private val handler = Handler(Looper.getMainLooper())
    private var loadAttempts = 0
    private var sharing = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        askRuntimePermissions()
        StorageAccess.ensure(this)
        startHost()
        web = WebView(this)
        setupWeb()
        setContentView(web)
        handleHostAction(intent)
        handleShare(intent)
        scheduleLoad()
    }

    override fun onResume() {
        super.onResume()
        NetIfaces.dump(filesDir)
        if (!sharing) {
            loadAttempts = 0
            loadDashboard()
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        handleHostAction(intent)
        handleShare(intent)
    }

    @Deprecated("Deprecated in Java")
    override fun onBackPressed() {
        if (web.canGoBack()) {
            web.goBack()
        } else {
            @Suppress("DEPRECATION")
            super.onBackPressed()
        }
    }

    @Deprecated("Deprecated in Java")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        when (requestCode) {
            REQ_TREE -> savePickedFolder(resultCode, data)
            REQ_FILES -> {
                val uris = pickedUris(resultCode, data)
                fileCallback?.onReceiveValue(uris)
                fileCallback = null
            }
        }
    }

    override fun onDestroy() {
        handler.removeCallbacksAndMessages(null)
        web.destroy()
        super.onDestroy()
    }

    private fun setupWeb() {
        CookieManager.getInstance().setAcceptCookie(true)
        CookieManager.getInstance().setAcceptThirdPartyCookies(web, true)
        with(web.settings) {
            javaScriptEnabled = true
            domStorageEnabled = true
            cacheMode = WebSettings.LOAD_NO_CACHE
            mixedContentMode = WebSettings.MIXED_CONTENT_COMPATIBILITY_MODE
        }
        web.webViewClient = object : WebViewClient() {
            @Deprecated("Deprecated in Java")
            override fun onReceivedError(
                view: WebView?,
                errorCode: Int,
                description: String?,
                failingUrl: String?,
            ) {
                handler.postDelayed({ loadDashboard() }, 400)
            }
        }
        web.webChromeClient = object : WebChromeClient() {
            override fun onShowFileChooser(
                view: WebView?,
                callback: ValueCallback<Array<Uri>>?,
                params: FileChooserParams?,
            ): Boolean {
                fileCallback?.onReceiveValue(null)
                fileCallback = callback
                val pick = Intent(Intent.ACTION_OPEN_DOCUMENT).apply {
                    addCategory(Intent.CATEGORY_OPENABLE)
                    type = "*/*"
                    putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true)
                }
                return try {
                    @Suppress("DEPRECATION")
                    startActivityForResult(Intent.createChooser(pick, getString(R.string.pick_files)), REQ_FILES)
                    true
                } catch (e: Throwable) {
                    fileCallback = null
                    Toast.makeText(this@MainActivity, e.message ?: getString(R.string.share_fail), Toast.LENGTH_LONG).show()
                    false
                }
            }
        }
        web.setDownloadListener { url, _, contentDisposition, mime, _ ->
            enqueueDownload(url, contentDisposition, mime)
        }
    }

    private fun handleHostAction(intent: Intent?) {
        when (intent?.action) {
            ACTION_ADD_FOLDER -> pickTree()
            ACTION_HOTSPOT -> HotspotSettings.open(this)
        }
    }

    private fun pickTree() {
        val pick = Intent(Intent.ACTION_OPEN_DOCUMENT_TREE).apply {
            addFlags(
                Intent.FLAG_GRANT_READ_URI_PERMISSION or
                    Intent.FLAG_GRANT_WRITE_URI_PERMISSION or
                    Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION,
            )
        }
        try {
            @Suppress("DEPRECATION")
            startActivityForResult(Intent.createChooser(pick, getString(R.string.pick_folder)), REQ_TREE)
        } catch (e: Throwable) {
            Toast.makeText(this, e.message ?: getString(R.string.folder_fail), Toast.LENGTH_LONG).show()
        }
    }

    private fun savePickedFolder(resultCode: Int, data: Intent?) {
        if (resultCode != RESULT_OK || data?.data == null) return
        val uri = data.data!!
        val granted = data.flags and
            (Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION)
        val flags = if (granted == 0) {
            Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION
        } else {
            granted
        }
        try {
            contentResolver.takePersistableUriPermission(uri, flags)
        } catch (_: Throwable) {
        }
        val ok = HostRoots.addTree(filesDir, uri)
        Toast.makeText(
            this,
            if (ok) R.string.folder_ok else R.string.folder_fail,
            Toast.LENGTH_LONG,
        ).show()
    }

    private fun startHost() {
        val i = Intent(this, HostService::class.java)
        try {
            startForegroundService(i)
        } catch (e: Throwable) {
            Toast.makeText(this, e.message ?: getString(R.string.host_missing), Toast.LENGTH_LONG).show()
        }
    }

    private fun scheduleLoad() {
        handler.postDelayed({ loadDashboard() }, 400)
    }

    private fun loadDashboard() {
        if (sharing) return
        var url = try {
            GoHost.url()
        } catch (_: Throwable) {
            ""
        }
        if (url.isEmpty() && loadAttempts >= 4) {
            url = "http://127.0.0.1:${HostService.PORT}/"
        }
        if (url.isEmpty()) {
            loadAttempts++
            if (loadAttempts < 80) {
                handler.postDelayed({ loadDashboard() }, 250)
            } else {
                Toast.makeText(this, R.string.host_missing, Toast.LENGTH_LONG).show()
            }
            return
        }
        web.loadUrl(url)
    }

    private fun handleShare(intent: Intent?) {
        val uris = shareUris(intent) ?: return
        if (uris.isEmpty()) return
        sharing = true
        intent?.action = Intent.ACTION_MAIN
        setIntent(intent)
        Toast.makeText(this, R.string.share_sending, Toast.LENGTH_SHORT).show()
        Thread {
            val err = sendWhenReady(uris)
            handler.post {
                sharing = false
                if (err != null) {
                    Toast.makeText(this, err, Toast.LENGTH_LONG).show()
                } else {
                    Toast.makeText(this, R.string.share_ok, Toast.LENGTH_SHORT).show()
                }
                loadAttempts = 0
                val url = try {
                    GoHost.url()
                } catch (_: Throwable) {
                    ""
                }
                if (url.isNotEmpty()) web.loadUrl(url)
                else loadDashboard()
            }
        }.start()
    }

    private fun sendWhenReady(uris: List<Uri>): String? {
        repeat(40) {
            val url = try {
                GoHost.url()
            } catch (_: Throwable) {
                ""
            }
            if (url.isNotEmpty()) {
                return try {
                    ShareSender.send(contentResolver, uris, HostService.PORT)
                    null
                } catch (e: Throwable) {
                    e.message ?: getString(R.string.share_fail)
                }
            }
            Thread.sleep(250)
        }
        return getString(R.string.host_missing)
    }

    private fun shareUris(intent: Intent?): List<Uri>? {
        if (intent == null) return null
        return when (intent.action) {
            Intent.ACTION_SEND -> {
                val one = if (Build.VERSION.SDK_INT >= 33) {
                    intent.getParcelableExtra(Intent.EXTRA_STREAM, Uri::class.java)
                } else {
                    @Suppress("DEPRECATION")
                    intent.getParcelableExtra(Intent.EXTRA_STREAM)
                }
                if (one != null) listOf(one) else emptyList()
            }
            Intent.ACTION_SEND_MULTIPLE -> {
                val many = if (Build.VERSION.SDK_INT >= 33) {
                    intent.getParcelableArrayListExtra(Intent.EXTRA_STREAM, Uri::class.java)
                } else {
                    @Suppress("DEPRECATION")
                    intent.getParcelableArrayListExtra(Intent.EXTRA_STREAM)
                }
                many ?: emptyList()
            }
            else -> null
        }
    }

    private fun pickedUris(resultCode: Int, data: Intent?): Array<Uri>? {
        if (resultCode != RESULT_OK || data == null) return null
        val out = ArrayList<Uri>()
        data.data?.let { out.add(it) }
        data.clipData?.let { clip ->
            for (i in 0 until clip.itemCount) {
                out.add(clip.getItemAt(i).uri)
            }
        }
        return if (out.isEmpty()) null else out.toTypedArray()
    }

    private fun enqueueDownload(url: String, contentDisposition: String?, mime: String?) {
        val name = URLUtil.guessFileName(url, contentDisposition, mime)
        try {
            val req = DownloadManager.Request(Uri.parse(url)).apply {
                setMimeType(mime)
                addRequestHeader("Cookie", CookieManager.getInstance().getCookie(url) ?: "")
                setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
                setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, name)
                setTitle(name)
            }
            (getSystemService(DOWNLOAD_SERVICE) as DownloadManager).enqueue(req)
            Toast.makeText(this, getString(R.string.download_started, name), Toast.LENGTH_SHORT).show()
        } catch (e: Throwable) {
            Toast.makeText(this, e.message ?: getString(R.string.share_fail), Toast.LENGTH_LONG).show()
        }
    }

    private fun askRuntimePermissions() {
        val need = ArrayList<String>()
        if (Build.VERSION.SDK_INT >= 33) {
            need.add(Manifest.permission.POST_NOTIFICATIONS)
            need.add(Manifest.permission.READ_MEDIA_IMAGES)
            need.add(Manifest.permission.READ_MEDIA_VIDEO)
            need.add(Manifest.permission.READ_MEDIA_AUDIO)
        } else if (Build.VERSION.SDK_INT >= 23) {
            need.add(Manifest.permission.READ_EXTERNAL_STORAGE)
        }
        if (Build.VERSION.SDK_INT < 29) {
            @Suppress("DEPRECATION")
            need.add(Manifest.permission.WRITE_EXTERNAL_STORAGE)
        }
        if (need.isNotEmpty()) {
            requestPermissions(need.toTypedArray(), 0)
        }
    }

    companion object {
        const val ACTION_ADD_FOLDER = "app.lanshare.host.ADD_FOLDER"
        const val ACTION_HOTSPOT = "app.lanshare.host.HOTSPOT"
        private const val REQ_FILES = 1001
        private const val REQ_TREE = 1002
    }
}
