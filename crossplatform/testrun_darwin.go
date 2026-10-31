//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit -framework WebKit
#import <AppKit/AppKit.h>
#import <WebKit/WebKit.h>
#import <objc/message.h>

static WKWebView *findWebView(NSView *view) {
	if ([view isKindOfClass:[WKWebView class]]) return (WKWebView *)view;
	for (NSView *sub in view.subviews) {
		WKWebView *found = findWebView(sub);
		if (found) return found;
	}
	return nil;
}

// test_hide puts the window on screen where nobody can see or click it:
// transparent, letting the mouse through, behind every other window. WebKit
// is told to keep drawing anyway (it stops for windows it thinks are
// covered), so the picture is of the page as it really looks.
static void test_hide(void *ptr) {
	NSWindow *window = (__bridge NSWindow *)ptr;
	window.alphaValue = 0;
	window.ignoresMouseEvents = YES;
	window.hasShadow = NO;
	WKWebView *web = findWebView(window.contentView);
	SEL occlusion = NSSelectorFromString(@"_setWindowOcclusionDetectionEnabled:");
	if (web && [web respondsToSelector:occlusion]) {
		((void (*)(id, SEL, BOOL))objc_msgSend)(web, occlusion, NO);
	}
	[window orderWindow:NSWindowBelow relativeTo:0];
}

// test_snapshot saves the page as WebKit draws it, at the screen's scale,
// then quits.
static void test_snapshot(void *ptr, const char *path) {
	NSWindow *window = (__bridge NSWindow *)ptr;
	NSString *out = [NSString stringWithUTF8String:path];
	WKWebView *web = findWebView(window.contentView);
	if (!web) {
		fprintf(stderr, "agentman: no web view to take a picture of\n");
		exit(3);
	}
	WKSnapshotConfiguration *config = [WKSnapshotConfiguration new];
	config.afterScreenUpdates = YES;
	[web takeSnapshotWithConfiguration:config completionHandler:^(NSImage *image, NSError *error) {
		if (!image) {
			fprintf(stderr, "agentman: test snapshot failed: %s\n", error.localizedDescription.UTF8String);
			exit(4);
		}
		CGImageRef cg = [image CGImageForProposedRect:NULL context:nil hints:nil];
		NSBitmapImageRep *rep = [[NSBitmapImageRep alloc] initWithCGImage:cg];
		NSData *png = [rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
		if (![png writeToFile:out atomically:YES]) {
			fprintf(stderr, "agentman: could not write %s\n", path);
			exit(4);
		}
		exit(0);
	}];
}
*/
import "C"

import "unsafe"

func testHide(window unsafe.Pointer) {
	if window != nil {
		C.test_hide(window)
	}
}

func testSnapshot(window unsafe.Pointer, path string) {
	if window == nil {
		return
	}
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	C.test_snapshot(window, cpath)
}
