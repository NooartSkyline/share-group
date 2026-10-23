package app.lanshare.host

import android.net.Uri
import android.os.Environment
import android.provider.DocumentsContract
import org.json.JSONArray
import org.json.JSONObject
import java.io.File

/** Persists SAF trees as host-roots.json for Go HostBrowser. No room rules. */
internal object HostRoots {
    private const val FILE = "host-roots.json"

    fun addTree(dataDir: File, uri: Uri): Boolean {
        val path = treePath(uri) ?: return false
        val dir = File(path)
        if (!dir.isDirectory) return false
        val id = "saf-" + Integer.toHexString(path.hashCode() and 0x7fffffff)
        val label = dir.name.ifEmpty { path }
        val rows = load(dataDir).filter { it.path != path && it.id != id }.toMutableList()
        rows.add(Root(id, label, path))
        save(dataDir, rows)
        return true
    }

    fun treePath(uri: Uri): String? {
        val docId = DocumentsContract.getTreeDocumentId(uri) ?: return null
        val parts = docId.split(":", limit = 2)
        val volume = parts[0]
        val rel = parts.getOrNull(1)?.trim('/') ?: ""
        val base = if (volume.equals("primary", ignoreCase = true)) {
            Environment.getExternalStorageDirectory().absolutePath
        } else {
            "/storage/$volume"
        }
        return if (rel.isEmpty()) base else "$base/$rel"
    }

    private data class Root(val id: String, val label: String, val path: String)

    private fun load(dataDir: File): List<Root> {
        val f = File(dataDir, FILE)
        if (!f.isFile) return emptyList()
        return try {
            val arr = JSONArray(f.readText())
            (0 until arr.length()).mapNotNull { i ->
                val o = arr.optJSONObject(i) ?: return@mapNotNull null
                val id = o.optString("id")
                val path = o.optString("path")
                if (id.isEmpty() || path.isEmpty()) null
                else Root(id, o.optString("label"), path)
            }
        } catch (_: Throwable) {
            emptyList()
        }
    }

    private fun save(dataDir: File, rows: List<Root>) {
        val arr = JSONArray()
        for (r in rows) {
            arr.put(
                JSONObject()
                    .put("id", r.id)
                    .put("label", r.label)
                    .put("path", r.path),
            )
        }
        File(dataDir, FILE).writeText(arr.toString())
    }
}
