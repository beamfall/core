export interface RenderFocusAnchor {
  navigation: string;
  section: string;
  item: string;
}

let rememberedAnchor: RenderFocusAnchor | null = null;
let pendingRestore: RenderFocusAnchor | null = null;

export function rememberRenderFocus(anchor: RenderFocusAnchor) {
  rememberedAnchor = anchor;
}

// Reveal controls own the transition that reloads the manifest. The control or
// PIN field has focus by then, so preserve the last card focus recorded by the
// renderer rather than the transition control itself.
export function prepareRenderFocusRestore() {
  pendingRestore = rememberedAnchor;
}

export function applyRenderFocus(root: HTMLElement, navigation: string): HTMLElement | null {
  let target: HTMLElement | null = null;
  if (pendingRestore?.navigation === navigation) {
    target = root.querySelector<HTMLElement>(
      `[data-focus-item][data-focus-navigation="${cssEscape(pendingRestore.navigation)}"]` +
        `[data-focus-section="${cssEscape(pendingRestore.section)}"]` +
        `[data-focus-id="${cssEscape(pendingRestore.item)}"]`,
    );
  }

  // A removed anchor (or first render) falls back to the contract's guaranteed
  // first focusable item of the first non-empty Section.
  target ??= root.querySelector<HTMLElement>("[data-focus-item]");
  target?.focus();
  if (target) {
    pendingRestore = null;
  }
  return target;
}

function cssEscape(value: string): string {
  if (typeof CSS !== "undefined" && typeof CSS.escape === "function") {
    return CSS.escape(value);
  }
  return value.replace(/["\\]/g, "\\$&");
}

export function resetRenderFocusForTests() {
  rememberedAnchor = null;
  pendingRestore = null;
}
