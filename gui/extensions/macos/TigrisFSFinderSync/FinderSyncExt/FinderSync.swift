import AppKit
import FinderSync
import Foundation

final class FinderSync: FIFinderSync {
    private let bridge = IntegrationBridge.shared
    private let controller = FIFinderSyncController.default()
    private var mountRefreshTimer: Timer?

    private enum BadgeID {
        static let pinned = "tigris.pinned"
        static let syncing = "tigris.syncing"
        static let partial = "tigris.partial"
        static let cached = "tigris.cached"
        static let remote = "tigris.remote"
    }

    override init() {
        super.init()
        registerBadges()
        refreshObservedFolders()
        startMountRefreshTimer()
    }

    deinit {
        mountRefreshTimer?.invalidate()
    }

    override func beginObservingDirectory(at url: URL) {
        requestBadgeIdentifier(for: url)
    }

    override func requestBadgeIdentifier(for url: URL) {
        bridge.pathStatus(path: url.path) { [weak self] status in
            guard let self else { return }
            let badgeID = self.badgeIdentifier(for: status)
            self.controller.setBadgeIdentifier(badgeID, for: url)
        }
    }

    override func menu(for menuKind: FIMenuKind) -> NSMenu {
        let menu = NSMenu(title: "TigrisFS")

        let pin = NSMenuItem(title: "Pin in TigrisFS Cache", action: #selector(pinSelected), keyEquivalent: "")
        pin.target = self
        menu.addItem(pin)

        let unpin = NSMenuItem(title: "Unpin from TigrisFS Cache", action: #selector(unpinSelected), keyEquivalent: "")
        unpin.target = self
        menu.addItem(unpin)

        menu.addItem(NSMenuItem.separator())

        let unmount = NSMenuItem(title: "Unmount TigrisFS Mount", action: #selector(unmountSelected), keyEquivalent: "")
        unmount.target = self
        menu.addItem(unmount)

        let open = NSMenuItem(title: "Open TigrisFS Dashboard", action: #selector(openDashboard), keyEquivalent: "")
        open.target = self
        menu.addItem(open)

        return menu
    }

    @objc private func pinSelected() {
        performPathCommand(action: "pin")
    }

    @objc private func unpinSelected() {
        performPathCommand(action: "unpin")
    }

    @objc private func unmountSelected() {
        performPathCommand(action: "unmount")
    }

    @objc private func openDashboard() {
        bridge.sendCommand(action: "show", path: nil, recursive: nil) { _ in }
    }

    private func performPathCommand(action: String) {
        let selected = selectedURLs()
        guard !selected.isEmpty else { return }

        for url in selected {
            let recursive = isDirectory(url)
            bridge.sendCommand(action: action, path: url.path, recursive: recursive) { [weak self] _ in
                self?.requestBadgeIdentifier(for: url)
            }
        }
    }

    private func selectedURLs() -> [URL] {
        if let urls = controller.selectedItemURLs(), !urls.isEmpty {
            return urls
        }
        if let targeted = controller.targetedURL() {
            return [targeted]
        }
        return []
    }

    private func isDirectory(_ url: URL) -> Bool {
        let values = try? url.resourceValues(forKeys: [.isDirectoryKey])
        return values?.isDirectory ?? false
    }

    private func startMountRefreshTimer() {
        mountRefreshTimer = Timer.scheduledTimer(withTimeInterval: 5, repeats: true) { [weak self] _ in
            self?.refreshObservedFolders()
        }
    }

    private func refreshObservedFolders() {
        bridge.mountRoots { [weak self] roots in
            self?.controller.directoryURLs = Set(roots)
        }
    }

    private func badgeIdentifier(for status: IntegrationPathStatus?) -> String {
        guard let status, status.mounted, status.exists else {
            return ""
        }
        if status.pinned {
            return BadgeID.pinned
        }
        if status.loading || status.dirty || status.downloadBps > 1 || status.uploadBps > 1 {
            return BadgeID.syncing
        }
        if status.fullyCached {
            return BadgeID.cached
        }
        if status.cachedBytes > 0 {
            return BadgeID.partial
        }
        return BadgeID.remote
    }

    private func registerBadges() {
        registerBadge(id: BadgeID.pinned, label: "Pinned", systemName: "pin.fill")
        registerBadge(id: BadgeID.syncing, label: "Sync", systemName: "arrow.triangle.2.circlepath")
        registerBadge(id: BadgeID.partial, label: "Partial", systemName: "circle.lefthalf.filled")
        registerBadge(id: BadgeID.cached, label: "Cached", systemName: "checkmark.circle.fill")
        registerBadge(id: BadgeID.remote, label: "Remote", systemName: "cloud")
    }

    private func registerBadge(id: String, label: String, systemName: String) {
        let image: NSImage
        if #available(macOS 11.0, *) {
            image = NSImage(systemSymbolName: systemName, accessibilityDescription: label) ?? NSImage(named: NSImage.statusAvailableName)!
        } else {
            image = NSImage(named: NSImage.statusAvailableName) ?? NSImage()
        }
        controller.setBadgeImage(image, label: label, forBadgeIdentifier: id)
    }
}
