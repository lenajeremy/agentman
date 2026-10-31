package main

import (
	"context"
	"errors"

	"github.com/lenajeremy/agentman/internal/daemon"
	"github.com/lenajeremy/agentman/internal/protocol"
)

type handler = func(context.Context, string, protocol.Request) protocol.Event

// deviceRequests answers Agentman for Mac about devices: a code for
// connecting a new one, and the list. Only the Mac's own socket reaches it,
// so a device can't let another in or see the rest.
func deviceRequests(next handler, client *daemon.Client, devices *daemon.Devices, relayURL string) handler {
	return func(ctx context.Context, from string, req protocol.Request) protocol.Event {
		switch req.Type {
		case protocol.ReqPairCode:
			if client == nil {
				return protocol.Event{Type: protocol.EvtError, Error: "Connecting a device needs the relay, and this daemon runs without it."}
			}
			control, err := client.PairCode(ctx)
			if errors.Is(err, daemon.ErrNoRelay) {
				return protocol.Event{Type: protocol.EvtError, Error: "Agentman can't reach the relay right now. Check the internet connection and try again."}
			}
			if err != nil {
				return protocol.Event{Type: protocol.EvtError, Error: "The relay couldn't give a code: " + err.Error() + "."}
			}
			return protocol.Event{Type: protocol.EvtPairCode, PairCode: &protocol.PairCode{
				Code: control.Code, URL: PairingURL(relayURL, control.Token), ExpiresAt: control.ExpiresAt,
			}}
		case protocol.ReqDevices:
			return protocol.Event{Type: protocol.EvtDevices, Devices: devices.List()}
		}
		return next(ctx, from, req)
	}
}

// namingDevices keeps what a device says about itself when it registers for
// notifications, then answers as before.
func namingDevices(next handler, devices *daemon.Devices) handler {
	return func(ctx context.Context, from string, req protocol.Request) protocol.Event {
		if req.Type == protocol.ReqRegisterPush && req.Device != nil {
			devices.Named(from, *req.Device)
		}
		return next(ctx, from, req)
	}
}
