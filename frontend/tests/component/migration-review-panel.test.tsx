import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Providers } from "@/app/providers";
import { MigrationReviewPanel } from "@/features/migrations/MigrationReviewPanel";
import type { MigrationService } from "@/lib/api/contract";
import type { MigrationJob, MigrationReview } from "@/lib/types";

function createFakeMigrationService(
  overrides?: Partial<MigrationService>
): MigrationService {
  return {
    listState: vi.fn(),
    getPosition: vi.fn(),
    getDefinition: vi.fn(),
    createDraft: vi.fn(),
    updateDraft: vi.fn(),
    explainDraft: vi.fn(),
    submitMigration: vi.fn(),
    listPendingReviews: vi.fn(),
    getReview: vi.fn(),
    decideReview: vi.fn(),
    getJob: vi.fn(),
    ...overrides,
  };
}

const PENDING_REVIEW: MigrationReview = {
  reviewId: 7,
  targetDbId: 1,
  migrationId: 3,
  status: "PENDING_REVIEW",
  actorId: 2,
  action: "APPLY",
  mode: "STANDARD",
  batchSize: 0,
  revision: 1,
  sqlSnapshot: "CREATE TABLE demo (id INT);",
  groups: [
    { groupId: 1, status: "PENDING", decidedBy: null, decidedAt: null },
  ],
};

afterEach(() => {
  vi.useRealTimers();
});

describe("MigrationReviewPanel", () => {
  it("approves pending group, starts async job polling, and stops when job reaches terminal SUCCEEDED status", async () => {
    const approvedReview: MigrationReview = {
      ...PENDING_REVIEW,
      status: "APPROVED",
      groups: [
        {
          groupId: 1,
          status: "APPROVED",
          decidedBy: 2,
          decidedAt: "2026-11-26T00:00:00Z",
        },
      ],
    };
    const queuedJob: MigrationJob = {
      jobId: 55,
      executionId: 0,
      migrationId: 3,
      targetDbId: 1,
      action: "APPLY",
      mode: "STANDARD",
      batchSize: 0,
      status: "QUEUED",
      attempts: 0,
      committedBatches: 0,
      committedRows: 0,
      totalRows: 0,
      cursor: 0,
      createdAt: "2026-11-26T00:00:00Z",
      updatedAt: "2026-11-26T00:00:00Z",
    };
    const succeededJob: MigrationJob = {
      ...queuedJob,
      executionId: 14,
      status: "SUCCEEDED",
      attempts: 1,
    };

    const user = userEvent.setup();
    const decideReview = vi.fn<MigrationService["decideReview"]>(async () => ({
      ok: true,
      message: "Persetujuan group berhasil disimpan.",
      data: { jobId: 55, location: "/databases/1/migrations/3?job_id=55" },
    }));
    const getReview = vi.fn<MigrationService["getReview"]>(async () => ({
      ok: true,
      data: approvedReview,
    }));
    const getJob = vi
      .fn<MigrationService["getJob"]>()
      .mockResolvedValueOnce({ ok: true, data: queuedJob })
      .mockResolvedValueOnce({ ok: true, data: succeededJob });

    const migrationService = createFakeMigrationService({
      decideReview,
      getReview,
      getJob,
    });

    vi.useFakeTimers({ shouldAdvanceTime: true });

    render(
      <Providers services={{ migrations: migrationService }}>
        <MigrationReviewPanel
          dbId={1}
          initialReview={PENDING_REVIEW}
          initialJob={null}
          currentUserId={2}
        />
      </Providers>
    );

    expect(screen.getByText("PENDING_REVIEW")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Approve" }));

    expect(decideReview).toHaveBeenCalledWith(1, 3, 7, 1, "APPROVED");
    expect(await screen.findByText(/Job #55/)).toBeInTheDocument();
    expect(screen.getByText("QUEUED")).toBeInTheDocument();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2100);
    });

    expect(await screen.findByText("SUCCEEDED")).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "Lihat di Daftar Migrations" })
    ).toHaveAttribute("href", "/databases/1/migrations?selected=3&job_id=55");
  });

  it("shows fallback Job ID lookup notice when Location is absent and supports retry handoff", async () => {
    const user = userEvent.setup();
    const approvedWithoutJobReview: MigrationReview = {
      ...PENDING_REVIEW,
      status: "APPROVED",
      groups: [
        {
          groupId: 1,
          status: "APPROVED",
          decidedBy: 2,
          decidedAt: "2026-11-26T00:00:00Z",
        },
      ],
    };

    const decideReview = vi.fn<MigrationService["decideReview"]>(async () => ({
      ok: true,
      message: "Persetujuan group berhasil disimpan.",
      data: { jobId: 77, location: "/databases/1/migrations/3?job_id=77" },
    }));
    const getReview = vi.fn<MigrationService["getReview"]>(async () => ({
      ok: true,
      data: approvedWithoutJobReview,
    }));
    const getJob = vi.fn<MigrationService["getJob"]>(async () => ({
      ok: true,
      data: {
        jobId: 77,
        executionId: 22,
        migrationId: 3,
        targetDbId: 1,
        action: "APPLY",
        mode: "STANDARD",
        batchSize: 0,
        status: "SUCCEEDED",
        attempts: 1,
        committedBatches: 0,
        committedRows: 0,
        totalRows: 0,
        cursor: 0,
        createdAt: "2026-11-26T00:00:00Z",
        updatedAt: "2026-11-26T00:00:05Z",
      },
    }));

    const migrationService = createFakeMigrationService({
      decideReview,
      getReview,
      getJob,
    });

    render(
      <Providers services={{ migrations: migrationService }}>
        <MigrationReviewPanel
          dbId={1}
          initialReview={approvedWithoutJobReview}
          initialJob={null}
          currentUserId={2}
        />
      </Providers>
    );

    const retryButton = screen.getByRole("button", {
      name: "Antrekan Ulang Job",
    });
    await user.click(retryButton);
    expect(decideReview).toHaveBeenCalledWith(1, 3, 7, 1, "APPROVED");
    expect(await screen.findByText(/Job #77/)).toBeInTheDocument();
  });
});
