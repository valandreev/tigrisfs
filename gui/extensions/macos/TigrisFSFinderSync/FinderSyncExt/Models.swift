import Foundation

struct IntegrationServerState: Decodable {
    let address: String
    let token: String
}

struct IntegrationMountStatus: Decodable {
    let mountPoint: String

    enum CodingKeys: String, CodingKey {
        case mountPoint = "mount_point"
    }
}

struct IntegrationMountsResponse: Decodable {
    let success: Bool
    let mounts: [IntegrationMountStatus]
    let message: String?
}

struct IntegrationPathStatus: Decodable {
    let path: String
    let mounted: Bool
    let exists: Bool
    let pinned: Bool
    let fullyCached: Bool
    let loading: Bool
    let dirty: Bool
    let cachedBytes: UInt64
    let downloadBps: Double
    let uploadBps: Double

    enum CodingKeys: String, CodingKey {
        case path
        case mounted
        case exists
        case pinned
        case fullyCached = "fully_cached"
        case loading
        case dirty
        case cachedBytes = "cached_bytes"
        case downloadBps = "download_bps"
        case uploadBps = "upload_bps"
    }
}

struct IntegrationPathStatusResponse: Decodable {
    let success: Bool
    let status: IntegrationPathStatus
    let message: String?
}

struct IntegrationCommandRequest: Encodable {
    let action: String
    let path: String?
    let recursive: Bool?
}

struct IntegrationCommandResponse: Decodable {
    let success: Bool
    let message: String?
}

struct IntegrationPathStatusRequest: Encodable {
    let path: String
}
