//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>

static void hold_keys_repeat(void) {
	[[NSUserDefaults standardUserDefaults] registerDefaults:@{@"ApplePressAndHoldEnabled": @NO}];
}
*/
import "C"

// holdKeysRepeat has a held key repeat, as in a code editor or a terminal,
// instead of offering accents: holding j in the editor's Vim mode moves
// down. Someone who set press-and-hold themselves keeps it (a registered
// default gives way to their own setting), as in AgentmanApp.swift.
func holdKeysRepeat() {
	C.hold_keys_repeat()
}
