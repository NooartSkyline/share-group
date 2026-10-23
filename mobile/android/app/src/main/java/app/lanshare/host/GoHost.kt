package app.lanshare.host

import mobilehost.Mobilehost

/** Thin wrapper around the gomobile package. No room/PIN logic here. */
object GoHost {
    fun start(dataDir: String, port: Int) {
        Mobilehost.start(dataDir, port.toLong())
    }

    fun stop() {
        Mobilehost.stop()
    }

    fun url(): String = Mobilehost.url() ?: ""
}
