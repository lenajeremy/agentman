import XCTest
@testable import Agentman

/// A file's name is cut before its extension, never through it.
final class FileNameTests: XCTestCase {
    func testTheExtensionIsKept() {
        XCTAssertEqual(FileNameText.split("ApplePayButton.tsx").stem, "ApplePayButton")
        XCTAssertEqual(FileNameText.split("ApplePayButton.tsx").ext, ".tsx")
        XCTAssertEqual(FileNameText.split("checkout.test.ts").ext, ".ts")
        XCTAssertEqual(FileNameText.split(".env").ext, "", "a dotfile is all name")
        XCTAssertEqual(FileNameText.split("Makefile").ext, "")
        XCTAssertEqual(FileNameText.split("notes.averyveryverylongsuffix").ext, "", "not an extension")
    }
}
