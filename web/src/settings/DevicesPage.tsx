// Devices (Views/DevicesSettings.swift): the phones and tablets that reach
// this computer, and pairing another on the page, with a one-time code shown
// as a QR to scan beside the digits to type, a new one as each runs out,
// until a device comes in.

import { useCallback, useEffect, useRef, useState } from "react";
import { Check, CircleCheck, Copy, Link, QrCode, RotateCw, Smartphone, Tablet, TriangleAlert } from "lucide-react";
import { Icon } from "../ui";
import { PushButton, Spinner } from "./controls";
import { Divider, Row, Section, SettingsPage } from "./layout";
import { copyText } from "./native";
import { QRCode } from "./QRCode";
import { connectedIds, deviceDetail, deviceKind, deviceModelBadge, deviceName, grouped, newlyConnected, timeLeft, type Device, type PairCode } from "./devices";
import { loadDevices, pairCode, useSettingsState } from "./state";

/** `pairRequest` changes each time something asks to pair (the palette's
 *  Connect a Device…), which opens the pairing panel. */
export function DevicesPage({ pairRequest = 0 }: { pairRequest?: number }) {
  const { devices } = useSettingsState();
  const [pairing, setPairing] = useState(pairRequest > 0);

  useEffect(() => {
    void loadDevices();
  }, []);
  useEffect(() => {
    if (pairRequest) setPairing(true);
  }, [pairRequest]);

  return (
    <SettingsPage title="Devices">
      <Section>
        <Row title="Pair a device" detail="Make a one-time code another phone or tablet can scan to reach your agents from anywhere." divider={false} className="pair-row">
          {pairing ? (
            <PushButton onClick={() => setPairing(false)}>Cancel</PushButton>
          ) : (
            <PushButton icon={QrCode} sf="qrcode" onClick={() => setPairing(true)}>
              Create code
            </PushButton>
          )}
        </Row>
        {pairing && (
          <>
            <Divider />
            <PairingPanel onDone={() => setPairing(false)} />
          </>
        )}
      </Section>
      <Section title="Your devices">
        {!devices ? (
          <div className="devices-note">
            <Spinner />
            <span>Looking…</span>
          </div>
        ) : devices.length === 0 ? (
          <div className="devices-note">No devices yet. Pair one above.</div>
        ) : (
          devices.map((device, index) => <DeviceRow key={device.id} device={device} divider={index < devices.length - 1} />)
        )}
      </Section>
    </SettingsPage>
  );
}

function DeviceRow({ device, divider }: { device: Device; divider: boolean }) {
  const kind = deviceKind(device);
  const badge = deviceModelBadge(device);
  return (
    <div className="device-row">
      <div className="device-main">
        <span className="device-icon">
          <Icon icon={kind === "ipad" ? Tablet : Smartphone} sf={kind} size={15} />
        </span>
        <div className="device-text">
          <div className="device-title">
            <span className="device-name">{deviceName(device)}</span>
            {badge && <span className="device-model">{badge}</span>}
          </div>
          <div className="device-detail">
            <span className={`device-dot ${device.connected ? "on" : ""}`} />
            <span>{deviceDetail(device)}</span>
          </div>
        </div>
      </div>
      {divider && <div className="device-divider" />}
    </div>
  );
}

function PairingPanel({ onDone }: { onDone: () => void }) {
  const { devices } = useSettingsState();
  const [code, setCode] = useState<PairCode>();
  const [problem, setProblem] = useState<string>();
  const [connected, setConnected] = useState<Device>();
  const [copied, setCopied] = useState<"code" | "link">();
  const [now, setNow] = useState(() => Date.now());
  // The devices connected when pairing began (once they are known): one
  // connecting after is the one just paired.
  const before = useRef<Set<string>>(undefined);
  const live = useRef(true);

  const fetch = useCallback(async () => {
    const reply = await pairCode();
    if (!live.current) return;
    setCode(reply.code);
    setProblem(reply.problem);
    setNow(Date.now());
  }, []);

  useEffect(() => {
    live.current = true;
    void fetch();
    return () => {
      live.current = false;
    };
  }, [fetch]);

  // A new code just before this one runs out, while pairing waits.
  useEffect(() => {
    if (!code || connected) return;
    const timer = window.setTimeout(() => void fetch(), Math.max(0, code.expiresAt - Date.now() - 1000));
    return () => window.clearTimeout(timer);
  }, [code, connected, fetch]);

  // The time left, each second.
  useEffect(() => {
    if (!code || connected) return;
    setCopied(undefined);
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [code, connected]);

  useEffect(() => {
    if (connected || !devices) return;
    if (!before.current) {
      before.current = connectedIds(devices);
      return;
    }
    const device = newlyConnected(devices, before.current);
    if (device) setConnected(device);
  }, [devices, connected]);

  const copy = (text: string, what: "code" | "link") => {
    void copyText(text).then(() => setCopied(what));
  };

  if (connected) {
    return (
      <div className="pairing-panel pairing-line">
        <span className="pairing-ok">
          <Icon icon={CircleCheck} sf="checkmark.circle.fill" size={18} />
        </span>
        <div className="pairing-text">
          <div className="pairing-title">{deviceName(connected)} is connected</div>
          <div className="pairing-caption">Agentman tells it when an agent finishes or needs you.</div>
        </div>
        <span className="row-spacer" />
        <PushButton prominent onClick={onDone}>
          Done
        </PushButton>
      </div>
    );
  }
  if (code) {
    return (
      <div className="pairing-panel pairing-code">
        <div className="qr-card">
          <QRCode text={code.url} label="QR code. Scan it with the Agentman app to pair." />
        </div>
        <div className="pairing-side">
          <div className="pairing-text">
            <div className="pairing-title">Scan with the Agentman app</div>
            <div className="pairing-caption">On the phone, tap Scan QR code, or Enter the code instead and type it.</div>
          </div>
          {/* The buttons beside the code, or both under it when narrow. */}
          <div className="pairing-actions">
            <span className="code-pill mono">{grouped(code.code)}</span>
            <span className="copy-buttons">
              <PushButton icon={copied === "code" ? Check : Copy} sf={copied === "code" ? "checkmark" : "doc.on.doc"} onClick={() => copy(code.code, "code")}>
                {copied === "code" ? "Copied" : "Copy code"}
              </PushButton>
              <PushButton icon={copied === "link" ? Check : Link} sf={copied === "link" ? "checkmark" : "link"} onClick={() => copy(code.url, "link")}>
                {copied === "link" ? "Copied" : "Copy link"}
              </PushButton>
            </span>
          </div>
          <div className="pairing-left tabular">Works for {timeLeft(code.expiresAt, now)} · a new one follows</div>
        </div>
      </div>
    );
  }
  if (problem) {
    return (
      <div className="pairing-panel pairing-line">
        <span className="pairing-problem">
          <Icon icon={TriangleAlert} sf="exclamationmark.triangle" size={13} />
        </span>
        <span className="pairing-caption problem">{problem}</span>
        <span className="row-spacer" />
        <PushButton
          icon={RotateCw}
          sf="arrow.clockwise"
          onClick={() => {
            setProblem(undefined);
            void fetch();
          }}
        >
          Try again
        </PushButton>
      </div>
    );
  }
  return (
    <div className="pairing-panel pairing-line making">
      <Spinner />
      <span className="pairing-caption">Making a code…</span>
    </div>
  );
}
