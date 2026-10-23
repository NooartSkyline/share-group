package app.lanshare.host

import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.net.Inet4Address
import java.net.NetworkInterface

/** Writes net-ifaces.json for Go. Android blocks net.Interfaces() in the process. */
internal object NetIfaces {
    const val FILE = "net-ifaces.json"

    fun dump(dir: File) {
        val arr = JSONArray()
        val en = try {
            NetworkInterface.getNetworkInterfaces()
        } catch (_: Throwable) {
            null
        } ?: return
        for (ni in en.toList()) {
            val up = try {
                ni.isUp
            } catch (_: Throwable) {
                continue
            }
            if (!up) continue
            val addrs = try {
                ni.interfaceAddresses
            } catch (_: Throwable) {
                emptyList()
            }
            for (ia in addrs) {
                val addr = ia.address ?: continue
                val host = addr.hostAddress?.substringBefore('%') ?: continue
                if (host.isEmpty()) continue
                arr.put(
                    JSONObject()
                        .put("name", ni.name)
                        .put("ip", host)
                        .put("prefix", ia.networkPrefixLength.toInt())
                        .put("loopback", addr.isLoopbackAddress || addr is Inet4Address && addr.isLoopbackAddress),
                )
            }
        }
        try {
            File(dir, FILE).writeText(arr.toString())
        } catch (_: Throwable) {
        }
    }
}
