#!/usr/bin/env node
// Fails when app CSS or TSX carries a raw colour (CORE-1179). Colour comes from the theme
// roles (src/styles/theme.css over the generated src/styles/tokens.css), so a preset or theme
// document re-skins everything; a literal would stay fixed under every theme.
//
//   node scripts/check-colour-literals.mjs     # scans src/**/*.css and src/**/*.tsx
//
// Exempt: the generated src/styles/tokens.css and test files. Colour data that is user
// content (subtitle swatches, library accents) belongs in .ts modules, not markup or styles.

import { readdirSync, readFileSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const APP_DIR = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const EXEMPT = new Set(["src/styles/tokens.css"]);

const NAMED = [
  "aliceblue antiquewhite aqua aquamarine azure beige bisque black blanchedalmond blue",
  "blueviolet brown burlywood cadetblue chartreuse chocolate coral cornflowerblue cornsilk",
  "crimson cyan darkblue darkcyan darkgoldenrod darkgray darkgreen darkgrey darkkhaki",
  "darkmagenta darkolivegreen darkorange darkorchid darkred darksalmon darkseagreen",
  "darkslateblue darkslategray darkslategrey darkturquoise darkviolet deeppink deepskyblue",
  "dimgray dimgrey dodgerblue firebrick floralwhite forestgreen fuchsia gainsboro ghostwhite",
  "gold goldenrod gray green greenyellow grey honeydew hotpink indianred indigo ivory khaki",
  "lavender lavenderblush lawngreen lemonchiffon lightblue lightcoral lightcyan",
  "lightgoldenrodyellow lightgray lightgreen lightgrey lightpink lightsalmon lightseagreen",
  "lightskyblue lightslategray lightslategrey lightsteelblue lightyellow lime limegreen linen",
  "magenta maroon mediumaquamarine mediumblue mediumorchid mediumpurple mediumseagreen",
  "mediumslateblue mediumspringgreen mediumturquoise mediumvioletred midnightblue mintcream",
  "mistyrose moccasin navajowhite navy oldlace olive olivedrab orange orangered orchid",
  "palegoldenrod palegreen paleturquoise palevioletred papayawhip peachpuff peru pink plum",
  "powderblue purple rebeccapurple red rosybrown royalblue saddlebrown salmon sandybrown",
  "seagreen seashell sienna silver skyblue slateblue slategray slategrey snow springgreen",
  "steelblue tan teal thistle tomato turquoise violet wheat white whitesmoke yellow",
  "yellowgreen",
]
  .join(" ")
  .split(" ");

const HEX = /#(?:[0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{3,4})(?![\w-])/;
const FUNCTION = /(?<![\w-])(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch|color)\(/i;
const NAMED_WORD = new RegExp(`(?<![\\w-])(?:${NAMED.join("|")})(?![\\w-])`, "i");
const PALETTE =
  "(?:slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)-\\d{2,3}";
const UTILITY =
  "(?:bg|text|border(?:-[trblxyse])?|ring(?:-offset)?|from|via|to|fill|stroke|outline|divide|placeholder|decoration|caret|accent|shadow)";
const TAILWIND = new RegExp(`(?<![\\w-])${UTILITY}-(?:white|black|${PALETTE})(?![\\w-])`);

const stripCssComments = (css) => css.replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, " "));
const stripStrings = (value) => value.replace(/"[^"]*"|'[^']*'/g, '""');

/** Declarations of a stylesheet: { line, property, value } for every `prop: value`. */
function declarations(css) {
  const out = [];
  const source = stripCssComments(css);
  let start = 0;
  for (let i = 0; i <= source.length; i++) {
    const ch = source[i];
    if (ch !== "{" && ch !== "}" && i !== source.length) continue;
    const pieces = source.slice(start, i).split(";");
    if (ch === "{") pieces.pop(); // the selector or at-rule prelude of the block that opens here
    let offset = start;
    for (const piece of pieces) {
      const colon = piece.indexOf(":");
      if (colon > 0 && !piece.trimStart().startsWith("@")) {
        const property = piece.slice(0, colon).trim();
        const lead = piece.length - piece.trimStart().length;
        const line = source.slice(0, offset + lead).split("\n").length;
        out.push({ line, property, value: piece.slice(colon + 1) });
      }
      offset += piece.length + 1;
    }
    start = i + 1;
  }
  return out;
}

/** Problems in one CSS file. */
export function checkCss(css) {
  const problems = [];
  for (const { line, property, value } of declarations(css)) {
    if (
      /^(?:font-family|content|src|grid-template-areas|grid-area|animation(?:-name)?)$/i.test(
        property,
      )
    ) {
      continue;
    }
    const scanned = stripStrings(value);
    const hit = HEX.exec(scanned) ?? FUNCTION.exec(scanned) ?? NAMED_WORD.exec(scanned);
    if (hit) problems.push({ line, text: `${property}: ${value.trim()}`, match: hit[0] });
  }
  return problems;
}

/** Problems in one TSX file: colour functions, hex colours and Tailwind palette classes. */
export function checkTsx(source) {
  const problems = [];
  source.split("\n").forEach((text, index) => {
    const trimmed = text.trim();
    if (trimmed.startsWith("//") || trimmed.startsWith("*") || trimmed.startsWith("/*")) return;
    const strings = text.match(/"[^"]*"|'[^']*'|`[^`]*`/g) ?? [];
    const hex = strings.map((s) => HEX.exec(s)).find(Boolean);
    const hit = FUNCTION.exec(text) ?? TAILWIND.exec(text) ?? hex;
    if (hit) problems.push({ line: index + 1, text: trimmed, match: hit[0] });
  });
  return problems;
}

function* walk(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) yield* walk(path);
    else yield path;
  }
}

/** Every problem under `src/`, as { file, line, text, match }. */
export function scan(appDir = APP_DIR) {
  const problems = [];
  for (const path of walk(join(appDir, "src"))) {
    const file = relative(appDir, path).split("\\").join("/");
    if (EXEMPT.has(file) || /\.test\.tsx?$/.test(file)) continue;
    const check = file.endsWith(".css") ? checkCss : file.endsWith(".tsx") ? checkTsx : null;
    if (!check) continue;
    for (const problem of check(readFileSync(path, "utf8"))) problems.push({ file, ...problem });
  }
  return problems;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const problems = scan();
  for (const p of problems)
    console.error(`${p.file}:${p.line}: raw colour "${p.match}" — ${p.text}`);
  if (problems.length) {
    console.error(
      `\n${problems.length} raw colour literal(s); use a theme role (src/styles/theme.css).`,
    );
    process.exit(1);
  }
  console.log("colour literals: none outside the generated tokens");
}
