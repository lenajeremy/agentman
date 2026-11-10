//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>

// settings_material puts the sidebar's material behind the Settings page, as
// the Mac's SidebarMaterial (Design/Theme.swift): translucent while the
// window is active, flat grey while it isn't. The page is clear over it.
static void settings_material(void *ptr) {
	NSWindow *window = (__bridge NSWindow *)ptr;
	NSView *content = window.contentView;
	for (NSView *view in content.subviews) {
		if ([view.identifier isEqualToString:@"agentman-sidebar"]) return;
	}
	NSVisualEffectView *material = [[NSVisualEffectView alloc] initWithFrame:NSMakeRect(0, 0, 200, content.bounds.size.height)];
	material.identifier = @"agentman-sidebar";
	material.material = NSVisualEffectMaterialSidebar;
	material.blendingMode = NSVisualEffectBlendingModeBehindWindow;
	material.state = NSVisualEffectStateFollowsWindowActiveState;
	material.autoresizingMask = NSViewHeightSizable | NSViewMaxXMargin;
	[content addSubview:material positioned:NSWindowBelow relativeTo:nil];
}
*/
import "C"

import "unsafe"

func settingsMaterial(window unsafe.Pointer) {
	if window != nil {
		C.settings_material(window)
	}
}
