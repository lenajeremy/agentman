import React, { createContext, useCallback, useContext, useEffect, useLayoutEffect, useRef, useState } from "react";
import { ActivityIndicator, Button, Text, View } from "react-native";
import AsyncStorage from "@react-native-async-storage/async-storage";
import type { Credentials } from "./client";
import { draftNamespace } from "./draft-policy";
import { loadDevices, saveDevice, removeDevice } from "./device-list";
import { StoreContext, StoreProvider, useStore, type Store } from "./store";
import { ManagerContext, ManagerProvider, useManager } from "./manager";

export type Host = {
  id: string;
  name?: string;
  /** The computer's reported hostname, cached locally when offline. */
  daemonName?: string;
  /** Optional user-chosen nickname, overriding daemonName when set. */
  alias?: string;
  store: Store;
  manager: ReturnType<typeof useManager>;
};
type Fleet = {
  hosts: Host[];
  active: string;
  filter: string | null;
  rename(id: string, name: string): Promise<void>;
  select(id: string): void;
  setFilter(id: string | null): void;
};
const Context = createContext<Fleet | null>(null);
export function useFleet() { return useContext(Context)!; }
export function hostName(host: Host, index: number) {
  return host.name || `Computer ${index + 1} · ${host.id.slice(-4)}`;
}

function PublishHost({ id, publish }: { id: string; publish(host: Host): void }) {
  const store = useStore();
  const manager = useManager();
  const [cached, setCached] = useState<string | null>(null);
  const liveName = store.daemonInfo?.deviceName;
  useEffect(() => {
    if (!id) return;
    const key = "agentman.deviceName." + id;
    if (liveName) {
      setCached(liveName);
      void AsyncStorage.setItem(key, liveName).catch(() => {});
      return;
    }
    let current = true;
    void AsyncStorage.getItem(key).then(value => { if (current) setCached(value); }).catch(() => {});
    return () => { current = false; };
  }, [id, liveName]);
  const daemonName = liveName || cached || undefined;
  useLayoutEffect(() => {
    publish({ id, name: daemonName, daemonName, store, manager });
  }, [id, daemonName, store, manager, publish]);
  return null;
}

export function FleetProvider({ children }: { children: React.ReactNode }) {
  const [devices, setDevices] = useState<Credentials[] | null>(null);
  const current = useRef<Credentials[]>([]);
  const queue = useRef(Promise.resolve());
  const [active, select] = useState("");
  const [names, setNames] = useState<Record<string, string>>({});
  const rename = useCallback(async (id: string, raw: string) => {
    const name = raw.trim().slice(0, 64);
    const key = "agentman.deviceAlias." + id;
    if (name) await AsyncStorage.setItem(key, name);
    else await AsyncStorage.removeItem(key);
    setNames(old => ({ ...old, [id]: name }));
  }, []);
  const [filter, setFilter] = useState<string | null>(null);
  const [snapshots, setSnapshots] = useState<Record<string, Host>>({});
  const booted = useRef(false);
  const [problem, setProblem] = useState("");
  const [attempt, retry] = useState(0);
  useEffect(() => {
    let live = true;
    loadDevices().then(async rows => {
      const aliases = await Promise.all(rows.map(async row => {
        const id = draftNamespace(row);
        return [id, await AsyncStorage.getItem("agentman.deviceAlias." + id) || ""];
      }));
      if (live) setNames(Object.fromEntries(aliases));
      if (!live) return;
      current.current = rows;
      select(rows.length ? draftNamespace(rows[0]) : "");
      setDevices(rows);
      setProblem("");
    }).catch(() => { if (live) setProblem("Could not read saved devices."); });
    return () => { live = false; };
  }, [attempt]);

  const add = useCallback((credentials: Credentials, name?: string) => {
    const task = queue.current.then(async () => {
      const id = draftNamespace(credentials);
      const next = [...current.current.filter(c => draftNamespace(c) !== id), credentials];
      if (name?.trim()) await rename(id, name);
      await saveDevice(credentials, next);
      current.current = next;
      setDevices(next);
      select(id);
      setFilter(null);
    });
    queue.current = task.catch(() => {});
    return task;
  }, [rename]);
  const remove = useCallback((id: string) => {
    const task = queue.current.then(async () => {
      const next = current.current.filter(c => draftNamespace(c) !== id);
      await removeDevice(id, next);
      await rename(id, "").catch(() => {});
      await AsyncStorage.multiRemove([
        "agentman.deviceName." + id,
        "agentman.dismissed." + id,
        "agentman.notifications.v1." + id,
        "agentman.managerLastRead." + id,
      ]).catch(() => {});
      current.current = next;
      setDevices(next);
      setSnapshots(old => { const copy = { ...old }; delete copy[id]; return copy; });
      select(old => old === id ? (next.length ? draftNamespace(next[0]) : "") : old);
      setFilter(old => old === id ? null : old);
    });
    queue.current = task.catch(() => {});
    return task;
  }, [rename]);
  const publish = useCallback((host: Host) => {
    setSnapshots(old => ({ ...old, [host.id]: host }));
  }, []);
  if (!devices) return <View>{problem ? <><Text>{problem}</Text><Button title="Retry" onPress={() => retry(n => n + 1)} /></> : <ActivityIndicator />}</View>;
  const hosts = devices
    .map(c => snapshots[draftNamespace(c)])
    .filter(Boolean)
    .map(host => {
      const alias = names[host.id] || undefined;
      return { ...host, alias, name: alias ?? host.daemonName };
    });
  // Wait for every paired device to publish on cold start before rendering
  // children so notification routing and multi-device screens see the full
  // fleet immediately, without ever unmounting children when adding a device
  // later.
  const allPublished = devices.length === 0 ? Boolean(snapshots[""]) : hosts.length === devices.length;
  if (allPublished) booted.current = true;
  const selected = snapshots[active] ?? hosts[0] ?? snapshots[""];
  return <>
    {(devices.length ? devices : [null]).map(creds => {
      const id = creds ? draftNamespace(creds) : "";
      return <DeviceConnection key={id} id={id} credentials={creds} add={add} remove={remove} publish={publish} />;
    })}
    {booted.current && selected && <Context.Provider value={{ hosts, active, filter, select, setFilter, rename }}>
      <StoreContext.Provider value={selected.store}><ManagerContext.Provider value={selected.manager}>
        {children}
      </ManagerContext.Provider></StoreContext.Provider>
    </Context.Provider>}
  </>;
}

function DeviceConnection({ id, credentials, add, remove, publish }: {
  id: string; credentials: Credentials | null; add: Store["signIn"];
  remove(id: string): Promise<void>; publish(host: Host): void;
}) {
  const signOut = useCallback(() => remove(id), [id, remove]);
  return <StoreProvider credentials={credentials} signIn={add} signOut={signOut}>
    <ManagerProvider><PublishHost id={id} publish={publish} /></ManagerProvider>
  </StoreProvider>;
}
