import AppKit

/// Registers every bridge method of `docs/ARCHITECTURE.md` §5 on the `NativeBridge`, with the
/// method names, parameters, result shapes and error codes of the Go backend
/// (`apps/vitals/win/bridge.go`), so the shared web UI cannot tell the platforms apart.
///
/// Fast handlers answer on the main actor. Anything that spawns a process or shows the
/// administrator prompt runs on `powerQueue` (power settings, schedule) or `processQueue`
/// (the process table) and completes asynchronously; those closures only capture the
/// objects they need, never the bridge itself.
@MainActor
final class VitalsBridge {

    private static let empty: [String: Any] = [:]
    private static let openableSchemes: Set<String> = ["http", "https", "mailto", "x-apple.systempreferences"]

    private let bridge: NativeBridge
    private let sampler: SystemSampler
    private let power = PowerSettings()
    private let schedule = PowerSchedule()
    private let keepAwake = KeepAwake()
    private let processes: ProcessSampler
    private let icons = AppIconCache()
    private let powerQueue = DispatchQueue(label: "vitals.power", qos: .userInitiated)
    private let processQueue = DispatchQueue(label: "vitals.processes", qos: .userInitiated)
    private let version: String

    init(bridge: NativeBridge, sampler: SystemSampler) {
        self.bridge = bridge
        self.sampler = sampler
        processes = ProcessSampler(coreCount: sampler.cpu.coreCount)
        version = Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "0.0"
    }

    // MARK: - Registration

    func registerAll() {
        registerApp()
        registerVitals()
        registerPower()
    }

    private func registerApp() {
        bridge.register("app.info") { [weak self] _ in
            guard let self else { throw BridgeError.internalError("bridge is gone") }
            return self.info()
        }

        bridge.register("app.openURL") { params in
            let raw = (params["url"] as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            guard let url = URL(string: raw), let scheme = url.scheme?.lowercased() else {
                throw BridgeError.invalidParams("url must be absolute")
            }
            guard VitalsBridge.openableSchemes.contains(scheme) else {
                throw BridgeError.invalidParams("unsupported url scheme \"\(scheme)\"")
            }
            NSWorkspace.shared.open(url)
            return VitalsBridge.empty
        }

        bridge.register("app.quit") { _ in
            Log.info("quit requested from the UI")
            // Let the reply reach the page first.
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) {
                MainActor.assumeIsolated { NSApp.terminate(nil) }
            }
            return VitalsBridge.empty
        }

        bridge.register("app.openSettingsPane") { [weak self] params in
            guard let self else { throw BridgeError.internalError("bridge is gone") }
            self.openSettingsPane(params["pane"] as? String ?? "")
            return VitalsBridge.empty
        }
    }

