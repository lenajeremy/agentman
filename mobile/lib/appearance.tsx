import AsyncStorage from "@react-native-async-storage/async-storage";
import {
  createContext,
  ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";
import { Appearance, Platform, useColorScheme } from "react-native";

import {
  APPEARANCE_STORAGE_KEY,
  AppearancePreference,
  DEFAULT_APPEARANCE,
  nativeScheme,
  parseAppearance,
  resolveScheme,
  Scheme,
} from "./appearance-policy";
import { simulatorAppearance, simulatorLook } from "./simulator";
import { Look, Palette, palettes, sitePalettes } from "./theme";

interface Theme {
  color: Palette;
  scheme: Scheme;
  preference: AppearancePreference;
  /** Today's look or the website's, chosen in Settings. */
  look: Look;
  /** False until the stored preference has been read. */
  ready: boolean;
  setPreference(next: AppearancePreference): void;
  setLook(next: Look): void;
}

const LOOK_STORAGE_KEY = "agentman.look.v1";

const ThemeContext = createContext<Theme>({
  color: palettes.light,
  scheme: "light",
  preference: DEFAULT_APPEARANCE,
  look: "classic",
  ready: false,
  setPreference: () => {},
  setLook: () => {},
});

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [preference, setStoredPreference] = useState<AppearancePreference>(DEFAULT_APPEARANCE);
  const [storedLook, setStoredLook] = useState<Look>("classic");
  const [ready, setReady] = useState(false);
  const system = useColorScheme();

  useEffect(() => {
    let active = true;
    Promise.all([AsyncStorage.getItem(APPEARANCE_STORAGE_KEY), AsyncStorage.getItem(LOOK_STORAGE_KEY)])
      .then(([raw, look]) => {
        if (!active) return;
        setStoredPreference(parseAppearance(raw));
        setStoredLook(look === "site" ? "site" : "classic");
      })
      .catch(() => {})
      .finally(() => {
        if (active) setReady(true);
      });
    return () => {
      active = false;
    };
  }, []);

  // Native chrome (alerts, the keyboard, the share sheet) follows the OS
  // scheme, not React state. Telling the OS keeps a light app from raising a
  // dark alert on a phone that is set to dark.
  useEffect(() => {
    if (Platform.OS === "web") return;
    try {
      Appearance.setColorScheme(nativeScheme(preference));
    } catch {
      // Older runtimes without an override simply keep the phone's scheme.
    }
  }, [preference]);

  const setPreference = useCallback((next: AppearancePreference) => {
    setStoredPreference(next);
    void AsyncStorage.setItem(APPEARANCE_STORAGE_KEY, next).catch(() => {});
  }, []);

  const setLook = useCallback((next: Look) => {
    setStoredLook(next);
    void AsyncStorage.setItem(LOOK_STORAGE_KEY, next).catch(() => {});
  }, []);

  const scheme = simulatorAppearance() ?? resolveScheme(preference, system);
  const look = simulatorLook() ?? storedLook;
  const value = useMemo<Theme>(
    () => ({
      color: (look === "site" ? sitePalettes : palettes)[scheme],
      scheme,
      preference,
      look,
      ready,
      setPreference,
      setLook,
    }),
    [scheme, preference, look, ready, setPreference, setLook],
  );

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme(): Theme {
  return useContext(ThemeContext);
}

// One stylesheet per factory per palette. There are only two palettes, so the
// cache stays tiny, and switching themes never rebuilds a sheet it has seen.
const sheets = new WeakMap<object, Map<Palette, unknown>>();

/**
 * Styles that depend on the palette. Factories must be module-level functions
 * so their identity is stable across renders.
 */
export function useStyles<T>(factory: (color: Palette) => T): T {
  const { color } = useTheme();
  let byPalette = sheets.get(factory);
  if (!byPalette) {
    byPalette = new Map();
    sheets.set(factory, byPalette);
  }
  let styles = byPalette.get(color) as T | undefined;
  if (!styles) {
    styles = factory(color);
    byPalette.set(color, styles);
  }
  return styles;
}
