import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { AlertMessage } from "@/components/ui/AlertMessage";
import { Card } from "@/components/ui/Card";
import { DataTable, type DataTableColumn } from "@/components/ui/DataTable";
import { PageLayout } from "@/components/ui/PageLayout";
import { StatusBadge, formatAccessLevel } from "@/components/ui/StatusBadge";
import { Tabs } from "@/components/ui/Tabs";

interface SampleItem {
  id: number;
  name: string;
}

const SAMPLE_COLUMNS: DataTableColumn<SampleItem>[] = [
  { key: "id", header: "ID", render: (row) => row.id },
  { key: "name", header: "Name", render: (row) => row.name },
];

describe("Reusable UI components", () => {
  it("renders PageLayout and Card with classic header and section structure", () => {
    render(
      <PageLayout
        subtitle="Database Manager"
        title="Ringkasan"
        nav={<a href="/dashboard">Dashboard</a>}
      >
        <Card title="Daftar" actions={<span>2 item</span>}>
          <p>Konten kartu</p>
        </Card>
      </PageLayout>
    );

    expect(screen.getByText("Database Manager")).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { level: 1, name: "Ringkasan" })
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { level: 2, name: "Daftar" })
    ).toBeInTheDocument();
    expect(screen.getByText("2 item")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Dashboard" })).toHaveAttribute(
      "href",
      "/dashboard"
    );
  });

  it("renders AlertMessage with accessible roles and hides when empty", () => {
    const { rerender } = render(
      <AlertMessage variant="error" message="Gagal menyimpan" />
    );
    expect(screen.getByRole("alert")).toHaveTextContent("Gagal menyimpan");

    rerender(<AlertMessage variant="success" message="Berhasil disimpan" />);
    expect(screen.getByRole("status")).toHaveTextContent("Berhasil disimpan");

    rerender(<AlertMessage variant="error" message={null} />);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("renders StatusBadge with data-status attribute and formats access levels", () => {
    render(
      <div>
        <StatusBadge status="APPLIED" />
        <StatusBadge status="FAILED" />
        <StatusBadge status="DRAFT" />
      </div>
    );

    expect(screen.getByText("APPLIED")).toHaveAttribute(
      "data-status",
      "APPLIED"
    );
    expect(screen.getByText("FAILED")).toHaveAttribute("data-status", "FAILED");
    expect(screen.getByText("DRAFT")).toHaveAttribute("data-status", "DRAFT");
    expect(formatAccessLevel("VIEW_ONLY")).toBe("View Only");
    expect(formatAccessLevel("VIEW_EDIT")).toBe("View + Edit");
  });

  it("renders DataTable rows, empty state, and pagination callbacks", async () => {
    const user = userEvent.setup();
    const onPageChange = vi.fn();

    const { rerender } = render(
      <DataTable
        columns={SAMPLE_COLUMNS}
        rows={[]}
        getRowKey={(row) => row.id}
        emptyMessage="Data kosong"
      />
    );
    expect(screen.getByText("Data kosong")).toBeInTheDocument();

    rerender(
      <DataTable
        columns={SAMPLE_COLUMNS}
        rows={[
          { id: 1, name: "Satu" },
          { id: 2, name: "Dua" },
        ]}
        getRowKey={(row) => row.id}
        emptyMessage="Data kosong"
        pagination={{
          page: 1,
          totalPages: 2,
          totalRows: 12,
          onPageChange,
        }}
      />
    );

    expect(screen.getByText("Satu")).toBeInTheDocument();
    expect(screen.getByText("Halaman 1 dari 2 (12 baris)")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Sebelumnya" })).toBeDisabled();

    await user.click(screen.getByRole("button", { name: "Berikutnya" }));
    expect(onPageChange).toHaveBeenCalledWith(2);
  });

  it("handles Tabs selection state and counts", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();

    render(
      <Tabs
        tabs={[
          { id: "users", label: "Users", count: 4 },
          { id: "groups", label: "Groups", count: 2 },
        ]}
        activeTab="users"
        onChange={onChange}
      />
    );

    expect(screen.getByRole("tab", { name: "Users (4)" })).toHaveAttribute(
      "aria-selected",
      "true"
    );
    expect(screen.getByRole("tab", { name: "Groups (2)" })).toHaveAttribute(
      "aria-selected",
      "false"
    );

    await user.click(screen.getByRole("tab", { name: "Groups (2)" }));
    expect(onChange).toHaveBeenCalledWith("groups");
  });
});
