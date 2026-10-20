import { createContext, type ReactNode, useContext, useEffect, useMemo, useState } from "react";

// The design system's two visual registers (docs/design-system.md):
//   viewer -> Cinema Dark (the immersive default)
//   manage -> Workbench (dark by default; light by per-Profile preference)
// Register/theme are visible STATE, never a hidden mode — they drive the
// data-* attributes on <html> that tokens.css keys off.

export type Register = "viewer" | "manage";
export type Theme = "dark" | "light";

interface RegisterValue {
  register: Register;
  theme: Theme;
  setRegister: (register: Register) => void;
  setTheme: (theme: Theme) => void;
}

const RegisterContext = createContext<RegisterValue | null>(null);

export function RegisterProvider({ children }: { children: ReactNode }) {
  const [register, setRegister] = useState<Register>("viewer");
  const [theme, setTheme] = useState<Theme>("dark");

  useEffect(() => {
    const root = document.documentElement;
    root.dataset.register = register;
    // Light is only meaningful in the Manage/Workbench register.
    if (register === "manage" && theme === "light") {
      root.dataset.theme = "light";
    } else {
      delete root.dataset.theme;
    }
  }, [register, theme]);

  const value = useMemo<RegisterValue>(
    () => ({ register, theme, setRegister, setTheme }),
    [register, theme],
  );

  return <RegisterContext.Provider value={value}>{children}</RegisterContext.Provider>;
}

export function useRegister(): RegisterValue {
  const ctx = useContext(RegisterContext);
  if (!ctx) throw new Error("useRegister must be used within <RegisterProvider>");
  return ctx;
}
