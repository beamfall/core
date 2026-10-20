import "@testing-library/jest-dom/vitest";
import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Input } from "@/components/ui/input";
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { I18nProvider } from "@/lib/i18n";
import {
  ControlRow,
  DataTable,
  EmptyState,
  ErrorState,
  Fieldset,
  ManageField,
  ManageInlineForm,
  ManageSection,
  ManageTable,
  StatusBadge,
  StatusChip,
} from ".";

function renderKit(node: React.ReactNode) {
  return render(<I18nProvider>{node}</I18nProvider>);
}

describe("Beamfall Workbench kit", () => {
  it("renders Manage panels, forms, tables, and status badges with stable hooks", () => {
    renderKit(
      <ManageSection
        title="Libraries"
        lead="Library roots"
        actions={<StatusBadge state="green">healthy</StatusBadge>}
      >
        <ManageInlineForm className="border-t pt-3">
          <ManageField id="library-title" label="Title">
            <Input id="library-title" />
          </ManageField>
        </ManageInlineForm>
        <ManageTable>
          <TableHeader>
            <TableRow>
              <TableHead>Title</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow>
              <TableCell>Home Movies</TableCell>
            </TableRow>
          </TableBody>
        </ManageTable>
      </ManageSection>,
    );

    expect(screen.getByText("Libraries")).toHaveClass("manage-section-title");
    expect(screen.getByText("healthy")).toHaveAttribute("data-state", "green");
    expect(screen.getByLabelText("Title")).toBeVisible();
    expect(screen.getByRole("table")).toHaveClass("manage-table");
    expect(screen.getByText("Home Movies")).toBeVisible();
  });

  it("renders Console state, table, and fieldset components", () => {
    renderKit(
      <>
        <StatusChip tone="amber">queued</StatusChip>
        <DataTable columns={[{ key: "name", label: "Name" }]}>
          <TableRow>
            <TableCell>Worker</TableCell>
          </TableRow>
        </DataTable>
        <Fieldset title="consoleConfigHeading">
          <ControlRow label="consoleConfigBackgroundWorkers" control={<span>2</span>} />
        </Fieldset>
        <EmptyState message="none" />
        <ErrorState message="requestFailed" />
      </>,
    );

    expect(screen.getByText("queued")).toHaveClass("is-amber");
    expect(within(screen.getByRole("table")).getByText("Worker")).toBeVisible();
    expect(screen.getByText("Server configuration")).toHaveClass("console-fieldset-title");
    expect(screen.getByText("Background workers")).toHaveClass("console-control-label");
    expect(screen.getByText("None")).toHaveClass("console-empty-line");
    expect(screen.getByRole("alert")).toHaveTextContent("Request failed.");
  });
});
