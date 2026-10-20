import { Tabs as TabsPrimitive } from "@base-ui/react/tabs";
import type * as React from "react";

import { cn } from "@/lib/utils";

let focusModality: "keyboard" | "pointer" = "keyboard";

if (typeof document !== "undefined") {
  document.addEventListener("keydown", () => {
    focusModality = "keyboard";
  });
  document.addEventListener("pointerdown", () => {
    focusModality = "pointer";
  });
}

function TabsTab({ className, onFocus, ...props }: React.ComponentProps<typeof TabsPrimitive.Tab>) {
  const mergedClassName =
    typeof className === "function"
      ? (state: TabsPrimitive.Tab.State) => cn(className(state))
      : cn(className);

  return (
    <TabsPrimitive.Tab
      {...props}
      data-slot="tabs-tab"
      className={mergedClassName}
      onFocus={(event) => {
        event.currentTarget.toggleAttribute("data-focus-visible", focusModality === "keyboard");
        onFocus?.(event);
      }}
    />
  );
}

function TabsPanel(props: React.ComponentProps<typeof TabsPrimitive.Panel>) {
  return <TabsPrimitive.Panel {...props} data-slot="tabs-panel" />;
}

const Tabs = {
  ...TabsPrimitive,
  Panel: TabsPanel,
  Tab: TabsTab,
};

export { Tabs };
