package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lenajeremy/agentman/internal/daemon"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/relay"
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
	relayed(context.Background(), "ipad", protocol.Request{Type: protocol.ReqListSessions,
		Device: &protocol.DeviceInfo{Model: "iPad", Platform: "ios"}})

	local := deviceRequests(next, nil, devices, DefaultRelay)
	listed := local(context.Background(), "local:1", protocol.Request{Type: protocol.ReqDevices})
	if listed.Type != protocol.EvtDevices || len(listed.Devices) != 2 {
		t.Fatalf("devices = %+v", listed)
	}
	if code := local(context.Background(), "local:1", protocol.Request{Type: protocol.ReqPairCode}); code.Type != protocol.EvtError || code.Error == "" {
		t.Fatalf("no relay: %+v", code)
	}
	local(context.Background(), "local:1", protocol.Request{Type: protocol.ReqListSessions})
	names := map[string]string{}
	for _, device := range listed.Devices {
		names[device.ID] = device.Name + device.Model
	}
	if names["phone"] != "iPhone" || names["ipad"] != "iPad" {
		t.Fatalf("named on registering and on connecting: %v", names)
	}
	if passed != 4 {
		t.Fatalf("passed on %d requests, want all four through", passed)
	}
}

// Connecting a device, end to end against a real relay: the Mac asks for a
// code, a phone redeems it and connects, names itself as it registers for
// notifications, and leaves; the list follows each step.
func TestConnectingADeviceEndToEnd(t *testing.T) {
	server := relay.NewServer("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "test", slog.New(slog.DiscardHandler), true)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	devices := daemon.NewDevices(filepath.Join(t.TempDir(), "devices.json"))
	client := daemon.NewClient(ts.URL, "daemon-token")
	online := make(chan struct{}, 1)
	client.OnStatus = func(connected bool, _ string) {
		if connected {
			select {
			case online <- struct{}{}:
			default:
			}
		}
	}
	client.OnDeviceSeen = devices.Seen
	client.OnDeviceDisconnected = devices.Left
	next := func(context.Context, string, protocol.Request) protocol.Event { return protocol.Event{} }
	go func() { _ = client.Run(ctx, namingDevices(next, devices)) }()
	select {
	case <-online:
	case <-ctx.Done():
		t.Fatal("the daemon never reached the relay")
	}

	asked := deviceRequests(next, client, devices, ts.URL)(ctx, "local:1", protocol.Request{Type: protocol.ReqPairCode})
	if asked.Type != protocol.EvtPairCode || asked.PairCode == nil || len(asked.PairCode.Code) != 10 {
		t.Fatalf("code = %+v", asked)
	}
	if !strings.HasPrefix(asked.PairCode.URL, "agentman://pair?relay=") || !strings.Contains(asked.PairCode.URL, "token=") {
		t.Fatalf("a relay of its own is spelled out in the QR: %s", asked.PairCode.URL)
	}

	body, _ := json.Marshal(map[string]string{"code": asked.PairCode.Code, "deviceId": "phone-1"})
	resp, err := http.Post(ts.URL+"/pair", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	var paired struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&paired)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || paired.Token == "" {
		t.Fatalf("redeem: HTTP %d", resp.StatusCode)
	}

	app, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/app", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + paired.Token}},
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, _ := protocol.NewEnvelope("r1", protocol.PeerDaemon, protocol.Request{
		Type: protocol.ReqRegisterPush, PushToken: "ExponentPushToken[test]",
		Device: &protocol.DeviceInfo{Name: "Test iPhone", Model: "iPhone 17 Pro", Platform: "ios"},
	})
	frame, _ := json.Marshal(envelope)
	if err := app.Write(ctx, websocket.MessageText, frame); err != nil {
		t.Fatal(err)
	}
	waitForDevices(t, ctx, func() bool {
		list := devices.List()
		return len(list) == 1 && list[0].Connected && list[0].Name == "Test iPhone"
	}, "the phone, connected and named")

	app.CloseNow()
	waitForDevices(t, ctx, func() bool {
		list := devices.List()
		return len(list) == 1 && !list[0].Connected
	}, "the phone, gone")
}

func waitForDevices(t *testing.T, ctx context.Context, done func() bool, what string) {
	t.Helper()
	for !done() {
		select {
		case <-ctx.Done():
			t.Fatalf("never saw %s", what)
		case <-time.After(20 * time.Millisecond):
		}
	}
}
