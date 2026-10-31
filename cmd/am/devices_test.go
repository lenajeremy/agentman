package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lenajeremy/agentman/internal/daemon"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// The Mac asks for the devices and a code; everything else passes on. A
// device naming itself as it registers for notifications is kept.
func TestDeviceRequests(t *testing.T) {
	devices := daemon.NewDevices(filepath.Join(t.TempDir(), "devices.json"))
	passed := 0
	next := func(context.Context, string, protocol.Request) protocol.Event {
		passed++
		return protocol.Event{Type: protocol.EvtSessions}
	}
	relayed := namingDevices(next, devices)
	relayed(context.Background(), "phone", protocol.Request{Type: protocol.ReqRegisterPush, PushToken: "t",
		Device: &protocol.DeviceInfo{Name: "iPhone", Platform: "ios"}})
	relayed(context.Background(), "tablet", protocol.Request{Type: protocol.ReqRegisterPush, PushToken: "t"})

	local := deviceRequests(next, nil, devices, DefaultRelay)
	listed := local(context.Background(), "local:1", protocol.Request{Type: protocol.ReqDevices})
	if listed.Type != protocol.EvtDevices || len(listed.Devices) != 1 || listed.Devices[0].Name != "iPhone" {
		t.Fatalf("devices = %+v", listed)
	}
	if code := local(context.Background(), "local:1", protocol.Request{Type: protocol.ReqPairCode}); code.Type != protocol.EvtError || code.Error == "" {
		t.Fatalf("no relay: %+v", code)
	}
	local(context.Background(), "local:1", protocol.Request{Type: protocol.ReqListSessions})
	if passed != 3 {
		t.Fatalf("passed on %d requests, want the two registrations and the session list", passed)
	}
}
