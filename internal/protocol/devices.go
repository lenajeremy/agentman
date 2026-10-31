package protocol

// Devices: the phones and tablets that reach this Mac through the relay, and
// a code to connect another. Both are the Mac app's alone: a device can't
// list the others or let a new one in.

const (
	// ReqPairCode asks for a code a new device can connect with, answered
	// with an EvtPairCode.
	ReqPairCode RequestType = "pair_code"
	EvtPairCode EventType   = "pair_code"
	// ReqDevices asks for the devices, answered with an EvtDevices, which is
	// also sent whenever one connects, leaves or names itself.
	ReqDevices RequestType = "devices"
	EvtDevices EventType   = "devices"
)

// PairCode is a code for connecting a new device, good until ExpiresAt.
type PairCode struct {
	// Code is the ten digits to type on the device.
	Code string `json:"code"`
	// URL is what the QR code holds: agentman://pair?token=…, which opens
	// the phone app straight into pairing.
	URL string `json:"url"`
	// ExpiresAt is in milliseconds since 1970.
	ExpiresAt int64 `json:"expiresAt"`
}

// DeviceInfo is what a device says about itself.
type DeviceInfo struct {
	// Name is the device's own name, where it can tell: "Jeremiah's iPhone".
	Name string `json:"name,omitempty"`
	// Model is the kind of device: "iPhone 17 Pro", "iPad".
	Model string `json:"model,omitempty"`
	// Platform is "ios" or "android".
	Platform string `json:"platform,omitempty"`
	// OS is the system's version: "26.1".
	OS string `json:"os,omitempty"`
}

// Device is one device that has reached this Mac.
type Device struct {
	ID string `json:"id"`
	DeviceInfo
	// FirstSeen and LastSeen are in milliseconds since 1970.
	FirstSeen int64 `json:"firstSeen"`
	LastSeen  int64 `json:"lastSeen"`
	// Connected is true while it is connected through the relay.
	Connected bool `json:"connected,omitempty"`
}
