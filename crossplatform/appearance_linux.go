//go:build linux

package main

/*
#cgo linux pkg-config: gtk4
#include <gtk/gtk.h>

// GTK's dark preference, which WebKitGTK's prefers-color-scheme follows.
// "system" puts back what it was before the app first changed it.
static int system_dark = -1;

static void set_appearance(int choice) {
	GtkSettings *settings = gtk_settings_get_default();
	if (!settings) return;
	if (system_dark < 0) {
		gboolean dark = FALSE;
		g_object_get(settings, "gtk-application-prefer-dark-theme", &dark, NULL);
		system_dark = dark ? 1 : 0;
	}
	gboolean dark = choice == 2 ? TRUE : choice == 1 ? FALSE : (system_dark == 1);
	g_object_set(settings, "gtk-application-prefer-dark-theme", dark, NULL);
}
*/
import "C"

func setAppearance(choice string) {
	code := 0
	switch choice {
	case "light":
		code = 1
	case "dark":
		code = 2
	}
	C.set_appearance(C.int(code))
}
