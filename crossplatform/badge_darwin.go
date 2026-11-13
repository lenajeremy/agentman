//go:build darwin

package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/dock"
)

var dockService = dock.New()

func badgeServices() []application.Service {
	return []application.Service{application.NewService(dockService)}
}

func setBadge(label string) error {
	if label == "" {
		return dockService.RemoveBadge()
	}
	return dockService.SetBadge(label)
}
