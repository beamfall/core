// Types for scripts/generate-theme-tokens.mjs, so the Vitest drift and vector tests can import it.
export const OUTPUTS: { css: string; ts: string };
export function designSystemDir(env?: Record<string, string | undefined>): string;
export function designSystemUnavailable(dsDir?: string): string | null;
export function generate(dsDir?: string): { release: string; css: string; ts: string };
export function drift(dsDir?: string): string[];
