// Types for scripts/check-colour-literals.mjs, so the Vitest lint test can import it.
export interface ColourProblem {
  line: number;
  text: string;
  match: string;
}
export function checkCss(css: string): ColourProblem[];
export function checkTsx(source: string): ColourProblem[];
export function scan(appDir?: string): Array<ColourProblem & { file: string }>;
