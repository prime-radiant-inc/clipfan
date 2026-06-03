import Foundation
import CryptoKit

enum ClipfanAuthenticationError: Error {
    case missingHTTPResponse
    case badStatus(Int)
    case missingRequestNonce
    case missingResponseSignature
    case badResponseSignature
}

let clipfanRequestAuthVersion = "clipfan-v1/request-hmac"
private let clipfanHKDFSalt = "clipfan-v1/hkdf-salt"

func clipfanHKDFSHA256(rawKey: Data, info: String, outputByteCount: Int = 32) -> Data {
    let key = HKDF<SHA256>.deriveKey(
        inputKeyMaterial: SymmetricKey(data: rawKey),
        salt: Data(clipfanHKDFSalt.utf8),
        info: Data(info.utf8),
        outputByteCount: outputByteCount
    )
    return key.withUnsafeBytes { Data($0) }
}

func clipfanRequestHMACKey(sharedKey: Data) -> Data {
    clipfanHKDFSHA256(rawKey: sharedKey, info: clipfanRequestAuthVersion)
}

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

func clipfanVersionedSign(method: String, requestURI: String, timestamp: String, nonce: String, body: Data, sharedKey: Data) -> String {
    var canonical = Data()
    canonical.append(Data(method.utf8))
    canonical.append(Data("\n".utf8))
    canonical.append(Data(requestURI.utf8))
    canonical.append(Data("\n".utf8))
    canonical.append(Data(timestamp.utf8))
    canonical.append(Data("\n".utf8))
    canonical.append(Data(nonce.utf8))
    canonical.append(Data("\n".utf8))
    canonical.append(Data("auth_version=\(clipfanRequestAuthVersion)".utf8))
    canonical.append(Data("\n".utf8))
    canonical.append(body)

    let requestKey = clipfanRequestHMACKey(sharedKey: sharedKey)
    let mac = HMAC<SHA256>.authenticationCode(for: canonical, using: SymmetricKey(data: requestKey))
    return mac.map { String(format: "%02x", $0) }.joined()
}

func clipfanResponseSignature(requestNonce: String, body: Data, key: Data) -> String {
    var canonical = Data()
    canonical.append(Data("response\n".utf8))
    canonical.append(Data(requestNonce.utf8))
    canonical.append(Data("\n".utf8))
    canonical.append(body)

    let mac = HMAC<SHA256>.authenticationCode(for: canonical, using: SymmetricKey(data: key))
    return mac.map { String(format: "%02x", $0) }.joined()
}

func clipfanVersionedResponseSignature(requestNonce: String, body: Data, sharedKey: Data) -> String {
    var canonical = Data()
    canonical.append(Data("response\n".utf8))
    canonical.append(Data(requestNonce.utf8))
    canonical.append(Data("\n".utf8))
    canonical.append(Data("auth_version=\(clipfanRequestAuthVersion)".utf8))
    canonical.append(Data("\n".utf8))
    canonical.append(body)

    let requestKey = clipfanRequestHMACKey(sharedKey: sharedKey)
    let mac = HMAC<SHA256>.authenticationCode(for: canonical, using: SymmetricKey(data: requestKey))
    return mac.map { String(format: "%02x", $0) }.joined()
}

func authenticatedClipfanData(_ data: Data, response: URLResponse, requestNonce: String, key: Data) throws -> Data {
    guard let http = response as? HTTPURLResponse else {
        throw ClipfanAuthenticationError.missingHTTPResponse
    }
    guard (200..<300).contains(http.statusCode) else {
        throw ClipfanAuthenticationError.badStatus(http.statusCode)
    }
    guard let sig = http.value(forHTTPHeaderField: "X-Clipfan-Response-Sig") else {
        throw ClipfanAuthenticationError.missingResponseSignature
    }
    guard clipfanVerifyResponseSignatureHeader(sig,
                                               authVersion: http.value(forHTTPHeaderField: "X-Clipfan-Auth-Version"),
                                               requestNonce: requestNonce,
                                               body: data,
                                               key: key) else {
        throw ClipfanAuthenticationError.badResponseSignature
    }
    return data
}

func clipfanVerifyResponseSignatureHeader(_ sig: String, authVersion: String?, requestNonce: String, body: Data, key: Data) -> Bool {
    if authVersion == clipfanRequestAuthVersion {
        return clipfanVerifyVersionedResponseSignature(sig, requestNonce: requestNonce, body: body, sharedKey: key)
    }
    return clipfanVerifyResponseSignature(sig, requestNonce: requestNonce, body: body, key: key)
}

func clipfanVerifyResponseSignature(_ sig: String, requestNonce: String, body: Data, key: Data) -> Bool {
    guard let got = hexData(sig) else { return false }
    guard let expect = hexData(clipfanResponseSignature(requestNonce: requestNonce, body: body, key: key)) else { return false }
    return constantTimeEqual(got, expect)
}

func clipfanVerifyVersionedResponseSignature(_ sig: String, requestNonce: String, body: Data, sharedKey: Data) -> Bool {
    guard let got = hexData(sig) else { return false }
    guard let expect = hexData(clipfanVersionedResponseSignature(requestNonce: requestNonce, body: body, sharedKey: sharedKey)) else { return false }
    return constantTimeEqual(got, expect)
}

private func hexData(_ hex: String) -> Data? {
    guard hex.count % 2 == 0 else { return nil }
    var out = Data()
    out.reserveCapacity(hex.count / 2)
    var i = hex.startIndex
    while i < hex.endIndex {
        let j = hex.index(i, offsetBy: 2)
        guard let byte = UInt8(hex[i..<j], radix: 16) else { return nil }
        out.append(byte)
        i = j
    }
    return out
}

private func constantTimeEqual(_ a: Data, _ b: Data) -> Bool {
    guard a.count == b.count else { return false }
    var diff: UInt8 = 0
    for i in 0..<a.count {
        diff |= a[i] ^ b[i]
    }
    return diff == 0
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

func clipfanVersionedSignatureHeaders(method: String, requestURI: String, body: Data, sharedKey: Data) -> [String: String] {
    let timestamp = String(Int(Date().timeIntervalSince1970))
    let nonce = UUID().uuidString
    return [
        "X-Clipfan-Auth-Version": clipfanRequestAuthVersion,
        "X-Clipfan-Ts": timestamp,
        "X-Clipfan-Nonce": nonce,
        "X-Clipfan-Sig": clipfanVersionedSign(method: method, requestURI: requestURI, timestamp: timestamp, nonce: nonce, body: body, sharedKey: sharedKey),
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
