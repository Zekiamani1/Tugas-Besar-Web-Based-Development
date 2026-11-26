import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { Providers } from "@/app/providers";
import { MigrationManager } from "@/features/migrations/MigrationManager";
import type { MigrationService } from "@/lib/api/contract";
import type {
  MigrationPosition,
  MigrationRecord,
  MigrationReview,
} from "@/lib/types";

const INITIAL_HISTORY: MigrationRecord[] = [
  {
    id: 1,
    executionId: 10,
    action: "APPLY",
    revision: 1,
    targetDbId: 1,
    version: 1,
    sqlQuery: "CREATE TABLE public.produk (id INT PRIMARY KEY);",
    executedBy: "budi_developer",
    status: "APPLIED",
    executedAt: "2026-11-24T10:00:00Z",
  },
];

const INITIAL_POSITION: MigrationPosition = {
  activeHead: { executionId: 10, migrationId: 1, revision: 1 },
  nextForward: null,
};

function createFakeMigrationService(
  overrides?: Partial<MigrationService>
): MigrationService {
  return {
    listState: vi.fn(async () => ({
      ok: true as const,
      data: { canEdit: true, drafts: [], history: INITIAL_HISTORY },
    })),
    getPosition: vi.fn(async () => ({
      ok: true as const,
      data: INITIAL_POSITION,
    })),
    getDefinition: vi.fn(async (_dbId, migrationId) => ({
      ok: true as const,
      data: {
        migrationId,
        targetDbId: 1,
        revision: 1,
        sqlUp: "CREATE TABLE public.produk (id INT PRIMARY KEY);",
        sqlDown: "DROP TABLE public.produk;",
        irreversible: false,
        irreversibleReason: "",
      },
    })),
    createDraft: vi.fn(),
    updateDraft: vi.fn(),
    explainDraft: vi.fn(),
    submitMigration: vi.fn(),
    listPendingReviews: vi.fn(async () => ({ ok: true as const, data: [] })),
    getReview: vi.fn(),
    decideReview: vi.fn(),
    getJob: vi.fn(),
    ...overrides,
  };
}

