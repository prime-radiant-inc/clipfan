import AppKit
import Foundation

@MainActor
final class RemoteUpdateOfferController {
    static let shared = RemoteUpdateOfferController()

    private let userDefaultsKey = "LastRemoteUpdateOfferVersion"

    private init() {}

    func maybeOffer() async {
        let daemon = DaemonClient.shared
        await daemon.refreshPeerVersions()
        let localVersion = daemon.version
        let lastOffered = UserDefaults.standard.string(forKey: userDefaultsKey)
        guard PeerUpdateAdvisor.shouldOffer(localVersion: localVersion,
                                            peers: daemon.peers,
                                            statuses: daemon.peerVersions,
                                            lastOfferedVersion: lastOffered),
              let localVersion else { return }

        let peers = PeerUpdateAdvisor.peersNeedingUpdate(peers: daemon.peers,
                                                         statuses: daemon.peerVersions)
        UserDefaults.standard.set(localVersion, forKey: userDefaultsKey)

        let alert = NSAlert()
        alert.messageText = "Update clipfan peers?"
        alert.informativeText = "\(peers.count) peer\(peers.count == 1 ? "" : "s") appear to be running an older or unverified clipfan. Open Fleet settings to update them over SSH."
        alert.alertStyle = .informational
        alert.addButton(withTitle: "Open Fleet")
        alert.addButton(withTitle: "Later")

        if alert.runModal() == .alertFirstButtonReturn {
            NSApp.activate(ignoringOtherApps: true)
            WindowOpener.shared.openWindow?(id: "settings")
        }
    }
}

