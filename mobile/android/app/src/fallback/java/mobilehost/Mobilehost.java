package mobilehost;

/** Used only when lanshare.aar is missing. gomobile bind replaces this class. */
public abstract class Mobilehost {
    private Mobilehost() {}

    public static void start(String dataDir, long listenPort) throws Exception {
        throw new UnsatisfiedLinkError("ยังไม่ได้ผูกไลบรารี Go — รัน sh mobile/android/bind.sh");
    }

    public static void stop() throws Exception {}

    public static String url() {
        return "";
    }

    public static long port() {
        return 0;
    }
}
