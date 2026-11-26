"use client";

import Link from "next/link";
import { useContext, useEffect, useState } from "react";
import { AlertMessage } from "@/components/ui/AlertMessage";
import { Card } from "@/components/ui/Card";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { ServicesContext } from "@/lib/api/context";
import type {
  MigrationJob,
  MigrationReview,
  MigrationReviewDecision,
} from "@/lib/types";
import { isTerminalJobStatus } from "./logic";

export function MigrationReviewPanel({
  dbId,
  initialReview,
  initialJob,
  currentUserId,
}: {
  dbId: number;
  initialReview: MigrationReview;
  initialJob: MigrationJob | null;
  currentUserId: number | null;
}) {
  const { migrations: migrationService } = useContext(ServicesContext);
  const [review, setReview] = useState<MigrationReview>(initialReview);
  const [activeJob, setActiveJob] = useState<MigrationJob | null>(initialJob);
  const [submittingGroupId, setSubmittingGroupId] = useState<number | null>(
    null
  );
  const [decisionError, setDecisionError] = useState<string | null>(null);
  const [decisionSuccess, setDecisionSuccess] = useState<string | null>(null);
  const [decisionNotice, setDecisionNotice] = useState<string | null>(null);
  const [lookupJobId, setLookupJobId] = useState(
    initialJob ? String(initialJob.jobId) : ""
  );
  const [jobLookupError, setJobLookupError] = useState<string | null>(null);
  const [jobResumeError, setJobResumeError] = useState<string | null>(null);
  const [jobResumeMessage, setJobResumeMessage] = useState<string | null>(null);

  const pollingJobId =
    activeJob && !isTerminalJobStatus(activeJob.status)
      ? activeJob.jobId
      : null;

  useEffect(() => {
    if (!migrationService || !pollingJobId) return;
    let cancelled = false;
    const timer = window.setInterval(() => {
      void migrationService
        .getJob(dbId, review.migrationId, pollingJobId)
        .then((res) => {
          if (!cancelled && res.ok && res.data) {
            setActiveJob(res.data);
          }
        });
    }, 2000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [dbId, migrationService, pollingJobId, review.migrationId]);

  async function handleDecision(
    groupId: number,
    decision: MigrationReviewDecision
  ) {
    if (!migrationService) return;
    setSubmittingGroupId(groupId);
    setDecisionError(null);
    setDecisionSuccess(null);
    setDecisionNotice(null);

    const res = await migrationService.decideReview(
      dbId,
      review.migrationId,
      review.reviewId,
      groupId,
      decision
    );

    const refreshed = await migrationService.getReview(
      dbId,
      review.migrationId,
      review.reviewId
    );
    const updatedReview = refreshed.ok && refreshed.data ? refreshed.data : null;
    if (updatedReview) {
      setReview(updatedReview);
    }
    setSubmittingGroupId(null);

    if (!res.ok) {
      setDecisionError(res.error);
      return;
    }
    setDecisionSuccess(res.message ?? "Keputusan review berhasil disimpan.");

    const returnedJobId = res.data?.jobId ?? null;
    if (returnedJobId) {
      setLookupJobId(String(returnedJobId));
      const jobRes = await migrationService.getJob(
        dbId,
        review.migrationId,
        returnedJobId
      );
      if (jobRes.ok && jobRes.data) {
        setActiveJob(jobRes.data);
      }
      if (typeof window !== "undefined") {
        window.history.replaceState(
          null,
          "",
          `/databases/${dbId}/migrations/${review.migrationId}/reviews/${review.reviewId}?job_id=${returnedJobId}`
        );
      }
    } else if ((updatedReview ?? review).status === "APPROVED") {
      setDecisionNotice(
        "Review telah disetujui seluruh group. Header Location tidak tersedia di respons ini; masukkan Job ID secara manual di bawah jika ingin memantau status job."
      );
    }
  }

  async function handleLookupJob() {
    if (!migrationService) return;
    const parsedJobId = Number.parseInt(lookupJobId.trim(), 10);
    if (!Number.isSafeInteger(parsedJobId) || parsedJobId <= 0) {
      setJobLookupError("Job ID harus berupa angka positif.");
      return;
    }
    const res = await migrationService.getJob(
      dbId,
      review.migrationId,
      parsedJobId
    );
    if (!res.ok || !res.data) {
      setJobLookupError(res.ok ? "Job tidak ditemukan." : res.error);
      return;
    }
    setJobLookupError(null);
    setActiveJob(res.data);
    if (typeof window !== "undefined") {
      window.history.replaceState(
        null,
        "",
        `/databases/${dbId}/migrations/${review.migrationId}/reviews/${review.reviewId}?job_id=${parsedJobId}`
      );
    }
  }

  async function handleResumeJob(job: MigrationJob) {
    if (!migrationService) return;
    const res = await migrationService.resumeJob(
      dbId,
      job.migrationId,
      job.jobId
    );
    if (!res.ok || !res.data) {
      setJobResumeError(
        res.ok ? "Job gagal dilanjutkan." : res.error
      );
      setJobResumeMessage(null);
      return;
    }
    setJobResumeError(null);
    setJobResumeMessage(
      res.message ?? `Job #${res.data.jobId} berhasil dilanjutkan.`
    );
    setActiveJob({ ...job, status: "QUEUED", error: undefined });
  }

  return (
    <>
      <Card title={`Review #${review.reviewId}`}>
        <p>
          Status: <StatusBadge status={review.status} /> · Migration #
          {review.migrationId} · Action: {review.action} · Mode: {review.mode} ·
          Actor: User #{review.actorId}
        </p>
        <p className="muted">
          {review.action === "APPLY"
            ? `Revision: ${review.revision}`
            : `Expected Execution ID: ${review.expectedExecutionId ?? "-"}`}
          {review.mode === "BATCH"
            ? ` · Batch: ${review.batchTable ?? "-"}.${review.batchAttribute ?? "-"} (size ${review.batchSize})`
            : null}
        </p>
        <h3>SQL Snapshot</h3>
        <pre className="sql-preview">{review.sqlSnapshot || "(kosong)"}</pre>
      </Card>

      <Card title="Group Approvals">
        <AlertMessage variant="error" message={decisionError} />
        <AlertMessage variant="success" message={decisionSuccess} />
        {decisionNotice ? <p className="muted">{decisionNotice}</p> : null}

        <ul className="item-list">
          {review.groups.length > 0 ? (
            review.groups.map((group) => {
              const canDecide =
                review.status === "PENDING_REVIEW" &&
                group.status === "PENDING";
              const canRetryHandoff =
                review.status === "APPROVED" &&
                group.status === "APPROVED" &&
                currentUserId !== null &&
                group.decidedBy === currentUserId &&
                !activeJob;

              return (
                <li key={group.groupId}>
                  <strong>Group #{group.groupId}</strong>{" "}
                  <StatusBadge status={group.status} />
                  {group.decidedBy !== null || group.decidedAt ? (
                    <>
                      <br />
                      <span className="muted">
                        {group.decidedBy !== null
                          ? `Decided by: User #${group.decidedBy}`
                          : null}
                        {group.decidedAt ? ` · ${group.decidedAt}` : null}
                      </span>
                    </>
                  ) : null}
                  {canDecide ? (
                    <div className="inline-actions">
                      <button
                        type="button"
                        disabled={submittingGroupId !== null}
                        onClick={() =>
                          void handleDecision(group.groupId, "APPROVED")
                        }
                      >
                        Approve
                      </button>
                      <button
                        type="button"
                        className="button-danger"
                        disabled={submittingGroupId !== null}
                        onClick={() =>
                          void handleDecision(group.groupId, "REJECTED")
                        }
                      >
                        Reject
                      </button>
                    </div>
                  ) : null}
                  {canRetryHandoff ? (
                    <div className="inline-actions">
                      <button
                        type="button"
                        disabled={submittingGroupId !== null}
                        onClick={() =>
                          void handleDecision(group.groupId, "APPROVED")
                        }
                      >
                        Antrekan Ulang Job
                      </button>
                    </div>
                  ) : null}
                </li>
              );
            })
          ) : (
            <li className="empty-state">Tidak ada group approval.</li>
          )}
        </ul>
      </Card>

      <Card title="Async Job Status">
        {activeJob ? (
          <div>
            <p>
              Job #{activeJob.jobId} <StatusBadge status={activeJob.status} /> ·
              Action: {activeJob.action} · Mode: {activeJob.mode}
            </p>
            <p className="muted">
              Execution ID: {activeJob.executionId || "-"} · Attempts:{" "}
              {activeJob.attempts}
              {activeJob.mode === "BATCH" ||
              activeJob.totalRows > 0 ||
              activeJob.committedBatches > 0
                ? ` · Progress: ${activeJob.committedRows} / ${activeJob.totalRows} rows (${activeJob.committedBatches} batches, cursor ${activeJob.cursor})`
                : null}
            </p>
            <AlertMessage variant="error" message={activeJob.error ?? null} />
            {activeJob.status === "PARTIAL" ||
            activeJob.status === "PARTIALLY_REVERTED" ? (
              <div className="inline-actions">
                <button
                  type="button"
                  onClick={() => void handleResumeJob(activeJob)}
                >
                  Resume Job
                </button>
              </div>
            ) : null}
            <AlertMessage variant="error" message={jobResumeError} />
            <AlertMessage variant="success" message={jobResumeMessage} />
            {isTerminalJobStatus(activeJob.status) ? (
              <p>
                <Link
                  href={`/databases/${dbId}/migrations?selected=${review.migrationId}&job_id=${activeJob.jobId}`}
                >
                  Lihat di Daftar Migrations
                </Link>
              </p>
            ) : (
              <p className="muted">Memantau progres job secara otomatis...</p>
            )}
          </div>
        ) : (
          <p className="empty-state">Belum ada job yang dipantau.</p>
        )}

        <form
          onSubmit={(event) => {
            event.preventDefault();
            void handleLookupJob();
          }}
          noValidate
        >
          <label htmlFor="review-lookup-job-id">Job ID</label>
          <input
            id="review-lookup-job-id"
            name="job_id"
            type="number"
            min={1}
            value={lookupJobId}
            onChange={(event) => setLookupJobId(event.target.value)}
            placeholder="Masukkan Job ID"
          />
          <AlertMessage variant="error" message={jobLookupError} />
          <button type="submit">Cek Status Job</button>
        </form>
      </Card>
    </>
  );
}
