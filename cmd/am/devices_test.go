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

	// The same phone back: the relay names the new connection afresh, and it
	// is the same device. A second phone, though just like it, is another.
	connect := func(token string) *websocket.Conn {
		conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/app", &websocket.DialOptions{
			HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.Write(ctx, websocket.MessageText, frame); err != nil {
			t.Fatal(err)
		}
		return conn
	}
	back := connect(paired.Token)
	waitForDevices(t, ctx, func() bool {
		list := devices.List()
		return len(list) == 1 && list[0].Connected
	}, "the same phone back, still one device")
	back.CloseNow()

	second := deviceRequests(next, client, devices, ts.URL)(ctx, "local:1", protocol.Request{Type: protocol.ReqPairCode})
	body, _ = json.Marshal(map[string]string{"code": second.PairCode.Code, "deviceId": "phone"})
	resp, err = http.Post(ts.URL+"/pair", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	var other struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&other)
	resp.Body.Close()
	secondPhone := connect(other.Token)
	waitForDevices(t, ctx, func() bool { return len(devices.List()) == 2 }, "the second phone")
	secondPhone.CloseNow()
	waitForDevices(t, ctx, func() bool {
		list := devices.List()
		return len(list) == 2 && !list[0].Connected && !list[1].Connected && list[0].ID != list[1].ID
	}, "two phones, both gone")
}

// The daemon's link to the relay drops while the phone stays connected: the
// phone shows as away while the daemon is, and connected again once the
// daemon is back and the phone speaks, under the same connection name.
func TestAPhoneStaysOneDeviceWhileTheDaemonReconnects(t *testing.T) {
	server := relay.NewServer("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "test", slog.New(slog.DiscardHandler), true)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	devices := daemon.NewDevices(filepath.Join(t.TempDir(), "devices.json"))
	next := func(context.Context, string, protocol.Request) protocol.Event { return protocol.Event{} }
	// One run of the daemon's link to the relay, until stopped.
	run := func() (*daemon.Client, context.CancelFunc, chan struct{}) {
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
		client.OnDeviceUnreachable = devices.Lost
		runCtx, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = client.Run(runCtx, namingDevices(next, devices))
		}()
		select {
		case <-online:
		case <-ctx.Done():
			t.Fatal("the daemon never reached the relay")
		}
		return client, stop, done
	}
	client, stop, done := run()

	code := deviceRequests(next, client, devices, ts.URL)(ctx, "local:1", protocol.Request{Type: protocol.ReqPairCode})
	if code.PairCode == nil {
		t.Fatalf("code = %+v", code)
	}
	body, _ := json.Marshal(map[string]string{"code": code.PairCode.Code, "deviceId": "phone"})
	resp, err := http.Post(ts.URL+"/pair", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	var paired struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&paired)
	resp.Body.Close()
	phone, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/app", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + paired.Token}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer phone.CloseNow()
	// The phone reads what the relay sends it, as the app does, so its
	// socket stays healthy while the daemon comes and goes.
	go func() {
		for {
			if _, _, err := phone.Read(ctx); err != nil {
				return
			}
		}
	}()
	speak := func() {
		envelope, _ := protocol.NewEnvelope("r1", protocol.PeerDaemon, protocol.Request{
			Type: protocol.ReqRegisterPush, PushToken: "ExponentPushToken[test]",
			Device: &protocol.DeviceInfo{Name: "Test iPhone", Model: "iPhone 17 Pro", Platform: "ios"},
		})
		frame, _ := json.Marshal(envelope)
		if err := phone.Write(ctx, websocket.MessageText, frame); err != nil {
			t.Fatal(err)
		}
	}
	speak()
	waitForDevices(t, ctx, func() bool {
		list := devices.List()
		return len(list) == 1 && list[0].Connected
	}, "the phone, connected")

	// The daemon's link drops; the phone's stays open.
	stop()
	<-done
	waitForDevices(t, ctx, func() bool {
		list := devices.List()
		return len(list) == 1 && !list[0].Connected
	}, "the phone, away while the daemon is")

	_, stop, done = run()
	defer func() {
		stop()
		<-done
	}()
	// The phone keeps speaking, as the app does: one request that reached
	// the relay before it had the daemon back is dropped there.
	last := time.Time{}
	waitForDevices(t, ctx, func() bool {
		if time.Since(last) > 200*time.Millisecond {
			speak()
			last = time.Now()
		}
		list := devices.List()
		return len(list) == 1 && list[0].Connected
	}, "the phone, connected again under the same name")
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
