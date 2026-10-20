import { readdirSync, readFileSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { describe, expect, it } from "vitest";

const appRoot = resolve(import.meta.dirname, "../../..");
const sourceRoot = join(appRoot, "src");
const facadeModules = new Set(["components/ui/astryx-primitives.ts", "components/ui/tabs.tsx"]);
const targetedRadixImport =
  /@radix-ui\/react-(?:alert-dialog|combobox|dialog|dropdown-menu|menu|menubar|popover|select|slider|tabs)\b/;
const directBaseUIImport = /["']@base-ui\/react(?:\/[^"']+)?["']/;

const manualWidgetPatterns = [
  {
    name: "manual focus traversal",
    pattern:
      /(?:event|e)\.key[^\n]{0,160}(?:Tab|ArrowDown|ArrowUp|ArrowLeft|ArrowRight|Home|End)[\s\S]{0,1600}(?:querySelectorAll|\.focus\(\))/,
  },
  {
    name: "manual focusable collection",
    pattern:
      /querySelectorAll(?:<[^>]+>)?\([^)]*(?:button|tabindex)[^)]*\)[\s\S]{0,1200}(?:Tab|ArrowDown|ArrowUp|ArrowLeft|ArrowRight|Home|End)[\s\S]{0,800}\.focus\(\)/,
  },
  {
    name: "manual roving keyboard navigation",
    pattern:
      /(?:event|e)\.key[\s\S]{0,400}(?:ArrowLeft|ArrowRight|Home|End)[\s\S]{0,1600}(?:\.focus\(\)|tabIndex|activeIndex|setActive(?:Index|ID|Id))/,
  },
  {
    name: "manual aria-activedescendant management",
    pattern: /(?:aria-activedescendant|ariaActiveDescendant)/i,
  },
  {
    name: "manual typeahead",
    pattern:
      /(?:typeahead|keyBuffer|searchBuffer|typedKeys)[\s\S]{0,1200}(?:startsWith|find\()[\s\S]{0,800}\.focus\(\)/i,
  },
] as const;

const skippedDirectories = new Set([
  "coverage",
  "dist",
  "node_modules",
  "playwright-report",
  "test-results",
]);

function sourceFiles(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      if (skippedDirectories.has(entry.name)) return [];
      return sourceFiles(path);
    }
    if (!/\.[jt]sx?$/.test(entry.name)) return [];
    if (/\.(?:test|spec)\.[jt]sx?$/.test(entry.name)) return [];
    return [path];
  });
}

function packageManifests(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      if (skippedDirectories.has(entry.name)) return [];
      return packageManifests(path);
    }
    return entry.name === "package.json" ? [path] : [];
  });
}

function sourceViolations(source: string, facade: boolean): string[] {
  const found: string[] = [];
  if (targetedRadixImport.test(source)) found.push("targeted direct Radix import");
  if (!facade && directBaseUIImport.test(source)) {
    found.push("direct Base UI import outside facade");
  }
  if (facade) return found;
  for (const check of manualWidgetPatterns) {
    if (check.pattern.test(source)) found.push(check.name);
  }
  return found;
}

function manifestViolations(source: string): string[] {
  const manifest = JSON.parse(source) as Record<string, unknown>;
  const dependencySections = [
    "dependencies",
    "devDependencies",
    "optionalDependencies",
    "peerDependencies",
  ];
  return dependencySections.flatMap((section) => {
    const dependencies = manifest[section];
    if (!dependencies || typeof dependencies !== "object" || Array.isArray(dependencies)) return [];
    return Object.keys(dependencies).flatMap((name) =>
      targetedRadixImport.test(name) ? [`targeted Radix dependency ${name}`] : [],
    );
  });
}

describe("migrated widget conformance", () => {
  it.each([
    ["targeted direct Radix import", 'import * as Select from "@radix-ui/react-select";'],
    ["direct Base UI import outside facade", 'import { Select } from "@base-ui/react/select";'],
    [
      "manual focus traversal",
      `if (event.key === "ArrowDown") {
        const items = panel.querySelectorAll("button:not(:disabled)");
        items[0]?.focus();
      }`,
    ],
    [
      "manual focusable collection",
      `const focusable = root.querySelectorAll<HTMLElement>("button, [tabindex]");
      if (event.key === "Tab") focusable[0]?.focus();`,
    ],
    [
      "manual roving keyboard navigation",
      `switch (event.key) {
        case "ArrowRight": setActiveIndex(activeIndex + 1); break;
        case "Home": setActiveIndex(0); break;
        case "End": setActiveIndex(items.length - 1); break;
      }`,
    ],
    [
      "manual aria-activedescendant management",
      `<input role="combobox" aria-activedescendant={activeOptionID} />`,
    ],
    [
      "manual typeahead",
      `keyBuffer += event.key;
      const match = items.find((item) => item.textContent?.startsWith(keyBuffer));
      match?.focus();`,
    ],
  ])("detects %s", (name, source) => {
    expect(sourceViolations(source, false)).toContain(name);
  });

  it("detects targeted Radix dependencies in package manifests", () => {
    const manifest = JSON.stringify({
      devDependencies: { "@radix-ui/react-select": "1.0.0" },
    });
    expect(manifestViolations(manifest)).toEqual([
      "targeted Radix dependency @radix-ui/react-select",
    ]);
  });

  it("keeps production widgets on the Beamfall Base UI facades", () => {
    const sourceFindings = sourceFiles(sourceRoot).flatMap((path) => {
      const sourcePath = relative(sourceRoot, path);
      return sourceViolations(readFileSync(path, "utf8"), facadeModules.has(sourcePath)).map(
        (violation) => `${sourcePath}: ${violation}`,
      );
    });
    const manifestFindings = packageManifests(appRoot).flatMap((path) =>
      manifestViolations(readFileSync(path, "utf8")).map(
        (violation) => `${relative(appRoot, path)}: ${violation}`,
      ),
    );

    expect([...sourceFindings, ...manifestFindings]).toEqual([]);
  });
});
