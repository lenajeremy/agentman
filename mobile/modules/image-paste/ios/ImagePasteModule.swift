import ExpoModulesCore
import ObjectiveC
import UIKit

/// Lets the system Paste command put an image into a text input.
///
/// React Native's multiline TextInput is a UITextView, and a plain-text
/// UITextView only offers Paste when the clipboard holds text. Its onPaste
/// never reaches JavaScript either. So a screenshot on the clipboard could be
/// attached from the composer's + menu, but holding the text box and choosing
/// Paste, the gesture everyone already knows, did nothing.
///
/// This teaches React Native's text view two things, while JavaScript is
/// listening: to offer Paste for an image, and, when Paste is chosen with only
/// images on the clipboard, to hand them to JavaScript instead of inserting
/// nothing. Text pastes as it always has. The listener is only held while the
/// composer is focused, so every other text box in the app is unchanged.
public final class ImagePasteModule: Module {
  public func definition() -> ModuleDefinition {
    Name("ImagePaste")

    Events("onPasteImages")

    OnCreate {
      DispatchQueue.main.async { PasteHook.install() }
    }

    OnStartObserving {
      PasteHook.receiver = self
    }

    OnStopObserving {
      if PasteHook.receiver === self { PasteHook.receiver = nil }
    }
  }

  fileprivate func deliver(_ images: [[String: Any]]) {
    sendEvent("onPasteImages", ["images": images])
  }
}

private enum PasteHook {
  /// Whoever is listening. Nil means Paste behaves exactly as React Native's.
  static weak var receiver: ImagePasteModule?
  private static var installed = false

  /// The composer takes at most four images; more is work thrown away.
  private static let limit = 4

  static func install() {
    guard !installed, let textView = NSClassFromString("RCTUITextView") else { return }
    installed = true
    replacePaste(in: textView)
    replaceCanPerformAction(in: textView)
  }

  /// Images and no text: a screenshot, a photo copied from Photos. When the
  /// clipboard holds both, as rich text copied from a web page can, the text
  /// is what someone writing a message meant to paste.
  private static var clipboardIsImage: Bool {
    let board = UIPasteboard.general
    return receiver != nil && board.hasImages && !board.hasStrings
  }

  private static func replacePaste(in textView: AnyClass) {
    let selector = #selector(UIResponderStandardEditActions.paste(_:))
    guard let method = class_getInstanceMethod(textView, selector) else { return }
    typealias Paste = @convention(c) (AnyObject, Selector, Any?) -> Void
    let original = unsafeBitCast(method_getImplementation(method), to: Paste.self)
    let replacement: @convention(block) (AnyObject, Any?) -> Void = { view, sender in
      if !takeImages() { original(view, selector, sender) }
    }
    method_setImplementation(method, imp_implementationWithBlock(replacement))
  }

  private static func replaceCanPerformAction(in textView: AnyClass) {
    let selector = #selector(UIResponder.canPerformAction(_:withSender:))
    guard let method = class_getInstanceMethod(textView, selector) else { return }
    typealias CanPerform = @convention(c) (AnyObject, Selector, Selector, Any?) -> Bool
    let original = unsafeBitCast(method_getImplementation(method), to: CanPerform.self)
    let paste = #selector(UIResponderStandardEditActions.paste(_:))
    let replacement: @convention(block) (AnyObject, Selector, Any?) -> Bool = { view, action, sender in
      if original(view, selector, action, sender) { return true }
      guard action == paste, let field = view as? UITextView, field.isEditable else { return false }
      return clipboardIsImage
    }
    method_setImplementation(method, imp_implementationWithBlock(replacement))
  }

  /// Hand the clipboard's images to JavaScript, or return false to let the
  /// text view paste as usual.
  ///
  /// The images are read here, inside the Paste the person chose, because a
  /// read from anywhere else asks them to allow it first. Encoding them is
  /// slower and needs no permission, so it happens off the main thread.
  private static func takeImages() -> Bool {
    guard clipboardIsImage, let receiver else { return false }
    let images = Array((UIPasteboard.general.images ?? []).prefix(limit))
    guard !images.isEmpty else { return false }
    DispatchQueue.global(qos: .userInitiated).async {
      let saved = images.compactMap(save)
      guard !saved.isEmpty else { return }
      receiver.deliver(saved)
    }
    return true
  }

  /// Written as a JPEG at full quality: the app downscales and re-encodes
  /// every attachment the same way, wherever it came from.
  private static func save(_ image: UIImage) -> [String: Any]? {
    guard let data = image.jpegData(compressionQuality: 1) else { return nil }
    let url = FileManager.default.temporaryDirectory
      .appendingPathComponent("pasted-\(UUID().uuidString).jpg")
    do {
      try data.write(to: url, options: .atomic)
    } catch {
      return nil
    }
    return [
      "uri": url.absoluteString,
      "width": image.size.width * image.scale,
      "height": image.size.height * image.scale,
    ]
  }
}
