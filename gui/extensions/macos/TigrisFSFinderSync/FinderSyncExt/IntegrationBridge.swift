import Foundation

final class IntegrationBridge {
    static let shared = IntegrationBridge()

    private let cacheQueue = DispatchQueue(label: "com.tigrisdata.tigrisfs.findersync.cache")
    private var statusCache: [String: (status: IntegrationPathStatus, expiresAt: Date)] = [:]
    private var mountRootsCache: (roots: [URL], expiresAt: Date)?

    private let statusTTL: TimeInterval = 2
    private let mountsTTL: TimeInterval = 5

    private init() {}

    func mountRoots(completion: @escaping ([URL]) -> Void) {
        cacheQueue.async {
            if let cached = self.mountRootsCache, cached.expiresAt > Date() {
                DispatchQueue.main.async {
                    completion(cached.roots)
                }
                return
            }

            self.performRequest(path: "/v1/mounts", method: "GET", body: nil) { result in
                switch result {
                case .success(let data):
                    do {
                        let response = try JSONDecoder().decode(IntegrationMountsResponse.self, from: data)
                        let roots = response.mounts.map { URL(fileURLWithPath: $0.mountPoint, isDirectory: true) }
                        self.cacheQueue.async {
                            self.mountRootsCache = (roots, Date().addingTimeInterval(self.mountsTTL))
                        }
                        DispatchQueue.main.async {
                            completion(roots)
                        }
                    } catch {
                        DispatchQueue.main.async {
                            completion([])
                        }
                    }
                case .failure:
                    DispatchQueue.main.async {
                        completion([])
                    }
                }
            }
        }
    }

    func pathStatus(path: String, completion: @escaping (IntegrationPathStatus?) -> Void) {
        let cleaned = path.trimmingCharacters(in: .whitespacesAndNewlines)
        if cleaned.isEmpty {
            completion(nil)
            return
        }

        cacheQueue.async {
            if let cached = self.statusCache[cleaned], cached.expiresAt > Date() {
                DispatchQueue.main.async {
                    completion(cached.status)
                }
                return
            }

            let body: Data
            do {
                body = try JSONEncoder().encode(IntegrationPathStatusRequest(path: cleaned))
            } catch {
                DispatchQueue.main.async {
                    completion(nil)
                }
                return
            }

            self.performRequest(path: "/v1/path/status", method: "POST", body: body) { result in
                switch result {
                case .success(let data):
                    do {
                        let response = try JSONDecoder().decode(IntegrationPathStatusResponse.self, from: data)
                        let status = response.status
                        self.cacheQueue.async {
                            self.statusCache[cleaned] = (status, Date().addingTimeInterval(self.statusTTL))
                        }
                        DispatchQueue.main.async {
                            completion(status)
                        }
                    } catch {
                        DispatchQueue.main.async {
                            completion(nil)
                        }
                    }
                case .failure:
                    DispatchQueue.main.async {
                        completion(nil)
                    }
                }
            }
        }
    }

    func sendCommand(action: String, path: String?, recursive: Bool?, completion: @escaping (Bool) -> Void) {
        let request = IntegrationCommandRequest(action: action, path: path, recursive: recursive)
        let body: Data
        do {
            body = try JSONEncoder().encode(request)
        } catch {
            completion(false)
            return
        }

        performRequest(path: "/v1/command", method: "POST", body: body) { result in
            switch result {
            case .success(let data):
                do {
                    let response = try JSONDecoder().decode(IntegrationCommandResponse.self, from: data)
                    completion(response.success)
                } catch {
                    completion(false)
                }
            case .failure:
                completion(false)
            }
        }
    }

    private func performRequest(path: String, method: String, body: Data?, completion: @escaping (Result<Data, Error>) -> Void) {
        guard let state = loadServerState() else {
            completion(.failure(NSError(domain: "TigrisFSFinderSync", code: 1)))
            return
        }
        guard let url = URL(string: "http://\(state.address)\(path)") else {
            completion(.failure(NSError(domain: "TigrisFSFinderSync", code: 2)))
            return
        }

        var request = URLRequest(url: url)
        request.httpMethod = method
        request.timeoutInterval = 2
        request.addValue(state.token, forHTTPHeaderField: "X-TigrisFS-Token")
        if let body {
            request.httpBody = body
            request.addValue("application/json", forHTTPHeaderField: "Content-Type")
        }

        let task = URLSession.shared.dataTask(with: request) { data, response, error in
            if let error {
                completion(.failure(error))
                return
            }
            guard let http = response as? HTTPURLResponse,
                  let data,
                  (200..<300).contains(http.statusCode) else {
                completion(.failure(NSError(domain: "TigrisFSFinderSync", code: 3)))
                return
            }
            completion(.success(data))
        }
        task.resume()
    }

    private func loadServerState() -> IntegrationServerState? {
        let home = FileManager.default.homeDirectoryForCurrentUser
        let stateURL = home
            .appendingPathComponent("Library", isDirectory: true)
            .appendingPathComponent("Application Support", isDirectory: true)
            .appendingPathComponent("TigrisFS", isDirectory: true)
            .appendingPathComponent("integration_server.json")

        guard let data = try? Data(contentsOf: stateURL) else {
            return nil
        }
        return try? JSONDecoder().decode(IntegrationServerState.self, from: data)
    }
}
