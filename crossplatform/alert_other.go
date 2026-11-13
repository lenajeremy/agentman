//go:build !darwin

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// runAlert asks in a modal dialog and answers with the index of the button
// pressed; the first is the default, and the one named Cancel is Escape's.
func runAlert(title, message string, buttons []string) int {
	chosen := make(chan int, 1)
	dialog := application.Get().Dialog.Question().SetTitle(title).SetMessage(message)
	if w := application.Get().Window.Current(); w != nil {
		dialog.AttachToWindow(w)
	}
	for i, label := range buttons {
		i := i
		button := dialog.AddButton(label)
		button.OnClick(func() { chosen <- i })
		if i == 0 {
			dialog.SetDefaultButton(button)
		}
		if label == "Cancel" {
			dialog.SetCancelButton(button)
		}
	}
	dialog.Show()
	return <-chosen
}
