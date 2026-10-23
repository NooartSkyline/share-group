package app.lanshare.host

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.provider.Settings
import android.widget.Toast

/** Asks Android for shared-storage access so Go can list /sdcard and OTG. */
internal object StorageAccess {
    fun ensure(activity: Activity) {
        if (Build.VERSION.SDK_INT < 30) return
        if (Environment.isExternalStorageManager()) return
        Toast.makeText(activity, R.string.storage_need, Toast.LENGTH_LONG).show()
        val uri = Uri.parse("package:" + activity.packageName)
        try {
            activity.startActivity(Intent(Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION, uri))
        } catch (_: Throwable) {
            activity.startActivity(Intent(Settings.ACTION_MANAGE_ALL_FILES_ACCESS_PERMISSION))
        }
    }
}
