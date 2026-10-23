package app.lanshare.host

import android.app.Activity
import android.content.Intent
import android.provider.Settings
import android.widget.Toast

/** Opens the system Hotspot / tethering screen. Go still labels the NIC. */
internal object HotspotSettings {
    fun open(activity: Activity) {
        val tries = listOf(
            Intent("android.settings.TETHER_SETTINGS"),
            Intent("android.settings.WIFI_TETHER_SETTINGS"),
            Intent(Settings.ACTION_WIRELESS_SETTINGS),
        )
        for (intent in tries) {
            intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            try {
                activity.startActivity(intent)
                return
            } catch (_: Throwable) {
            }
        }
        Toast.makeText(activity, R.string.hotspot_fail, Toast.LENGTH_LONG).show()
    }
}
