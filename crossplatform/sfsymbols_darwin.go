//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>
#include <stdlib.h>

// sf_symbol draws one SF Symbol, as SwiftUI's Image(systemName:) does, at a
// point size and weight, centred in a square of side box points, at scale
// pixels per point. It returns PNG bytes (black on clear, for a mask), or
// NULL for a name macOS doesn't know. The caller frees *out.
static int sf_symbol(const char *name, double size, int weight, double box, double scale, void **out) {
	@autoreleasepool {
		NSImage *image = [NSImage imageWithSystemSymbolName:[NSString stringWithUTF8String:name] accessibilityDescription:nil];
		if (image == nil) return 0;
		NSFontWeight weights[] = { NSFontWeightRegular, NSFontWeightMedium, NSFontWeightSemibold, NSFontWeightBold };
		NSImageSymbolConfiguration *config = [NSImageSymbolConfiguration configurationWithPointSize:size weight:weights[weight] scale:NSImageSymbolScaleMedium];
		image = [image imageWithSymbolConfiguration:config];
		if (image == nil) return 0;
		NSSize glyph = image.size;
		NSInteger side = (NSInteger)ceil(box * scale);
		NSBitmapImageRep *rep = [[NSBitmapImageRep alloc] initWithBitmapDataPlanes:NULL pixelsWide:side pixelsHigh:side bitsPerSample:8 samplesPerPixel:4 hasAlpha:YES isPlanar:NO colorSpaceName:NSDeviceRGBColorSpace bytesPerRow:0 bitsPerPixel:0];
		rep.size = NSMakeSize(box, box);
		[NSGraphicsContext saveGraphicsState];
		[NSGraphicsContext setCurrentContext:[NSGraphicsContext graphicsContextWithBitmapImageRep:rep]];
		NSRect rect = NSMakeRect((box - glyph.width) / 2, (box - glyph.height) / 2, glyph.width, glyph.height);
		[image drawInRect:rect fromRect:NSZeroRect operation:NSCompositingOperationSourceOver fraction:1];
		[NSGraphicsContext restoreGraphicsState];
		NSData *png = [rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
		*out = malloc(png.length);
		memcpy(*out, png.bytes, png.length);
		return (int)png.length;
	}
}
*/
import "C"

import (
	"unsafe"
)

// drawSymbol renders an SF Symbol from macOS itself, so the app shows
// Apple's own symbols on the Mac, as SwiftUI does, without shipping them:
// Apple's licence allows SF Symbols in apps on its platforms, drawn by the
// system, not copied elsewhere. Elsewhere the screens fall back to Lucide.
func drawSymbol(name string, size float64, weight int, box, scale float64) []byte {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	var out unsafe.Pointer
	n := C.sf_symbol(cname, C.double(size), C.int(weight), C.double(box), C.double(scale), &out)
	if n == 0 || out == nil {
		return nil
	}
	defer C.free(out)
	return C.GoBytes(out, n)
}
