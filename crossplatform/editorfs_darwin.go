//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework AppKit
#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>
#include <stdlib.h>

// editor_trash moves a file to the Trash as Finder does, and says where it
// went, or why it couldn't. The caller frees both strings.
static void editor_trash(const char *path, char **where, char **problem) {
	@autoreleasepool {
		NSURL *url = [NSURL fileURLWithPath:[NSString stringWithUTF8String:path]];
		NSURL *result = nil;
		NSError *error = nil;
		if ([[NSFileManager defaultManager] trashItemAtURL:url resultingItemURL:&result error:&error]) {
			*where = result ? strdup(result.path.UTF8String) : strdup("");
		} else {
			*problem = strdup(error.localizedDescription.UTF8String ?: "Couldn't move it to the Trash.");
		}
	}
}

// editor_trash_folder is the user's Trash.
static char *editor_trash_folder(void) {
	@autoreleasepool {
		NSURL *url = [[NSFileManager defaultManager] URLsForDirectory:NSTrashDirectory inDomains:NSUserDomainMask].firstObject;
		return url ? strdup(url.URLByStandardizingPath.path.UTF8String) : strdup("");
	}
}

// editor_copy_files puts files on the clipboard as file URLs, as Finder does.
static void editor_copy_files(const char **paths, int count) {
	@autoreleasepool {
		NSMutableArray *urls = [NSMutableArray arrayWithCapacity:count];
		for (int i = 0; i < count; i++) {
			[urls addObject:[NSURL fileURLWithPath:[NSString stringWithUTF8String:paths[i]]]];
		}
		NSPasteboard *board = [NSPasteboard generalPasteboard];
		[board clearContents];
		[board writeObjects:urls];
	}
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

func trashItem(path string) (string, error) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var where, problem *C.char
	C.editor_trash(cpath, &where, &problem)
	if problem != nil {
		defer C.free(unsafe.Pointer(problem))
		return "", errors.New(C.GoString(problem))
	}
	defer C.free(unsafe.Pointer(where))
	return C.GoString(where), nil
}

func trashFolder() string {
	folder := C.editor_trash_folder()
	defer C.free(unsafe.Pointer(folder))
	return C.GoString(folder)
}

// forgetTrashed has nothing to do on macOS: Finder keeps no notes.
func forgetTrashed(string) {}

func copyFileURLs(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	list := make([]*C.char, len(paths))
	for i, path := range paths {
		list[i] = C.CString(path)
	}
	defer func() {
		for _, p := range list {
			C.free(unsafe.Pointer(p))
		}
	}()
	C.editor_copy_files((**C.char)(unsafe.Pointer(&list[0])), C.int(len(list)))
	return true
}
