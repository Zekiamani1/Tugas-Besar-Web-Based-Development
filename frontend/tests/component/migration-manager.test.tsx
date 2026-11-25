import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { MigrationManager } from "@/features/migrations/MigrationManager";
import { Providers } from "@/app/providers";
import type { MigrationService } from "@/lib/api/contract";
import type { MigrationRecord } from "@/lib/types";

const INITIAL_HISTORY: MigrationRecord[] = [
  {
    id: 1,
    targetDbId: 1,
    version: 1,
    sqlQuery: "CREATE TABLE public.produk (id INT PRIMARY KEY);",
    executedBy: "budi_developer",
    status: "APPLIED",
    executedAt: "2026-11-24 10:00:00 UTC",
  },
];

function createFakeMigrationService(
  overrides?: Partial<MigrationService>
): MigrationService {
  return {
    createDraft: vi.fn(),
    updateDraft: vi.fn(),
    explainDraft: vi.fn(),
    executeDraft: vi.fn(),
    ...overrides,
  };
}

describe("MigrationManager", () => {
  it("hides Create Draft, Drafts, and edit/execute controls for VIEW_ONLY users", () => {
    render(
      <Providers>
        <MigrationManager
          dbId={1}
          canEdit={false}
          initialDrafts={[]}
          initialHistory={INITIAL_HISTORY}
        />
      </Providers>
    );

    expect(
      screen.queryByRole("heading", { name: "Create Draft" })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { name: "Drafts" })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Execute" })
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "History" })
    ).toBeInTheDocument();
  });

  it("allows VIEW_EDIT users to create, edit, explain, and execute drafts with confirmation", async () => {
    const user = userEvent.setup();
    const createdDraft: MigrationRecord = {
      id: 5,
      targetDbId: 1,
      version: 0,
      sqlQuery: "SELECT id, nama FROM public.produk;",
      executedBy: "",
      status: "DRAFT",
      executedAt: "",
    };
    const updatedSql = "SELECT id, nama, stok FROM public.produk WHERE stok > 5;";
    const updatedDraft: MigrationRecord = {
      ...createdDraft,
      sqlQuery: updatedSql,
    };

    const migrationService = createFakeMigrationService({
      createDraft: vi.fn<MigrationService["createDraft"]>(async () => ({
        ok: true,
        data: createdDraft,
      })),
      updateDraft: vi.fn<MigrationService["updateDraft"]>(async () => ({
        ok: true,
        message: "Draft #5 berhasil disimpan.",
        data: updatedDraft,
      })),
      explainDraft: vi.fn<MigrationService["explainDraft"]>(async () => ({
        ok: true,
        data: "Seq Scan on public.produk  (cost=0.00..18.50 rows=12 width=64)",
      })),
      executeDraft: vi.fn<MigrationService["executeDraft"]>(async () => ({
        ok: true,
        message: "Migration #5 berhasil dijalankan (Version 2).",
        data: {
          ...updatedDraft,
          version: 2,
          status: "APPLIED",
          executedBy: "budi_developer",
          executedAt: "2026-11-25 09:00:00 UTC",
        },
      })),
    });

    render(
      <Providers services={{ migrations: migrationService }}>
        <MigrationManager
          dbId={1}
          canEdit
          initialDrafts={[]}
          initialHistory={INITIAL_HISTORY}
        />
      </Providers>
    );

    await user.type(
      screen.getByLabelText("SQL Query", { selector: "#draft-sql" }),
      "SELECT id, nama FROM public.produk;"
    );
    await user.click(screen.getByRole("button", { name: "Create Draft" }));

    expect(migrationService.createDraft).toHaveBeenCalledWith(
      1,
      "SELECT id, nama FROM public.produk;"
    );
    expect(
      await screen.findByRole("heading", { level: 2, name: "Migration #5" })
    ).toBeInTheDocument();

    const editArea = screen.getByLabelText("SQL Query", {
      selector: "#edit-sql",
    });
    await user.clear(editArea);
    await user.type(editArea, updatedSql);
    await user.click(screen.getByRole("button", { name: "Save Draft" }));

    expect(migrationService.updateDraft).toHaveBeenCalledWith(1, 5, updatedSql);
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Draft #5 berhasil disimpan."
    );

    await user.click(screen.getByRole("button", { name: "Lihat Query Plan" }));
    expect(migrationService.explainDraft).toHaveBeenCalledWith(1, 5);
    expect(
      await screen.findByText(/Seq Scan on public\.produk/)
    ).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Execute" }));
    expect(
      screen.getByText("Jalankan migration ini? Pastikan SQL sudah benar.")
    ).toBeInTheDocument();

    await user.click(
      screen.getByRole("button", { name: "Konfirmasi Execute" })
    );
    expect(migrationService.executeDraft).toHaveBeenCalledWith(1, 5);
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Migration #5 berhasil dijalankan (Version 2)."
    );
    expect(
      screen.queryByRole("button", { name: "Save Draft" })
    ).not.toBeInTheDocument();
  });
});
