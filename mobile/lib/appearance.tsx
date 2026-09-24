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
import { Palette, palettes } from "./theme";

interface Theme {
  color: Palette;
  scheme: Scheme;
  preference: AppearancePreference;
  /** False until the stored preference has been read. */
  ready: boolean;
  setPreference(next: AppearancePreference): void;
}

const ThemeContext = createContext<Theme>({
  color: palettes.light,
  scheme: "light",
  preference: DEFAULT_APPEARANCE,
  ready: false,
  setPreference: () => {},
});

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [preference, setStoredPreference] = useState<AppearancePreference>(DEFAULT_APPEARANCE);
  const [ready, setReady] = useState(false);
  const system = useColorScheme();

  useEffect(() => {
    let active = true;
    AsyncStorage.getItem(APPEARANCE_STORAGE_KEY)
      .then((raw) => {
        if (active) setStoredPreference(parseAppearance(raw));
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

  const scheme = resolveScheme(preference, system);
  const value = useMemo<Theme>(
    () => ({ color: palettes[scheme], scheme, preference, ready, setPreference }),
    [scheme, preference, ready, setPreference],
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
