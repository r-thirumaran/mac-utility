#if os(macOS)
import Foundation

enum AdminShellError: Error, Equatable {
    /// The user dismissed the password prompt.
    case cancelled
    /// The script ran (or osascript failed) with a non-zero status.
    case failed(status: Int32, stderr: String)
}

/// Runs a shell script as root through the system authorisation dialog:
/// `osascript -e 'do shell script "…" with administrator privileges with prompt "…"'`.
///
/// This is how Vitals applies `pmset` changes without a privileged helper. The script is
/// embedded in an AppleScript string literal, so `\` and `"` are escaped; AppleScript passes
/// it to `/bin/sh`, so normal shell quoting applies inside. `prompt` is shown above the
/// password field ("Vitals wants to change power settings.").
enum AdminShell {

    /// Runs `script` with `/bin/sh` as root and returns its stdout.
    static func run(script: String, prompt: String) throws -> String {
        let source = "do shell script \"" + escape(script) + "\""
            + " with administrator privileges"
            + " with prompt \"" + escape(prompt) + "\""
        let result = try Shell.run("/usr/bin/osascript", ["-e", source])
        guard result.status == 0 else {
            if result.stderr.contains("User canceled") || result.stderr.contains("(-128)") {
                throw AdminShellError.cancelled
            }
            throw AdminShellError.failed(status: result.status, stderr: result.stderr)
        }
        return result.stdout
    }

    /// Runs several commands in one authorisation (joined with ` ; `).
    static func run(commands: [String], prompt: String) throws -> String {
        try run(script: commands.joined(separator: " ; "), prompt: prompt)
    }

    /// Escapes text for an AppleScript double-quoted string literal.
    static func escape(_ text: String) -> String {
        var out = String()
        out.reserveCapacity(text.count + 8)
        for character in text {
            switch character {
            case "\\": out += "\\\\"
            case "\"": out += "\\\""
            default:   out.append(character)
            }
        }
        return out
    }
}
#endif
