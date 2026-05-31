import Foundation
import CryptoKit

func clipfanSign(method: String, requestURI: String, timestamp: String, nonce: String, body: Data, key: Data) -> String {
    var canonical = Data()
    canonical.append(Data(method.utf8))
    canonical.append(Data("\n".utf8))
    canonical.append(Data(requestURI.utf8))
    canonical.append(Data("\n".utf8))
    canonical.append(Data(timestamp.utf8))
    canonical.append(Data("\n".utf8))
    canonical.append(Data(nonce.utf8))
    canonical.append(Data("\n".utf8))
    canonical.append(body)

    let mac = HMAC<SHA256>.authenticationCode(for: canonical, using: SymmetricKey(data: key))
    return mac.map { String(format: "%02x", $0) }.joined()
}

func clipfanSignatureHeaders(method: String, requestURI: String, body: Data, key: Data) -> [String: String] {
    let timestamp = String(Int(Date().timeIntervalSince1970))
    let nonce = UUID().uuidString
    return [
        "X-Clipfan-Ts": timestamp,
        "X-Clipfan-Nonce": nonce,
        "X-Clipfan-Sig": clipfanSign(method: method, requestURI: requestURI, timestamp: timestamp, nonce: nonce, body: body, key: key),
    ]
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
