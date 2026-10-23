import Foundation
#if canImport(Mobilehost)
import Mobilehost
#endif

/// Thin wrapper around the gomobile package. No room/PIN logic here.
enum GoHost {
    static let port: Int = 8080

    static func start(dataDir: String) throws {
        #if canImport(Mobilehost)
        var err: NSError?
        MobilehostStart(dataDir, port, &err)
        if let err {
            throw err
        }
        #else
        throw GoHostError.missingLibrary
        #endif
    }

    static func stop() {
        #if canImport(Mobilehost)
        MobilehostStop(nil)
        #endif
    }

    static func url() -> String {
        #if canImport(Mobilehost)
        return MobilehostURL()
        #else
        return ""
        #endif
    }
}

enum GoHostError: LocalizedError {
    case missingLibrary

    var errorDescription: String? {
        switch self {
        case .missingLibrary:
            return "ยังไม่ได้ผูกไลบรารี Go — รัน sh mobile/ios/bind.sh"
        }
    }
}
