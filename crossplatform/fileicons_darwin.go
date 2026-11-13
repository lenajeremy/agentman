//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>
#include <stdlib.h>

// file_icon draws the icon Finder shows for a file, size points square at
// scale pixels per point, as PNG. The caller frees *out.
static int file_icon(const char *path, double size, double scale, void **out) {
	@autoreleasepool {
		NSImage *icon = [[NSWorkspace sharedWorkspace] iconForFile:[NSString stringWithUTF8String:path]];
		if (icon == nil) return 0;
		NSInteger side = (NSInteger)ceil(size * scale);
		NSBitmapImageRep *rep = [[NSBitmapImageRep alloc] initWithBitmapDataPlanes:NULL pixelsWide:side pixelsHigh:side bitsPerSample:8 samplesPerPixel:4 hasAlpha:YES isPlanar:NO colorSpaceName:NSDeviceRGBColorSpace bytesPerRow:0 bitsPerPixel:0];
		rep.size = NSMakeSize(size, size);
		[NSGraphicsContext saveGraphicsState];
		[NSGraphicsContext setCurrentContext:[NSGraphicsContext graphicsContextWithBitmapImageRep:rep]];
		[NSGraphicsContext currentContext].imageInterpolation = NSImageInterpolationHigh;
		[icon drawInRect:NSMakeRect(0, 0, size, size) fromRect:NSZeroRect operation:NSCompositingOperationSourceOver fraction:1];
		[NSGraphicsContext restoreGraphicsState];
		NSData *png = [rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
		*out = malloc(png.length);
		memcpy(*out, png.bytes, png.length);
		return (int)png.length;
	}
}
*/
import "C"

import "unsafe"

func drawFileIcon(path string, size, scale float64) []byte {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var out unsafe.Pointer
	n := C.file_icon(cpath, C.double(size), C.double(scale), &out)
	if n == 0 || out == nil {
		return nil
	}
	defer C.free(out)
	return C.GoBytes(out, n)
}
