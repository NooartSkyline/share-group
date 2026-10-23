package app.lanshare.host

import android.content.ContentResolver
import android.net.Uri
import android.provider.OpenableColumns
import java.io.InputStream
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder

/** Streams shared files to the Go shortcuts API on loopback. No PIN or room logic. */
internal object ShareSender {
    fun send(resolver: ContentResolver, uris: List<Uri>, port: Int) {
        for (uri in uris) {
            val name = displayName(resolver, uri)
            val body = resolver.openInputStream(uri)
                ?: throw RuntimeException("เปิดไฟล์ไม่ได้")
            body.use { post(port, name, it) }
        }
    }

    private fun post(port: Int, name: String, body: InputStream) {
        val enc = URLEncoder.encode(name, Charsets.UTF_8.name()).replace("+", "%20")
        val conn = URL("http://127.0.0.1:$port/api/shortcuts/files?name=$enc")
            .openConnection() as HttpURLConnection
        try {
            conn.requestMethod = "POST"
            conn.doOutput = true
            conn.connectTimeout = 8_000
            conn.readTimeout = 0
            conn.instanceFollowRedirects = false
            conn.setRequestProperty("Content-Type", "application/octet-stream")
            body.copyTo(conn.outputStream)
            conn.outputStream.close()
            val code = conn.responseCode
            if (code != HttpURLConnection.HTTP_CREATED) {
                throw RuntimeException("ส่งไฟล์ไม่สำเร็จ (HTTP $code)")
            }
        } finally {
            conn.disconnect()
        }
    }

    private fun displayName(resolver: ContentResolver, uri: Uri): String {
        resolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { c ->
            if (c.moveToFirst()) {
                val i = c.getColumnIndex(OpenableColumns.DISPLAY_NAME)
                if (i >= 0) {
                    val n = c.getString(i)
                    if (!n.isNullOrBlank()) return n
                }
            }
        }
        return uri.lastPathSegment?.substringAfterLast('/') ?: "file"
    }
}
