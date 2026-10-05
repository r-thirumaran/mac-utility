import XCTest
@testable import Shared

final class ShellTests: XCTestCase {

    func testCapturesStdoutAndStatus() throws {
        let result = try Shell.run("/bin/echo", ["hello", "world"])
        XCTAssertEqual(result.status, 0)
        XCTAssertEqual(result.stdout, "hello world\n")
        XCTAssertEqual(result.stderr, "")
    }

    func testFeedsStdin() throws {
        let result = try Shell.run("/bin/cat", [], input: "from stdin")
        XCTAssertEqual(result.status, 0)
        XCTAssertEqual(result.stdout, "from stdin")
    }

    func testNonZeroExitAndStderr() throws {
        let result = try Shell.run("/bin/sh", ["-c", "echo oops 1>&2; exit 3"])
        XCTAssertEqual(result.status, 3)
        XCTAssertEqual(result.stderr, "oops\n")
    }

    func testLargeOutputDoesNotDeadlock() throws {
        let result = try Shell.run("/bin/sh", ["-c", "yes 0123456789 | head -c 300000; yes e | head -c 200000 1>&2"])
        XCTAssertEqual(result.status, 0)
        XCTAssertEqual(result.stdout.utf8.count, 300_000)
        XCTAssertEqual(result.stderr.utf8.count, 200_000)
    }

    func testMissingExecutableThrows() {
        XCTAssertThrowsError(try Shell.run("/nonexistent/binary", []))
    }

    func testLogFallbackDoesNotCrash() {
        Log.info("info line", category: "test")
        Log.error("error line", category: "test")
        Log.debug("debug line")
    }
}
