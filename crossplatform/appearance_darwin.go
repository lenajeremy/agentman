//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>

static void set_appearance(int choice) {
	switch (choice) {
	case 1: NSApp.appearance = [NSAppearance appearanceNamed:NSAppearanceNameAqua]; break;
	case 2: NSApp.appearance = [NSAppearance appearanceNamed:NSAppearanceNameDarkAqua]; break;
	default: NSApp.appearance = nil;
	}
}
*/
import "C"

func setAppearance(choice string) {
	C.set_appearance(C.int(appearanceCode(choice)))
}

func appearanceCode(choice string) int {
	switch choice {
	case "light":
		return 1
	case "dark":
		return 2
	}
	return 0
}
