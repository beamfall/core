import { mkdirSync, renameSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";

const EXPECTED_TESTS_BY_PROJECT = new Map([
  ["web-compat-firefox", 2],
  ["web-compat-webkit", 2],
]);

function publishExecutedTestCount(executedTestCount) {
  const path = process.env.BEAMFALL_WEB_COMPAT_EXECUTION_PROVENANCE;
  if (!path) return;

  const temporary = `${path}.${process.pid}.tmp`;
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(
    temporary,
    `${JSON.stringify({ schemaVersion: 1, executedTestCount }, null, 2)}\n`,
    { mode: 0o600 },
  );
  renameSync(temporary, path);
}

class RequireExecutedTestsReporter {
  executedByProject = new Map(
    [...EXPECTED_TESTS_BY_PROJECT.keys()].map((project) => [project, new Set()]),
  );

  onTestEnd(test, result) {
    if (result.status === "skipped" || result.status === "interrupted") return;
    const project = test.parent.project()?.name;
    if (!this.executedByProject.has(project)) return;
    this.executedByProject.get(project).add(test.id);
  }

  onEnd() {
    const incomplete = [...EXPECTED_TESTS_BY_PROJECT].filter(
      ([project, expected]) => this.executedByProject.get(project).size !== expected,
    );
    if (incomplete.length === 0) {
      const executedTestCount = [...this.executedByProject.values()].reduce(
        (total, testIds) => total + testIds.size,
        0,
      );
      publishExecutedTestCount(executedTestCount);
      return;
    }
    for (const [project, expected] of incomplete) {
      const actual = this.executedByProject.get(project).size;
      process.stderr.write(
        `FAIL web-compat-nightly required browser ${project} expected=${expected} executed=${actual}\n`,
      );
    }
    return { status: "failed" };
  }
}

export default RequireExecutedTestsReporter;
