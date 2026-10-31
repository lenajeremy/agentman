//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>
#include <stdlib.h>

// run_alert shows an NSAlert as the Mac app's are (the app's own icon, the
// first button the default, Escape pressing one named Cancel) and answers
// with the index of the button pressed.
static int run_alert(const char *title, const char *message, const char **buttons, int count) {
	@autoreleasepool {
		NSAlert *alert = [[NSAlert alloc] init];
		alert.messageText = [NSString stringWithUTF8String:title];
		alert.informativeText = [NSString stringWithUTF8String:message];
		for (int i = 0; i < count; i++) {
			[alert addButtonWithTitle:[NSString stringWithUTF8String:buttons[i]]];
		}
		[NSApp activateIgnoringOtherApps:YES];
		NSModalResponse response = [alert runModal];
		return (int)(response - NSAlertFirstButtonReturn);
	}
}
*/
import "C"

import (
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// runAlert asks in a modal alert and answers with the index of the button
// pressed. It waits on the main thread's answer, so call it off it.
func runAlert(title, message string, buttons []string) int {
	ctitle, cmessage := C.CString(title), C.CString(message)
	defer C.free(unsafe.Pointer(ctitle))
	defer C.free(unsafe.Pointer(cmessage))
	cbuttons := make([]*C.char, len(buttons))
	for i, b := range buttons {
		cbuttons[i] = C.CString(b)
		defer C.free(unsafe.Pointer(cbuttons[i]))
	}
	array := C.malloc(C.size_t(len(buttons)) * C.size_t(unsafe.Sizeof(uintptr(0))))
	defer C.free(array)
	list := (*[16]*C.char)(array)
	for i, b := range cbuttons {
		list[i] = b
	}
	result := 0
	application.InvokeSync(func() {
		result = int(C.run_alert(ctitle, cmessage, (**C.char)(array), C.int(len(buttons))))
	})
	return result
}
