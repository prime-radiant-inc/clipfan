import Foundation
import CryptoKit

/// clipfanSign returns the hex-encoded HMAC-SHA256 of body under key, matching
/// the Go daemon's transport.Auth.Sign (hex over the raw request body bytes).
func clipfanSign(body: Data, key: Data) -> String {
    let mac = HMAC<SHA256>.authenticationCode(for: body, using: SymmetricKey(data: key))
    return mac.map { String(format: "%02x", $0) }.joined()
}

/// loadSharedKey reads the base64 `shared_key` from the clipfan config and
/// returns the raw key bytes, or nil if unavailable/unparseable.
func loadSharedKey(configPath: URL? = nil) -> Data? {
    let path = configPath ?? defaultConfigPath()
    guard let data = try? Data(contentsOf: path),
          let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
          let b64 = obj["shared_key"] as? String,
          let raw = Data(base64Encoded: b64) else { return nil }
    return raw
}

/// defaultConfigPath honors XDG_CONFIG_HOME, falling back to ~/.config.
func defaultConfigPath() -> URL {
    if let xdg = ProcessInfo.processInfo.environment["XDG_CONFIG_HOME"], !xdg.isEmpty {
        return URL(fileURLWithPath: xdg).appendingPathComponent("clipfan/config.json")
    }
    return FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent(".config/clipfan/config.json")
}