    private func registerVitals() {
        bridge.register("vitals.subscribe") { [weak self] params in
            guard let self else { throw BridgeError.internalError("bridge is gone") }
            let requested = (params["intervalMs"] as? NSNumber)?.intValue ?? 0
            let state = self.sampler.subscribe(intervalMs: requested)
            Log.debug("subscribe: \(state.subscribers) subscribers, interval \(state.intervalMs) ms", category: "sampler")
            let result: [String: Any] = ["intervalMs": state.intervalMs, "subscribers": state.subscribers]
            return result
        }

        bridge.register("vitals.unsubscribe") { [weak self] _ in
            guard let self else { throw BridgeError.internalError("bridge is gone") }
            let left = self.sampler.unsubscribe()
            Log.debug("unsubscribe: \(left) subscribers left", category: "sampler")
            let result: [String: Any] = ["subscribers": left]
            return result
        }

        bridge.registerAsync("vitals.processes") { [weak self] params, done in
            guard let self else {
                done(.failure(BridgeError.internalError("bridge is gone")))
                return
            }
            let sortBy = params["sortBy"] as? String ?? ""
            let limit = (params["limit"] as? NSNumber)?.intValue ?? 0
            let withIcons = params["icons"] as? Bool ?? false
            let apps = self.icons.identities(includeIcons: withIcons)
            let sampler = self.processes
            self.processQueue.async {
                let listing = sampler.list(apps: apps)
                let rows = ProcessSampler.limit(ProcessSampler.sort(listing.rows, by: sortBy), to: limit)
                done(.success(rows.map(VitalsBridge.json)))
            }
        }

        bridge.register("vitals.killProcess") { params in
            let pid = (params["pid"] as? NSNumber)?.intValue ?? 0
            let force = params["force"] as? Bool ?? false
            guard pid > 0, pid <= Int(Int32.max) else { throw BridgeError.invalidParams("pid must be positive") }
            guard kill(pid_t(pid), force ? SIGKILL : SIGTERM) == 0 else {
                let code = errno
                Log.error("kill \(pid): \(String(cString: strerror(code)))", category: "processes")
                switch code {
                case EPERM:
                    throw BridgeError.notAuthorized("cannot end process \(pid): access denied")
                case ESRCH:
                    throw BridgeError.invalidParams("no process with pid \(pid)")
                default:
                    throw BridgeError.internalError("kill \(pid): \(String(cString: strerror(code)))")
                }
            }
            Log.info("ended process \(pid) (force=\(force))", category: "processes")
            let result: [String: Any] = ["pid": pid]
            return result
        }
    }

    private func registerPower() {
        bridge.registerAsync("power.get") { [weak self] _, done in
            guard let self else {
                done(.failure(BridgeError.internalError("bridge is gone")))
                return
            }
            let power = self.power
            let awake = self.keepAwake.state
            self.powerQueue.async {
                do {
                    var model = try jsonObject(try power.read()) as? [String: Any] ?? [:]
                    model["keepAwake"] = awake
                    done(.success(model))
                } catch {
                    Log.error("power.get: \(error)", category: "power")
                    done(.failure(error))
                }
            }
        }

        bridge.registerAsync("power.apply") { [weak self] params, done in
            guard let self else {
                done(.failure(BridgeError.internalError("bridge is gone")))
                return
            }
            guard let changes = params["changes"] as? [String: Any], !changes.isEmpty else {
                done(.failure(BridgeError.invalidParams("changes must not be empty")))
                return
            }
            let power = self.power
            self.powerQueue.async {
                do {
                    let result = try power.apply(changes)
                    Log.info("power.apply: applied \(result.applied), failed \(result.failed.count)", category: "power")
                    done(.success(try jsonObject(result)))
                } catch {
                    Log.error("power.apply: \(error)", category: "power")
                    done(.failure(error))
                }
            }
        }

        bridge.register("power.relaunchElevated") { _ in
            throw BridgeError.notSupported("elevation is only available on Windows; macOS asks for the administrator password when needed")
        }

        bridge.registerAsync("power.action") { params, done in
            let action = params["action"] as? String ?? ""
            guard PowerActions.all.contains(action) else {
                done(.failure(BridgeError.invalidParams("action must be one of \(PowerActions.all.joined(separator: ", "))")))
                return
            }
            Log.info("power.action \(action)", category: "power")
            PowerActions.perform(action) { result in
                switch result {
                case .success:
                    let reply: [String: Any] = ["action": action]
                    done(.success(reply))
                case .failure(let error):
                    Log.error("power.action \(action): \(error)", category: "power")
                    done(.failure(error))
                }
            }
        }

        bridge.registerAsync("power.schedule.get") { [weak self] _, done in
            guard let self else {
                done(.failure(BridgeError.internalError("bridge is gone")))
                return
            }
            let schedule = self.schedule
            self.powerQueue.async {
                do {
                    let reply: [String: Any] = ["items": try jsonObject(try schedule.items())]
                    done(.success(reply))
                } catch {
                    done(.failure(error))
                }
            }
        }

        bridge.registerAsync("power.schedule.set") { [weak self] params, done in
            guard let self else {
                done(.failure(BridgeError.internalError("bridge is gone")))
                return
            }
            let items = VitalsBridge.scheduleItems(from: params)
            let schedule = self.schedule
            self.powerQueue.async {
                do {
                    let saved = try schedule.set(items)
                    Log.info("power.schedule.set: \(saved.count) items", category: "power")
                    let reply: [String: Any] = ["items": try jsonObject(saved)]
                    done(.success(reply))
                } catch {
                    Log.error("power.schedule.set: \(error)", category: "power")
                    done(.failure(error))
                }
            }
        }

        bridge.register("power.keepAwake") { [weak self] params in
            guard let self else { throw BridgeError.internalError("bridge is gone") }
            let enabled = params["enabled"] as? Bool ?? false
            let mode = params["mode"] as? String ?? "system"
            try self.keepAwake.set(enabled: enabled, mode: mode)
            let state = self.keepAwake.state
            Log.info("keep awake: enabled=\(self.keepAwake.enabled) mode=\(self.keepAwake.mode)", category: "power")
            self.bridge.emit("power.keepAwake", state)
            return state
        }

        bridge.register("power.keepAwake.get") { [weak self] _ in
            guard let self else { throw BridgeError.internalError("bridge is gone") }
            return self.keepAwake.state
        }
    }