describe("MigrationManager", () => {
  it("hides editor and submission controls for VIEW_ONLY users while showing position and rollback filter", async () => {
    const user = userEvent.setup();
    const listState = vi.fn<MigrationService["listState"]>(async () => ({
      ok: true,
      data: { canEdit: false, drafts: [], history: [] },
    }));
    const migrationService = createFakeMigrationService({ listState });

    render(
      <Providers services={{ migrations: migrationService }}>
        <MigrationManager
          dbId={1}
          canEdit={false}
          initialDrafts={[]}
          initialHistory={INITIAL_HISTORY}
          initialPosition={INITIAL_POSITION}
          initialPendingReviews={[]}
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
      screen.queryByRole("button", { name: "Submit untuk Approval" })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Ajukan Rollback Head" })
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Posisi Migrasi" })
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "History" })
    ).toBeInTheDocument();

    await user.click(
      screen.getByLabelText("Sembunyikan pasangan rollback", {
        selector: "#hide-rollback-toggle",
      })
    );
    expect(listState).toHaveBeenCalledWith(1, { hideRollback: true });
  });

  it("allows VIEW_EDIT users to create, edit, explain, submit for approval, submit rollback, and decide reviews", async () => {
    const user = userEvent.setup();
    const createdDraft: MigrationRecord = {
      id: 5,
      revision: 1,
      targetDbId: 1,
      version: 0,
      sqlQuery: "CREATE TABLE public.orders (id INT);",
      executedBy: "",
      status: "DRAFT",
      executedAt: "",
    };
    const updatedSqlUp =
      "CREATE TABLE public.orders (id INT PRIMARY KEY) WHERE {{batch_predicate}};";
    const updatedSqlDown = "DROP TABLE public.orders;";
    const updatedDraft: MigrationRecord = {
      ...createdDraft,
      revision: 2,
      sqlQuery: updatedSqlUp,
    };
    const pendingReview: MigrationReview = {
      reviewId: 12,
      targetDbId: 1,
      migrationId: 5,
      status: "PENDING_REVIEW",
      actorId: 2,
      action: "APPLY",
      mode: "BATCH",
      batchSize: 500,
      batchTable: "public.orders",
      batchAttribute: "id",
      revision: 2,
      sqlSnapshot: updatedSqlUp,
      groups: [
        { groupId: 1, status: "PENDING", decidedBy: null, decidedAt: null },
      ],
    };

    const migrationService = createFakeMigrationService({
      getDefinition: vi.fn<MigrationService["getDefinition"]>(
        async (_dbId, migrationId) => ({
          ok: true,
          data: {
            migrationId,
            targetDbId: 1,
            revision: migrationId === 5 ? 2 : 1,
            sqlUp: migrationId === 5 ? updatedSqlUp : INITIAL_HISTORY[0].sqlQuery,
            sqlDown:
              migrationId === 5 ? updatedSqlDown : "DROP TABLE public.produk;",
            irreversible: false,
            irreversibleReason: "",
          },
        })
      ),
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
        data: "Seq Scan on public.orders  (cost=0.00..18.50 rows=12 width=64)",
      })),
      submitMigration: vi.fn<MigrationService["submitMigration"]>(
        async (_dbId, _migId, input) => ({
          ok: true,
          message:
            input.action === "APPLY"
              ? "Pengajuan review #12 berhasil dibuat."
              : "Pengajuan review #13 berhasil dibuat.",
          data: { reviewId: input.action === "APPLY" ? 12 : 13 },
        })
      ),
      listPendingReviews: vi
        .fn<MigrationService["listPendingReviews"]>()
        .mockResolvedValueOnce({ ok: true, data: [pendingReview] })
        .mockResolvedValueOnce({ ok: true, data: [pendingReview] })
        .mockResolvedValueOnce({ ok: true, data: [pendingReview] })
        .mockResolvedValue({ ok: true, data: [] }),
      decideReview: vi.fn<MigrationService["decideReview"]>(async () => ({
        ok: true,
        message: "Persetujuan group berhasil disimpan.",
        data: {
          jobId: 99,
          location: "/databases/1/migrations/5?job_id=99",
        },
      })),
      getJob: vi.fn<MigrationService["getJob"]>(async () => ({
        ok: true,
        data: {
          jobId: 99,
          executionId: 21,
          migrationId: 5,
          targetDbId: 1,
          action: "APPLY",
          mode: "BATCH",
          batchSize: 500,
          batchTable: "public.orders",
          batchAttribute: "id",
          status: "SUCCEEDED",
          attempts: 1,
          committedBatches: 1,
          committedRows: 10,
          totalRows: 10,
          cursor: 10,
          createdAt: "2026-11-26T00:00:00Z",
          updatedAt: "2026-11-26T00:00:05Z",
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
          initialPosition={INITIAL_POSITION}
          initialPendingReviews={[]}
        />
      </Providers>
    );

    await user.type(
      screen.getByLabelText("SQL Up", { selector: "#create-draft-sql-up" }),
      "CREATE TABLE public.orders (id INT);"
    );
    await user.type(
      screen.getByLabelText("SQL Down", {
        selector: "#create-draft-sql-down",
      }),
      "DROP TABLE public.orders;"
    );
    await user.click(screen.getByRole("button", { name: "Create Draft" }));

    expect(migrationService.createDraft).toHaveBeenCalledWith(1, {
      sqlUp: "CREATE TABLE public.orders (id INT);",
      sqlDown: "DROP TABLE public.orders;",
      irreversible: false,
      irreversibleReason: "",
    });
    expect(
      await screen.findByRole("heading", { level: 2, name: "Migration #5" })
    ).toBeInTheDocument();

    const editUp = screen.getByLabelText("SQL Up", {
      selector: "#edit-draft-sql-up",
    });
    await user.clear(editUp);
    await user.type(
      editUp,
      "CREATE TABLE public.orders (id INT PRIMARY KEY) WHERE {{batch_predicate}};"
    );
    await user.click(screen.getByRole("button", { name: "Save Draft" }));
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Draft #5 berhasil disimpan."
    );

    await user.click(screen.getByRole("button", { name: "Lihat Query Plan" }));
    expect(migrationService.explainDraft).toHaveBeenCalledWith(1, 5);
    expect(
      await screen.findByText(/Seq Scan on public\.orders/)
    ).toBeInTheDocument();

    await user.selectOptions(
      screen.getByLabelText("Mode Eksekusi", { selector: "#apply-mode" }),
      "BATCH"
    );
    await user.type(
      screen.getByLabelText("Batch Size (1-10000)", {
        selector: "#apply-batch-size",
      }),
      "500"
    );
    await user.type(
      screen.getByLabelText("Batch Table", {
        selector: "#apply-batch-table",
      }),
      "public.orders"
    );
    await user.type(
      screen.getByLabelText("Batch Attribute", {
        selector: "#apply-batch-attr",
      }),
      "id"
    );
    await user.click(
      screen.getByRole("button", { name: "Submit untuk Approval" })
    );
    expect(migrationService.submitMigration).toHaveBeenCalledWith(1, 5, {
      action: "APPLY",
      mode: "BATCH",
      revision: 2,
      batchSize: 500,
      batchTable: "public.orders",
      batchAttribute: "id",
    });
    expect(
      await screen.findByRole("link", { name: "Buka Review #12" })
    ).toHaveAttribute("href", "/databases/1/migrations/5/reviews/12");

    await user.click(
      screen.getByRole("button", { name: "Ajukan Rollback Head" })
    );
    await user.click(
      screen.getByRole("button", { name: "Submit ROLLBACK Review" })
    );
    expect(migrationService.submitMigration).toHaveBeenLastCalledWith(1, 1, {
      action: "ROLLBACK",
      mode: "STANDARD",
      expectedExecutionId: 10,
      batchSize: undefined,
      batchTable: undefined,
      batchAttribute: undefined,
    });
    expect(
      await screen.findByRole("link", { name: "Buka Review #13" })
    ).toHaveAttribute("href", "/databases/1/migrations/1/reviews/13");

    await user.click(screen.getByRole("tab", { name: /Pending Reviews/ }));
    expect(
      await screen.findByRole("link", { name: "Review #12" })
    ).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Approve" }));
    expect(migrationService.decideReview).toHaveBeenCalledWith(
      1,
      5,
      12,
      1,
      "APPROVED"
    );
    expect(await screen.findByText(/Job #99/)).toBeInTheDocument();
    expect(screen.getByText("SUCCEEDED")).toBeInTheDocument();
  });
});
