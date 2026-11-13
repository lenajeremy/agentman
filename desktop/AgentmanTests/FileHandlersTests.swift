import AppKit
import XCTest
@testable import Agentman

/// Agentman as what opens a kind of file, from Settings. Only read here:
/// changing it changes the Mac's own setting, and macOS asks first.
@MainActor
final class FileHandlersTests: XCTestCase {
    func testTheKindsAndTheirTypes() {
        XCTAssertEqual(FileHandlers.Kind.markdown.type.identifier, "net.daringfireball.markdown")
        XCTAssertEqual(FileHandlers.Kind.text.type, .plainText)
    }

    // No to macOS's question is no problem to show.
    func testANoIsNotAProblem() {
        let wrapped = NSError(domain: NSCocoaErrorDomain, code: 256,
                              userInfo: [NSUnderlyingErrorKey: NSError(domain: NSOSStatusErrorDomain, code: -128)])
        XCTAssertTrue(FileHandlers.cancelled(wrapped), "as macOS 26 says it")
        XCTAssertTrue(FileHandlers.cancelled(NSError(domain: NSCocoaErrorDomain, code: NSUserCancelledError)))
        XCTAssertFalse(FileHandlers.cancelled(NSError(domain: NSCocoaErrorDomain, code: 256)))
        XCTAssertFalse(FileHandlers.cancelled(NSError(domain: NSOSStatusErrorDomain, code: -50)))
    }
}
