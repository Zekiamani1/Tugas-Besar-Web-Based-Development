import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { DatabaseExplorer } from "@/features/databases/DatabaseExplorer";
import { Providers } from "@/app/providers";
import type { DatabaseService } from "@/lib/api/contract";
import type {
  DatabaseExplorerInitialData,
  TableDataResult,
  TableMetadata,
} from "@/lib/types";

const PRODUK_METADATA: TableMetadata = {
  schema: "public",
  tableName: "produk",
  columns: [
    { name: "id", dataType: "integer", isNullable: false },
    { name: "nama", dataType: "text", isNullable: false },
    { name: "harga", dataType: "integer", isNullable: false },
    { name: "stok", dataType: "integer", isNullable: true },
  ],
  constraints: [{ name: "produk_pkey", type: "PRIMARY KEY" }],
  indexes: [{ name: "produk_pkey", isUnique: true }],
};

const EMPTY_AUDIT_METADATA: TableMetadata = {
  schema: "analytics",
  tableName: "empty_audit",
  columns: [],
  constraints: [],
  indexes: [],
};

const PRODUK_PAGE_1: TableDataResult = {
  columns: ["harga", "id", "nama", "stok"],
  rows: [{ harga: 21000000, id: 1, nama: "Laptop ThinkPad X1", stok: 8 }],
  totalRows: 12,
  page: 1,
  pageSize: 10,
  totalPages: 2,
};

const PRODUK_PAGE_2: TableDataResult = {
  columns: ["harga", "id", "nama", "stok"],
  rows: [{ harga: 250000, id: 11, nama: "Standing Desk Mat", stok: 25 }],
  totalRows: 12,
  page: 2,
  pageSize: 10,
  totalPages: 2,
};

const EMPTY_TABLE_DATA: TableDataResult = {
  columns: [],
  rows: [],
  totalRows: 0,
  page: 1,
  pageSize: 10,
  totalPages: 1,
};

const INITIAL_EXPLORER_DATA: DatabaseExplorerInitialData = {
  schemas: ["public", "analytics"],
  activeSchema: "public",
  tables: ["produk", "pesanan"],
  activeTable: "produk",
  activeTab: "structure",
  metadata: PRODUK_METADATA,
  tableData: PRODUK_PAGE_1,
};

function createFakeDatabaseService(
  overrides?: Partial<DatabaseService>
): DatabaseService {
  return {
    getTables: vi.fn(async () => []),
    getTableMetadata: vi.fn(async () => null),
    getTableData: vi.fn(async () => EMPTY_TABLE_DATA),
    ...overrides,
  };
}

describe("DatabaseExplorer", () => {
  it("switches schemas and tables and renders structure metadata", async () => {
    const user = userEvent.setup();
    const getTables = vi
      .fn<DatabaseService["getTables"]>()
      .mockResolvedValueOnce(["daily_metrics", "empty_audit"]);
    const getTableMetadata = vi
      .fn<DatabaseService["getTableMetadata"]>()
      .mockResolvedValueOnce({
        schema: "analytics",
        tableName: "daily_metrics",
        columns: [],
        constraints: [],
        indexes: [],
      })
      .mockResolvedValueOnce(EMPTY_AUDIT_METADATA);

    const databaseService = createFakeDatabaseService({
      getTables,
      getTableMetadata,
    });

    render(
      <Providers services={{ databases: databaseService }}>
        <DatabaseExplorer dbId={1} initialData={INITIAL_EXPLORER_DATA} />
      </Providers>
    );

    expect(
      screen.getByRole("heading", { level: 2, name: "public.produk" })
    ).toBeInTheDocument();
    expect(screen.getByText(/nama \(text\) Nullable: false/)).toBeInTheDocument();
    expect(
      screen.getByText(/produk_pkey \(PRIMARY KEY\)/)
    ).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "analytics" }));
    expect(getTables).toHaveBeenCalledWith(1, "analytics");
    expect(
      await screen.findByRole("heading", {
        level: 2,
        name: "Tables · analytics",
      })
    ).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "empty_audit" }));
    expect(getTableMetadata).toHaveBeenLastCalledWith(
      1,
      "analytics",
      "empty_audit"
    );
    expect(
      await screen.findByText("Tidak ada kolom yang dapat ditampilkan.")
    ).toBeInTheDocument();
    expect(screen.getByText("Tidak ada constraint.")).toBeInTheDocument();
    expect(screen.getByText("Tidak ada index.")).toBeInTheDocument();
  });

  it("displays sorted columns, pagination, and empty data state in Table Data tab", async () => {
    const user = userEvent.setup();
    const getTables = vi
      .fn<DatabaseService["getTables"]>()
      .mockResolvedValueOnce(["empty_audit"]);
    const getTableMetadata = vi
      .fn<DatabaseService["getTableMetadata"]>()
      .mockResolvedValueOnce(PRODUK_METADATA)
      .mockResolvedValue(EMPTY_AUDIT_METADATA);
    const getTableData = vi
      .fn<DatabaseService["getTableData"]>()
      .mockResolvedValueOnce(PRODUK_PAGE_1)
      .mockResolvedValueOnce(PRODUK_PAGE_2)
      .mockResolvedValue(EMPTY_TABLE_DATA);

    const databaseService = createFakeDatabaseService({
      getTables,
      getTableMetadata,
      getTableData,
    });

    render(
      <Providers services={{ databases: databaseService }}>
        <DatabaseExplorer dbId={1} initialData={INITIAL_EXPLORER_DATA} />
      </Providers>
    );

    await user.click(screen.getByRole("button", { name: "Data produk" }));
    expect(await screen.findByText("Laptop ThinkPad X1")).toBeInTheDocument();
    expect(screen.queryByText("Standing Desk Mat")).not.toBeInTheDocument();

    const headers = screen
      .getAllByRole("columnheader")
      .map((cell) => cell.textContent);
    expect(headers).toEqual(["harga", "id", "nama", "stok"]);

    await user.click(screen.getByRole("button", { name: "Berikutnya" }));
    expect(getTableData).toHaveBeenCalledWith(1, "public", "produk", 2, 10);
    expect(await screen.findByText("Standing Desk Mat")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "analytics" }));
    const emptyAuditBtn = await screen.findByRole("button", {
      name: "Data empty_audit",
    });
    await user.click(emptyAuditBtn);
    expect(
      await screen.findByText("Belum ada data pada tabel ini.")
    ).toBeInTheDocument();
  });

  it("renders error alert when schema loading fails", async () => {
    const user = userEvent.setup();
    const databaseService = createFakeDatabaseService({
      getTables: vi.fn(async () => {
        throw new Error("Backend unavailable");
      }),
    });

    render(
      <Providers services={{ databases: databaseService }}>
        <DatabaseExplorer dbId={1} initialData={INITIAL_EXPLORER_DATA} />
      </Providers>
    );

    await user.click(screen.getByRole("button", { name: "analytics" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Gagal memuat daftar tabel untuk schema ini."
    );
  });
});
