//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit -framework Foundation
#import <AppKit/AppKit.h>

extern void agentmanScreenLock(int locked);

// watch_screen_lock hears macOS say the screen locked or unlocked, as the
// Mac app's AgentsStore does through the distributed notification centre.
static void watch_screen_lock(void) {
	NSDistributedNotificationCenter *center = [NSDistributedNotificationCenter defaultCenter];
	[center addObserverForName:@"com.apple.screenIsLocked" object:nil queue:[NSOperationQueue mainQueue]
		usingBlock:^(NSNotification *note) { agentmanScreenLock(1); }];
	[center addObserverForName:@"com.apple.screenIsUnlocked" object:nil queue:[NSOperationQueue mainQueue]
		usingBlock:^(NSNotification *note) { agentmanScreenLock(0); }];
}

static BOOL app_is_active(void) {
	return NSApp.isActive;
}
*/
import "C"

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

var screenLockHandler func(locked bool)

//export agentmanScreenLock
func agentmanScreenLock(locked C.int) {
	if screenLockHandler != nil {
		go screenLockHandler(locked != 0)
	}
}

func watchScreenLock(handler func(locked bool)) {
	screenLockHandler = handler
	application.InvokeAsync(func() { C.watch_screen_lock() })
}

// watchActive tells the shell when the app comes to the front and leaves it.
func watchActive(app *application.App, set func(bool)) {
	app.Event.OnApplicationEvent(events.Mac.ApplicationDidBecomeActive, func(*application.ApplicationEvent) { set(true) })
	app.Event.OnApplicationEvent(events.Mac.ApplicationDidResignActive, func(*application.ApplicationEvent) { set(false) })
	application.InvokeAsync(func() { set(bool(C.app_is_active())) })
}
