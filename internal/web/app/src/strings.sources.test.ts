import { readdirSync, readFileSync } from "node:fs";
import { basename, resolve } from "node:path";
import ts from "typescript";
import { describe, expect, it } from "vitest";

function findDuplicateStringKeys(shards: Iterable<readonly [string, string]>): string[] {
  const owners = new Map<string, string>();
  const duplicates: string[] = [];

  for (const [file, text] of shards) {
    const source = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true);
    const keys = new Set<string>();
    // A locale object can spread another shard. Only its explicit properties define keys.
    function visit(node: ts.Node) {
      if (ts.isPropertyAssignment(node) && ts.isPropertyAssignment(node.parent.parent)) {
        const { name } = node;
        if (ts.isIdentifier(name) || ts.isStringLiteral(name)) {
          keys.add(name.text);
        } else if (ts.isComputedPropertyName(name) && ts.isStringLiteralLike(name.expression)) {
          keys.add(name.expression.text);
        }
      }
      ts.forEachChild(node, visit);
    }
    visit(source);

    const shard = basename(file);
    for (const key of keys) {
      const owner = owners.get(key);
      if (owner) {
        duplicates.push(`${key}: ${owner} and ${shard}`);
      } else {
        owners.set(key, shard);
      }
    }
  }
  return duplicates;
}

describe("localized string sources", () => {
  const sourceDirectory = resolve(process.cwd(), "src");
  const shardDirectory = resolve(sourceDirectory, "strings");
  const shardFiles = readdirSync(shardDirectory, { withFileTypes: true })
    .filter((entry) => entry.isFile() && entry.name.endsWith(".ts") && entry.name !== "catalog.ts")
    .map((entry) => resolve(shardDirectory, entry.name));
  const sourceFiles = [
    resolve(sourceDirectory, "strings.ts"),
    resolve(sourceDirectory, "strings.test.ts"),
    resolve(sourceDirectory, "strings.sources.test.ts"),
    resolve(shardDirectory, "catalog.ts"),
    ...shardFiles,
  ];

  it("defines each string key in only one shard", () => {
    const shards = shardFiles.map((file) => [file, readFileSync(file, "utf8")] as const);
    expect(findDuplicateStringKeys(shards)).toEqual([]);
  });

  it.each(['["transcodeAutomatic"]', "[`transcodeAutomatic`]"])(
    "detects a duplicate across shards with a computed key %s",
    (key) => {
      const settingsHelp = resolve(shardDirectory, "settings-help.ts");
      const sessionsUpdates = resolve(shardDirectory, "sessions-updates.ts");
      const sessionsSource = readFileSync(sessionsUpdates, "utf8");
      const withDuplicate = sessionsSource.replace(/en: \{/, `en: { ${key}: "Automatic",`);
      expect(withDuplicate).not.toEqual(sessionsSource);
      expect(
        findDuplicateStringKeys([
          [settingsHelp, readFileSync(settingsHelp, "utf8")],
          [sessionsUpdates, withDuplicate],
        ]),
      ).toEqual(["transcodeAutomatic: settings-help.ts and sessions-updates.ts"]);
    },
  );

  it("keeps every authored registry source within 20 KB and 600 lines", () => {
    for (const file of sourceFiles) {
      const source = readFileSync(file, "utf8");

      expect(Buffer.byteLength(source), `${file} byte size`).toBeLessThanOrEqual(20_000);
      expect(source.split("\n").length, `${file} line count`).toBeLessThanOrEqual(600);
    }
  });
});
