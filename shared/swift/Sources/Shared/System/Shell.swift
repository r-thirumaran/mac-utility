import Foundation

/// Runs a child process to completion and captures its output. Foundation only, so it works
/// on Linux too (tests) — the macOS apps use it for `pmset`, `osascript`, `networksetup`…
enum Shell {

    /// Collects one pipe on a background queue so stdout and stderr cannot deadlock each
    /// other when both fill their 64 KiB buffers.
    private final class OutputCollector {
        var data = Data()
    }

    /// Runs `launchPath` with `args`, optionally feeding `input` on stdin.
    /// Throws only when the process could not be started.
    static func run(_ launchPath: String,
                    _ args: [String],
                    input: String? = nil) throws -> (status: Int32, stdout: String, stderr: String) {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: launchPath)
        process.arguments = args

        let stdoutPipe = Pipe()
        let stderrPipe = Pipe()
        process.standardOutput = stdoutPipe
        process.standardError = stderrPipe

        var stdinPipe: Pipe?
        if input != nil {
            let pipe = Pipe()
            process.standardInput = pipe
            stdinPipe = pipe
        }

        try process.run()

        if let stdinPipe, let input {
            stdinPipe.fileHandleForWriting.write(Data(input.utf8))
            try? stdinPipe.fileHandleForWriting.close()
        }

        let outCollector = OutputCollector()
        let errCollector = OutputCollector()
        let group = DispatchGroup()
        group.enter()
        DispatchQueue.global(qos: .utility).async {
            outCollector.data = stdoutPipe.fileHandleForReading.readDataToEndOfFile()
            group.leave()
        }
        group.enter()
        DispatchQueue.global(qos: .utility).async {
            errCollector.data = stderrPipe.fileHandleForReading.readDataToEndOfFile()
            group.leave()
        }
        group.wait()
        process.waitUntilExit()

        return (process.terminationStatus,
                String(decoding: outCollector.data, as: UTF8.self),
                String(decoding: errCollector.data, as: UTF8.self))
    }
}