    // MARK: - app.*

    private func info() -> [String: Any] {
        let osVersion = ProcessInfo.processInfo.operatingSystemVersion
        let machine: [String: Any] = [
            "hostname": Host.current().localizedName ?? ProcessInfo.processInfo.hostName,
            "cpuModel": sampler.cpu.brand,
            "cores": sampler.cpu.coreCount,
            "memTotal": sampler.memory.total,
            "os": "macOS \(osVersion.majorVersion).\(osVersion.minorVersion).\(osVersion.patchVersion)",
            "uptimeSec": Int(ProcessInfo.processInfo.systemUptime),
        ]
        return [
            "name": "Vitals",
            "version": version,
            "platform": "macos",
            "machine": machine,
            "permissions": VitalsBridge.empty,
            "elevated": geteuid() == 0,
            "fakePower": false,
        ]
    }

    /// System Settings deep links: the Battery pane on portables, Energy Saver on desktops,
    /// Login Items for "startup" and the privacy panes the power actions may need.
    private func openSettingsPane(_ pane: String) {
        switch pane.lowercased() {
        case "startup", "login", "loginitems":
            LaunchAtLogin.openLoginItemsSettings()
        case "accessibility":
            Permissions.openSettingsPane(.accessibility)
        case "automation":
            open("x-apple.systempreferences:com.apple.preference.security?Privacy_Automation")
        default:
            open("x-apple.systempreferences:" + (power.hasBattery ? "com.apple.preference.battery" : "com.apple.preference.energysaver"))
        }
    }

    private func open(_ link: String) {
        guard let url = URL(string: link) else { return }
        NSWorkspace.shared.open(url)
    }

    // MARK: - Shapes

    /// Runs on the process queue, hence `nonisolated`.
    nonisolated private static func json(_ row: ProcessSampler.Row) -> [String: Any] {
        var object: [String: Any] = [
            "pid": row.pid,
            "name": row.name,
            "cpu": row.cpu,
            "memBytes": row.memBytes,
        ]
        if let icon = row.icon { object["icon"] = icon }
        if let user = row.user { object["user"] = user }
        return object
    }

    /// `{items: [{id, type, days, time, enabled}]}`, tolerant of missing fields.
    private static func scheduleItems(from params: [String: Any]) -> [PowerSchedule.Item] {
        let raw = params["items"] as? [[String: Any]] ?? []
        return raw.map { item in
            PowerSchedule.Item(id: item["id"] as? String ?? "",
                               type: item["type"] as? String ?? "",
                               days: item["days"] as? [String] ?? [],
                               time: item["time"] as? String ?? "",
                               enabled: item["enabled"] as? Bool ?? true)
        }
    }
}
