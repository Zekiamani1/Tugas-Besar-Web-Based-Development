"use client";

import Link from "next/link";
import { useContext, useEffect, useRef, useState } from "react";
import { AlertMessage } from "@/components/ui/AlertMessage";
import { Card } from "@/components/ui/Card";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { Tabs, type TabItem } from "@/components/ui/Tabs";
import { ServicesContext } from "@/lib/api/context";
import type {
  MigrationDefinition,
  MigrationExecutionMode,
  MigrationJob,
  MigrationPosition,
  MigrationRecord,
  MigrationReview,
  MigrationReviewDecision,
  MigrationTab,
} from "@/lib/types";
import { isTerminalJobStatus, resolveSelectedMigration } from "./logic";

function replaceUrl(url: string) {
  if (typeof window !== "undefined") {
    window.history.replaceState(null, "", url);
  }
}

function buildMigrationsUrl(
  dbId: number,
  params: {
    selectedId: number | null;
    hideRollback: boolean;
    jobId: number | null;
    tab: MigrationTab;
  }
): string {
  const search = new URLSearchParams();
  if (params.selectedId) {
    search.set("selected", String(params.selectedId));
  }
  if (params.hideRollback) {
    search.set("hide_rollback", "true");
  }
  if (params.jobId) {
    search.set("job_id", String(params.jobId));
  }
  if (params.tab === "reviews") {
    search.set("tab", "reviews");
  }
  const qs = search.toString();
  return qs
    ? `/databases/${dbId}/migrations?${qs}`
    : `/databases/${dbId}/migrations`;
}

function DraftDefinitionFields({
  idPrefix,
  sqlUp,
  sqlDown,
  irreversible,
  irreversibleReason,
  onSqlUpChange,
  onSqlDownChange,
  onIrreversibleChange,
  onReasonChange,
}: {
  idPrefix: string;
  sqlUp: string;
  sqlDown: string;
  irreversible: boolean;
  irreversibleReason: string;
  onSqlUpChange: (value: string) => void;
  onSqlDownChange: (value: string) => void;
  onIrreversibleChange: (value: boolean) => void;
  onReasonChange: (value: string) => void;
}) {
  return (
    <>
      <label htmlFor={`${idPrefix}-sql-up`}>SQL Up</label>
      <textarea
        id={`${idPrefix}-sql-up`}
        name="sql_up"
        value={sqlUp}
        onChange={(event) => onSqlUpChange(event.target.value)}
        placeholder="CREATE TABLE ..."
        required
      />

      <label className="checkbox-label" htmlFor={`${idPrefix}-irreversible`}>
        <input
          id={`${idPrefix}-irreversible`}
          type="checkbox"
          checked={irreversible}
          onChange={(event) => onIrreversibleChange(event.target.checked)}
        />
        Irreversible (tanpa SQL Down)
      </label>

      {irreversible ? (
        <>
          <label htmlFor={`${idPrefix}-reason`}>Alasan Irreversible</label>
          <input
            id={`${idPrefix}-reason`}
            name="irreversible_reason"
            value={irreversibleReason}
            onChange={(event) => onReasonChange(event.target.value)}
            placeholder="Contoh: Menghapus kolom permanen"
            required
          />
        </>
      ) : (
        <>
          <label htmlFor={`${idPrefix}-sql-down`}>SQL Down</label>
          <textarea
            id={`${idPrefix}-sql-down`}
            name="sql_down"
            value={sqlDown}
            onChange={(event) => onSqlDownChange(event.target.value)}
            placeholder="DROP TABLE ..."
            required
          />
        </>
      )}
    </>
  );
}

function BatchConfigFields({
  idPrefix,
  mode,
  batchSize,
  batchTable,
  batchAttribute,
  lockMode = false,
  onModeChange,
  onBatchSizeChange,
  onBatchTableChange,
  onBatchAttributeChange,
}: {
  idPrefix: string;
  mode: MigrationExecutionMode;
  batchSize: string;
  batchTable: string;
  batchAttribute: string;
  lockMode?: boolean;
  onModeChange: (value: MigrationExecutionMode) => void;
  onBatchSizeChange: (value: string) => void;
  onBatchTableChange: (value: string) => void;
  onBatchAttributeChange: (value: string) => void;
}) {
  return (
    <>
      <label htmlFor={`${idPrefix}-mode`}>Mode Eksekusi</label>
      <select
        id={`${idPrefix}-mode`}
        name="mode"
        value={mode}
        disabled={lockMode}
        onChange={(event) =>
          onModeChange(event.target.value as MigrationExecutionMode)
        }
      >
        <option value="STANDARD">STANDARD</option>
        <option value="BATCH">BATCH</option>
      </select>

      {mode === "BATCH" ? (
        <>
          <p className="muted">
            Untuk mode BATCH, pastikan SQL memiliki placeholder{" "}
            <code>{"{{batch_predicate}}"}</code>.
          </p>
          <div className="form-grid">
            <div>
              <label htmlFor={`${idPrefix}-batch-size`}>
                Batch Size (1-10000)
              </label>
              <input
                id={`${idPrefix}-batch-size`}
                name="batch_size"
                type="number"
                min={1}
                max={10000}
                value={batchSize}
                onChange={(event) => onBatchSizeChange(event.target.value)}
                placeholder="1000"
                required
              />
            </div>
            <div>
              <label htmlFor={`${idPrefix}-batch-table`}>Batch Table</label>
              <input
                id={`${idPrefix}-batch-table`}
                name="batch_table"
                value={batchTable}
                onChange={(event) => onBatchTableChange(event.target.value)}
                placeholder="public.orders"
                required
              />
            </div>
            <div>
              <label htmlFor={`${idPrefix}-batch-attr`}>
                Batch Attribute
              </label>
              <input
                id={`${idPrefix}-batch-attr`}
                name="batch_attribute"
                value={batchAttribute}
                onChange={(event) => onBatchAttributeChange(event.target.value)}
                placeholder="id"
                required
              />
            </div>
          </div>
        </>
      ) : null}
    </>
  );
}

export function MigrationManager({
  dbId,
  canEdit,
  initialDrafts,
  initialHistory,
  initialPosition = { activeHead: null, nextForward: null },
  initialPendingReviews = [],
  initialActiveJobs = [],
  initialHideRollback = false,
  initialJob = null,
  initialSelectedId,
  initialTab = "migrations",
}: {
  dbId: number;
  canEdit: boolean;
  initialDrafts: MigrationRecord[];
  initialHistory: MigrationRecord[];
  initialPosition?: MigrationPosition;
  initialPendingReviews?: MigrationReview[];
  initialActiveJobs?: MigrationJob[];
  initialHideRollback?: boolean;
  initialJob?: MigrationJob | null;
  initialSelectedId?: number;
  initialTab?: MigrationTab;
}) {
  const { migrations: migrationService } = useContext(ServicesContext);
  const initialMigration = resolveSelectedMigration({
    canEdit,
    drafts: initialDrafts,
    history: initialHistory,
    selectedId: initialSelectedId ?? null,
  });

  const [activeTab, setActiveTab] = useState<MigrationTab>(initialTab);
  const [drafts, setDrafts] = useState<MigrationRecord[]>(initialDrafts);
  const [history, setHistory] = useState<MigrationRecord[]>(initialHistory);
  const [position, setPosition] = useState<MigrationPosition>(initialPosition);
  const [pendingReviews, setPendingReviews] = useState<MigrationReview[]>(
    initialPendingReviews
  );
  const [hideRollback, setHideRollback] = useState<boolean>(initialHideRollback);
  const [historyError, setHistoryError] = useState<string | null>(null);

  const [selectedId, setSelectedId] = useState<number | null>(
    initialMigration?.id ?? null
  );
  const [selectedDefinition, setSelectedDefinition] =
    useState<MigrationDefinition | null>(null);

  const [createSqlUp, setCreateSqlUp] = useState("");
  const [createSqlDown, setCreateSqlDown] = useState("");
  const [createIrreversible, setCreateIrreversible] = useState(false);
  const [createReason, setCreateReason] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);

  const [editSqlUp, setEditSqlUp] = useState(initialMigration?.sqlQuery ?? "");
  const [editSqlDown, setEditSqlDown] = useState("");
  const [editIrreversible, setEditIrreversible] = useState(false);
  const [editIrreversibleReason, setEditIrreversibleReason] = useState("");
  const [explainPlan, setExplainPlan] = useState<string | null>(null);
  const [applyMode, setApplyMode] =
    useState<MigrationExecutionMode>("STANDARD");
  const [applyBatchSize, setApplyBatchSize] = useState("");
  const [applyBatchTable, setApplyBatchTable] = useState("");
  const [applyBatchAttribute, setApplyBatchAttribute] = useState("");
  const [detailError, setDetailError] = useState<string | null>(null);
  const [detailSuccess, setDetailSuccess] = useState<string | null>(null);
  const [submittedApplyReview, setSubmittedApplyReview] = useState<{
    migrationId: number;
    reviewId: number;
  } | null>(null);

  const [positionAction, setPositionAction] = useState<
    "ROLLBACK" | "FORWARD" | null
  >(null);
  const [positionTarget, setPositionTarget] = useState<
    "head" | "partial" | "forward"
  >("head");
  const [posMode, setPosMode] = useState<MigrationExecutionMode>("STANDARD");
  const [posBatchSize, setPosBatchSize] = useState("");
  const [posBatchTable, setPosBatchTable] = useState("");
  const [posBatchAttribute, setPosBatchAttribute] = useState("");
  const [posError, setPosError] = useState<string | null>(null);
  const [posReview, setPosReview] = useState<{
    migrationId: number;
    reviewId: number;
    message: string;
  } | null>(null);

  const [reviewError, setReviewError] = useState<string | null>(null);
  const [reviewSuccess, setReviewSuccess] = useState<string | null>(null);
  const [reviewNotice, setReviewNotice] = useState<string | null>(null);
  const [submittingReviewKey, setSubmittingReviewKey] = useState<string | null>(
    null
  );

  const initialMonitoredJob = initialJob ?? initialActiveJobs[0] ?? null;
  const [activeJobs, setActiveJobs] =
    useState<MigrationJob[]>(initialActiveJobs);
  const [activeJob, setActiveJob] = useState<MigrationJob | null>(
    initialMonitoredJob
  );
  const [lookupJobId, setLookupJobId] = useState(
    initialMonitoredJob ? String(initialMonitoredJob.jobId) : ""
  );
  const [lookupMigrationId, setLookupMigrationId] = useState(
    initialMonitoredJob
      ? String(initialMonitoredJob.migrationId)
      : initialMigration
        ? String(initialMigration.id)
        : ""
  );
  const [jobLookupError, setJobLookupError] = useState<string | null>(null);
  const [jobResumeMessage, setJobResumeMessage] = useState<string | null>(null);
  const editDirtyRef = useRef(false);

  const partialJob =
    activeJobs.find(
      (job) =>
        job.status === "PARTIAL" && job.action === "APPLY" && job.executionId > 0
    ) ??
    (activeJob &&
    activeJob.status === "PARTIAL" &&
    activeJob.action === "APPLY" &&
    activeJob.executionId > 0
      ? activeJob
      : null);
  const partialExecution =
    [...history]
      .reverse()
      .find((item) => item.status === "PARTIAL" && item.action === "APPLY") ??
    null;
  const partialTarget = partialJob
    ? {
        migrationId: partialJob.migrationId,
        executionId: partialJob.executionId,
      }
    : partialExecution && (partialExecution.executionId ?? 0) > 0
      ? {
          migrationId: partialExecution.id,
          executionId: partialExecution.executionId!,
        }
      : null;

  const selectedMigration = resolveSelectedMigration({
    canEdit,
    drafts,
    history,
    selectedId,
  });
  const selectedMigrationId = selectedMigration?.id ?? null;
  const activeDefinition =
    selectedMigration &&
    selectedDefinition?.migrationId === selectedMigration.id
      ? selectedDefinition
      : null;

  useEffect(() => {
    if (!migrationService || !selectedMigrationId) return;
    let cancelled = false;
    void migrationService
      .getDefinition(dbId, selectedMigrationId)
      .then((res) => {
        if (cancelled || !res.ok || !res.data) return;
        const def = res.data;
        setSelectedDefinition(def);
        if (!editDirtyRef.current) {
          setEditSqlUp(def.sqlUp);
          setEditSqlDown(def.sqlDown);
          setEditIrreversible(def.irreversible);
          setEditIrreversibleReason(def.irreversibleReason);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [dbId, migrationService, selectedMigrationId]);

  const pollingJobId =
    activeJob && !isTerminalJobStatus(activeJob.status)
      ? activeJob.jobId
      : null;
  const pollingMigrationId =
    activeJob && !isTerminalJobStatus(activeJob.status)
      ? activeJob.migrationId
      : null;

  useEffect(() => {
    if (!migrationService || !pollingJobId || !pollingMigrationId) return;
    let cancelled = false;
    const timer = window.setInterval(() => {
      void migrationService
        .getJob(dbId, pollingMigrationId, pollingJobId)
        .then(async (res) => {
          if (cancelled || !res.ok || !res.data) return;
          const nextJob = res.data;
          setActiveJob(nextJob);
          if (isTerminalJobStatus(nextJob.status)) {
            const [stateRes, posRes, jobsRes] = await Promise.all([
              migrationService.listState(dbId, { hideRollback }),
              migrationService.getPosition(dbId),
              migrationService.listActiveJobs(dbId),
            ]);
            if (!cancelled) {
              if (stateRes.ok && stateRes.data) {
                setDrafts(stateRes.data.drafts);
                setHistory(stateRes.data.history);
              }
              if (posRes.ok && posRes.data) {
                setPosition(posRes.data);
              }
              if (jobsRes.ok && jobsRes.data) {
                setActiveJobs(jobsRes.data);
              }
            }
          }
        });
    }, 2000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [
    dbId,
    hideRollback,
    migrationService,
    pollingJobId,
    pollingMigrationId,
  ]);

  async function refreshActiveJobs() {
    if (!migrationService) return;
    const res = await migrationService.listActiveJobs(dbId);
    if (res.ok && res.data) {
      setActiveJobs(res.data);
    }
  }

  async function refreshPendingReviews() {
    if (!migrationService) return;
    const res = await migrationService.listPendingReviews(dbId);
    if (res.ok && res.data) {
      setPendingReviews(res.data);
      setReviewError(null);
    } else if (!res.ok) {
      setReviewError(res.error);
    }
  }

  function handleTabChange(nextTab: MigrationTab) {
    setActiveTab(nextTab);
    replaceUrl(
      buildMigrationsUrl(dbId, {
        selectedId,
        hideRollback,
        jobId: activeJob?.jobId ?? null,
        tab: nextTab,
      })
    );
    if (nextTab === "reviews") {
      void refreshPendingReviews();
    }
  }

  function handleSelectMigration(migrationId: number) {
    const next = resolveSelectedMigration({
      canEdit,
      drafts,
      history,
      selectedId: migrationId,
    });
    editDirtyRef.current = false;
    setSelectedId(migrationId);
    setSelectedDefinition(null);
    setEditSqlUp(next?.sqlQuery ?? "");
    setEditSqlDown("");
    setEditIrreversible(false);
    setEditIrreversibleReason("");
    setExplainPlan(null);
    setDetailError(null);
    setDetailSuccess(null);
    setSubmittedApplyReview(null);
    if (!activeJob) {
      setLookupMigrationId(String(migrationId));
    }
    replaceUrl(
      buildMigrationsUrl(dbId, {
        selectedId: migrationId,
        hideRollback,
        jobId: activeJob?.jobId ?? null,
        tab: activeTab,
      })
    );
  }

  async function handleToggleHideRollback(nextHide: boolean) {
    setHideRollback(nextHide);
    setHistoryError(null);
    replaceUrl(
      buildMigrationsUrl(dbId, {
        selectedId,
        hideRollback: nextHide,
        jobId: activeJob?.jobId ?? null,
        tab: activeTab,
      })
    );
    if (!migrationService) return;
    const res = await migrationService.listState(dbId, {
      hideRollback: nextHide,
    });
    if (!res.ok || !res.data) {
      setHistoryError(
        res.ok ? "Riwayat gagal dimuat." : res.error
      );
      return;
    }
    setDrafts(res.data.drafts);
    setHistory(res.data.history);
  }

  async function handleCreateDraft() {
    if (!migrationService) return;
    const res = await migrationService.createDraft(dbId, {
      sqlUp: createSqlUp,
      sqlDown: createSqlDown,
      irreversible: createIrreversible,
      irreversibleReason: createReason,
    });
    if (!res.ok) {
      setCreateError(res.error);
      return;
    }
    const createdSqlUp = createSqlUp.trim();
    const createdSqlDown = createIrreversible ? "" : createSqlDown.trim();
    const createdIrreversible = createIrreversible;
    const createdReason = createIrreversible ? createReason.trim() : "";

    setCreateError(null);
    setCreateSqlUp("");
    setCreateSqlDown("");
    setCreateIrreversible(false);
    setCreateReason("");
    if (res.data) {
      const created = res.data;
      editDirtyRef.current = false;
      setDrafts((prev) => [...prev, created]);
      setSelectedId(created.id);
      setSelectedDefinition({
        migrationId: created.id,
        targetDbId: dbId,
        revision: created.revision ?? 1,
        sqlUp: createdSqlUp,
        sqlDown: createdSqlDown,
        irreversible: createdIrreversible,
        irreversibleReason: createdReason,
      });
      setEditSqlUp(created.sqlQuery || createdSqlUp);
      setEditSqlDown(createdSqlDown);
      setEditIrreversible(createdIrreversible);
      setEditIrreversibleReason(createdReason);
      setExplainPlan(null);
      setDetailError(null);
      setDetailSuccess(null);
      setSubmittedApplyReview(null);
      if (!activeJob) {
        setLookupMigrationId(String(created.id));
      }
      replaceUrl(
        buildMigrationsUrl(dbId, {
          selectedId: created.id,
          hideRollback,
          jobId: activeJob?.jobId ?? null,
          tab: activeTab,
        })
      );
    }
  }

  async function handleSaveDraft() {
    if (!selectedMigration || !migrationService) return;
    const res = await migrationService.updateDraft(dbId, selectedMigration.id, {
      sqlUp: editSqlUp,
      sqlDown: editSqlDown,
      irreversible: editIrreversible,
      irreversibleReason: editIrreversibleReason,
    });
    if (!res.ok) {
      setDetailError(res.error);
      setDetailSuccess(null);
      return;
    }
    if (res.data) {
      const updated = res.data;
      setDrafts((prev) => prev.map((d) => (d.id === updated.id ? updated : d)));
    }
    editDirtyRef.current = false;
    const defRes = await migrationService.getDefinition(
      dbId,
      selectedMigration.id
    );
    if (defRes.ok && defRes.data) {
      const def = defRes.data;
      setSelectedDefinition(def);
      setEditSqlUp(def.sqlUp);
      setEditSqlDown(def.sqlDown);
      setEditIrreversible(def.irreversible);
      setEditIrreversibleReason(def.irreversibleReason);
    } else {
      const savedSqlUp = editSqlUp.trim();
      const savedSqlDown = editIrreversible ? "" : editSqlDown.trim();
      const savedReason = editIrreversible ? editIrreversibleReason.trim() : "";
      const nextRevision =
        res.data?.revision ??
        (activeDefinition?.revision ?? selectedMigration.revision ?? 1) + 1;
      setSelectedDefinition({
        migrationId: selectedMigration.id,
        targetDbId: dbId,
        revision: nextRevision,
        sqlUp: savedSqlUp,
        sqlDown: savedSqlDown,
        irreversible: editIrreversible,
        irreversibleReason: savedReason,
      });
    }
    setDetailError(null);
    setDetailSuccess(res.message ?? "Draft berhasil disimpan.");
  }

  async function handleExplain() {
    if (!selectedMigration || !migrationService) return;
    const res = await migrationService.explainDraft(dbId, selectedMigration.id);
    if (!res.ok) {
      setDetailError(res.error);
      setDetailSuccess(null);
      setExplainPlan(null);
      return;
    }
    setDetailError(null);
    setDetailSuccess(null);
    setExplainPlan(res.data ?? "");
  }

  async function handleSubmitApply() {
    if (!selectedMigration || !migrationService) return;
    const revision =
      activeDefinition?.revision ?? selectedMigration.revision ?? 1;
    const res = await migrationService.submitMigration(
      dbId,
      selectedMigration.id,
      {
        action: "APPLY",
        mode: applyMode,
        revision,
        batchSize:
          applyMode === "BATCH"
            ? Number.parseInt(applyBatchSize, 10)
            : undefined,
        batchTable: applyMode === "BATCH" ? applyBatchTable : undefined,
        batchAttribute:
          applyMode === "BATCH" ? applyBatchAttribute : undefined,
      }
    );
    if (!res.ok || !res.data) {
      setDetailError(res.ok ? "Pengajuan gagal dikirim." : res.error);
      setDetailSuccess(null);
      setSubmittedApplyReview(null);
      return;
    }
    setDetailError(null);
    setDetailSuccess(res.message ?? "Pengajuan approval berhasil dibuat.");
    setSubmittedApplyReview({
      migrationId: selectedMigration.id,
      reviewId: res.data.reviewId,
    });
    void refreshPendingReviews();
  }

  async function handleSubmitPosition() {
    if (!migrationService || !positionAction) return;
    const targetRef =
      positionAction === "FORWARD"
        ? position.nextForward
        : positionTarget === "partial"
          ? partialTarget
          : position.activeHead;
    if (!targetRef || targetRef.executionId <= 0) {
      setPosError("Target posisi migrasi tidak tersedia.");
      return;
    }
    const res = await migrationService.submitMigration(
      dbId,
      targetRef.migrationId,
      {
        action: positionAction,
        mode: posMode,
        expectedExecutionId: targetRef.executionId,
        batchSize:
          posMode === "BATCH" ? Number.parseInt(posBatchSize, 10) : undefined,
        batchTable: posMode === "BATCH" ? posBatchTable : undefined,
        batchAttribute: posMode === "BATCH" ? posBatchAttribute : undefined,
      }
    );
    if (!res.ok || !res.data) {
      setPosError(res.ok ? "Pengajuan posisi gagal dikirim." : res.error);
      setPosReview(null);
      return;
    }
    setPosError(null);
    setPosReview({
      migrationId: targetRef.migrationId,
      reviewId: res.data.reviewId,
      message: res.message ?? `Pengajuan ${positionAction} berhasil dikirim.`,
    });
    setPositionAction(null);
    void refreshPendingReviews();
  }

  async function handleResumeJob(job: MigrationJob) {
    if (!migrationService) return;
    const res = await migrationService.resumeJob(
      dbId,
      job.migrationId,
      job.jobId
    );
    if (!res.ok || !res.data) {
      setJobLookupError(res.ok ? "Job gagal dilanjutkan." : res.error);
      setJobResumeMessage(null);
      return;
    }
    setJobLookupError(null);
    setJobResumeMessage(
      res.message ?? `Job #${res.data.jobId} berhasil dilanjutkan.`
    );
    const resumedJob: MigrationJob = {
      ...job,
      status: "QUEUED",
      error: undefined,
    };
    setActiveJob(resumedJob);
    setActiveJobs((prev) =>
      prev.map((item) => (item.jobId === job.jobId ? resumedJob : item))
    );
  }

  async function handleReviewDecision(
    review: MigrationReview,
    groupId: number,
    decision: MigrationReviewDecision
  ) {
    if (!migrationService) return;
    setSubmittingReviewKey(`${review.reviewId}:${groupId}`);
    setReviewError(null);
    setReviewSuccess(null);
    setReviewNotice(null);

    const res = await migrationService.decideReview(
      dbId,
      review.migrationId,
      review.reviewId,
      groupId,
      decision
    );

    await refreshPendingReviews();
    setSubmittingReviewKey(null);

    if (!res.ok) {
      setReviewError(res.error);
      return;
    }
    setReviewSuccess(res.message ?? "Keputusan review berhasil disimpan.");

    const returnedJobId = res.data?.jobId ?? null;
    if (returnedJobId) {
      setLookupJobId(String(returnedJobId));
      setLookupMigrationId(String(review.migrationId));
      const jobRes = await migrationService.getJob(
        dbId,
        review.migrationId,
        returnedJobId
      );
      if (jobRes.ok && jobRes.data) {
        setActiveJob(jobRes.data);
      }
      void refreshActiveJobs();
      replaceUrl(
        buildMigrationsUrl(dbId, {
          selectedId,
          hideRollback,
          jobId: returnedJobId,
          tab: activeTab,
        })
      );
      return;
    }

    const detailRes = await migrationService.getReview(
      dbId,
      review.migrationId,
      review.reviewId
    );
    if (detailRes.ok && detailRes.data?.status === "APPROVED") {
      void refreshActiveJobs();
      setReviewNotice(
        "Review telah disetujui seluruh group. Header Location tidak tersedia; masukkan Job ID pada kartu Pemantauan Job jika ingin memantau statusnya."
      );
    }
  }

  async function handleLookupJob() {
    if (!migrationService) return;
    const parsedJobId = Number.parseInt(lookupJobId.trim(), 10);
    const parsedMigId = Number.parseInt(lookupMigrationId.trim(), 10);
    if (!Number.isSafeInteger(parsedMigId) || parsedMigId <= 0) {
      setJobLookupError("Migration ID harus berupa angka positif.");
      return;
    }
    if (!Number.isSafeInteger(parsedJobId) || parsedJobId <= 0) {
      setJobLookupError("Job ID harus berupa angka positif.");
      return;
    }
    const res = await migrationService.getJob(dbId, parsedMigId, parsedJobId);
    if (!res.ok || !res.data) {
      setJobLookupError(res.ok ? "Job tidak ditemukan." : res.error);
      return;
    }
    setJobLookupError(null);
    setJobResumeMessage(null);
    setActiveJob(res.data);
    replaceUrl(
      buildMigrationsUrl(dbId, {
        selectedId,
        hideRollback,
        jobId: parsedJobId,
        tab: activeTab,
      })
    );
  }

  function handleSelectActiveJob(job: MigrationJob) {
    setJobLookupError(null);
    setJobResumeMessage(null);
    setActiveJob(job);
    setLookupMigrationId(String(job.migrationId));
    setLookupJobId(String(job.jobId));
    replaceUrl(
      buildMigrationsUrl(dbId, {
        selectedId,
        hideRollback,
        jobId: job.jobId,
        tab: activeTab,
      })
    );
  }

  const tabs: ReadonlyArray<TabItem<MigrationTab>> = [
    { id: "migrations", label: "Migrations" },
    { id: "reviews", label: "Pending Reviews", count: pendingReviews.length },
  ];

  return (
    <>
      <Tabs
        tabs={tabs}
        activeTab={activeTab}
        onChange={handleTabChange}
        ariaLabel="Bagian Migrations"
      />

      <Card title="Pemantauan Job">
        {activeJob ? (
          <div>
            <p>
              Job #{activeJob.jobId} <StatusBadge status={activeJob.status} />{" "}
              · Migration #{activeJob.migrationId} · Action: {activeJob.action}{" "}
              · Mode: {activeJob.mode}
            </p>
            <p className="muted">
              Execution ID: {activeJob.executionId || "-"} · Attempts:{" "}
              {activeJob.attempts}
              {activeJob.mode === "BATCH" && activeJob.batchTable
                ? ` · Batch: ${activeJob.batchTable}.${activeJob.batchAttribute ?? "-"} (size ${activeJob.batchSize})`
                : null}
              {activeJob.mode === "BATCH" ||
              activeJob.totalRows > 0 ||
              activeJob.committedBatches > 0
                ? ` · Progress: ${activeJob.committedRows} / ${activeJob.totalRows} rows (${activeJob.committedBatches} batches, cursor ${activeJob.cursor})`
                : null}
            </p>
            <AlertMessage variant="error" message={activeJob.error ?? null} />
            <AlertMessage variant="success" message={jobResumeMessage} />
            {canEdit &&
            (activeJob.status === "PARTIAL" ||
              activeJob.status === "PARTIALLY_REVERTED") ? (
              <div className="inline-actions">
                <button
                  type="button"
                  onClick={() => void handleResumeJob(activeJob)}
                >
                  Resume Job
                </button>
              </div>
            ) : null}
            {!isTerminalJobStatus(activeJob.status) ? (
              <p className="muted">Memantau progres job secara otomatis...</p>
            ) : null}
          </div>
        ) : (
          <p className="muted">
            Belum ada job aktif yang dipantau. Masukkan Migration ID dan Job ID
            untuk memeriksa status eksekusi async.
          </p>
        )}

        <form
          onSubmit={(event) => {
            event.preventDefault();
            void handleLookupJob();
          }}
          noValidate
        >
          <div className="form-grid">
            <div>
              <label htmlFor="lookup-migration-id">Migration ID</label>
              <input
                id="lookup-migration-id"
                name="migration_id"
                type="number"
                min={1}
                value={lookupMigrationId}
                onChange={(event) => setLookupMigrationId(event.target.value)}
                placeholder="1"
              />
            </div>
            <div>
              <label htmlFor="lookup-job-id">Job ID</label>
              <input
                id="lookup-job-id"
                name="job_id"
                type="number"
                min={1}
                value={lookupJobId}
                onChange={(event) => setLookupJobId(event.target.value)}
                placeholder="1"
              />
            </div>
          </div>
          <AlertMessage variant="error" message={jobLookupError} />
          <button type="submit">Cek Status Job</button>
        </form>

        {activeJobs.length > 0 ? (
          <>
            <h3>Job Belum Selesai</h3>
            <ul className="item-list">
              {activeJobs.map((job) => (
                <li key={`active-job-${job.jobId}`}>
                  <strong>Job #{job.jobId}</strong>{" "}
                  <StatusBadge status={job.status} /> · Migration #
                  {job.migrationId} · Action: {job.action} · Mode: {job.mode}
                  <br />
                  <span className="muted">
                    Execution ID: {job.executionId || "-"}
                    {job.mode === "BATCH" && job.batchTable
                      ? ` · Batch: ${job.batchTable}.${job.batchAttribute ?? "-"} (size ${job.batchSize}) · Progress: ${job.committedRows} / ${job.totalRows} rows (${job.committedBatches} batches)`
                      : null}
                  </span>
                  <div className="inline-actions">
                    <button
                      type="button"
                      className="button-subtle"
                      onClick={() => handleSelectActiveJob(job)}
                    >
                      Pantau
                    </button>
                  </div>
                </li>
              ))}
            </ul>
          </>
        ) : null}
      </Card>

      {activeTab === "migrations" ? (
        <>
          <Card title="Posisi Migrasi">
            <p>
              <strong>Active Head:</strong>{" "}
              {position.activeHead
                ? `Migration #${position.activeHead.migrationId} · Execution #${position.activeHead.executionId} · Rev ${position.activeHead.revision}`
                : "Belum ada migrasi aktif."}
            </p>
            <p>
              <strong>Next Forward:</strong>{" "}
              {position.nextForward
                ? `Migration #${position.nextForward.migrationId} · Execution #${position.nextForward.executionId} · Rev ${position.nextForward.revision}`
                : "Tidak ada."}
            </p>
            {partialTarget ? (
              <div className="message error" role="region" aria-label="Status eksekusi partial">
                <strong>Eksekusi Batch Partial:</strong> Migration #
                {partialTarget.migrationId} · Execution #
                {partialTarget.executionId} · <StatusBadge status="PARTIAL" />
              </div>
            ) : null}

            <AlertMessage variant="error" message={posError} />
            {posReview ? (
              <div className="message success" role="status">
                {posReview.message}{" "}
                <Link
                  href={`/databases/${dbId}/migrations/${posReview.migrationId}/reviews/${posReview.reviewId}`}
                >
                  Buka Review #{posReview.reviewId}
                </Link>
              </div>
            ) : null}

            {canEdit ? (
              <>
                <div className="inline-actions">
                  {position.activeHead ? (
                    <button
                      type="button"
                      className="button-danger"
                      onClick={() => {
                        setPositionTarget("head");
                        setPositionAction("ROLLBACK");
                        setPosMode("STANDARD");
                        setPosBatchSize("");
                        setPosBatchTable("");
                        setPosBatchAttribute("");
                        setPosError(null);
                      }}
                    >
                      Ajukan Rollback Head
                    </button>
                  ) : null}
                  {position.nextForward ? (
                    <button
                      type="button"
                      onClick={() => {
                        setPositionTarget("forward");
                        setPositionAction("FORWARD");
                        setPosMode("STANDARD");
                        setPosBatchSize("");
                        setPosBatchTable("");
                        setPosBatchAttribute("");
                        setPosError(null);
                      }}
                    >
                      Ajukan Forward Berikutnya
                    </button>
                  ) : null}
                  {partialTarget ? (
                    <button
                      type="button"
                      className="button-danger"
                      onClick={() => {
                        setPositionTarget("partial");
                        setPositionAction("ROLLBACK");
                        setPosMode("BATCH");
                        setPosBatchSize(
                          partialJob && partialJob.batchSize > 0
                            ? String(partialJob.batchSize)
                            : ""
                        );
                        setPosBatchTable(partialJob?.batchTable ?? "");
                        setPosBatchAttribute(partialJob?.batchAttribute ?? "");
                        setPosError(null);
                      }}
                    >
                      Ajukan Rollback Kompensasi
                    </button>
                  ) : null}
                </div>

                {positionAction ? (
                  <form
                    className="confirm-box"
                    onSubmit={(event) => {
                      event.preventDefault();
                      void handleSubmitPosition();
                    }}
                    noValidate
                  >
                    <p>
                      <strong>Pengajuan {positionAction}:</strong>{" "}
                      {positionAction === "ROLLBACK" &&
                      positionTarget === "partial" &&
                      partialTarget
                        ? `Kompensasi Partial Migration #${partialTarget.migrationId} (Execution #${partialTarget.executionId})`
                        : positionAction === "ROLLBACK" && position.activeHead
                          ? `Migration #${position.activeHead.migrationId} (Execution #${position.activeHead.executionId})`
                          : positionAction === "FORWARD" && position.nextForward
                            ? `Migration #${position.nextForward.migrationId} (Execution #${position.nextForward.executionId})`
                            : "-"}
                    </p>
                    <BatchConfigFields
                      idPrefix="pos"
                      mode={posMode}
                      batchSize={posBatchSize}
                      batchTable={posBatchTable}
                      batchAttribute={posBatchAttribute}
                      lockMode={positionTarget === "partial"}
                      onModeChange={setPosMode}
                      onBatchSizeChange={setPosBatchSize}
                      onBatchTableChange={setPosBatchTable}
                      onBatchAttributeChange={setPosBatchAttribute}
                    />
                    <div className="inline-actions">
                      <button type="submit">
                        Submit {positionAction} Review
                      </button>
                      <button
                        type="button"
                        className="button-subtle"
                        onClick={() => {
                          setPositionAction(null);
                          setPosError(null);
                        }}
                      >
                        Batal
                      </button>
                    </div>
                  </form>
                ) : null}
              </>
            ) : null}
          </Card>

          {canEdit ? (
            <>
              <Card title="Create Draft">
                <form
                  onSubmit={(event) => {
                    event.preventDefault();
                    void handleCreateDraft();
                  }}
                  noValidate
                >
                  <DraftDefinitionFields
                    idPrefix="create-draft"
                    sqlUp={createSqlUp}
                    sqlDown={createSqlDown}
                    irreversible={createIrreversible}
                    irreversibleReason={createReason}
                    onSqlUpChange={setCreateSqlUp}
                    onSqlDownChange={setCreateSqlDown}
                    onIrreversibleChange={setCreateIrreversible}
                    onReasonChange={setCreateReason}
                  />
                  <AlertMessage variant="error" message={createError} />
                  <button type="submit">Create Draft</button>
                </form>
              </Card>

              <Card title="Drafts">
                <ul className="item-list">
                  {drafts.length > 0 ? (
                    drafts.map((draft) => {
                      const isActive = selectedMigration?.id === draft.id;
                      return (
                        <li
                          key={draft.id}
                          className={isActive ? "item-active" : undefined}
                        >
                          <button
                            type="button"
                            className="selectable-link"
                            onClick={() => handleSelectMigration(draft.id)}
                          >
                            {`Draft #${draft.id}`}
                          </button>{" "}
                          <StatusBadge status={draft.status} />
                          {draft.revision ? (
                            <span className="muted">
                              {" "}
                              · Rev {draft.revision}
                            </span>
                          ) : null}
                          <pre className="sql-preview">{draft.sqlQuery}</pre>
                        </li>
                      );
                    })
                  ) : (
                    <li className="empty-state">No drafts yet.</li>
                  )}
                </ul>
              </Card>
            </>
          ) : null}

          <Card title="History">
            <div className="section-header">
              <span className="muted">
                Total eksekusi tampil: {history.length}
              </span>
              <label className="checkbox-label" htmlFor="hide-rollback-toggle">
                <input
                  id="hide-rollback-toggle"
                  type="checkbox"
                  checked={hideRollback}
                  onChange={(event) =>
                    void handleToggleHideRollback(event.target.checked)
                  }
                />
                Sembunyikan pasangan rollback
              </label>
            </div>
            <AlertMessage variant="error" message={historyError} />
            <ul className="item-list">
              {history.length > 0 ? (
                history.map((item, index) => {
                  const isActive = selectedMigration?.id === item.id;
                  const rowKey = item.executionId
                    ? `exec-${item.executionId}`
                    : `mig-${item.id}-${index}`;
                  return (
                    <li
                      key={rowKey}
                      className={isActive ? "item-active" : undefined}
                    >
                      <button
                        type="button"
                        className="selectable-link"
                        onClick={() => handleSelectMigration(item.id)}
                      >
                        {`Migration #${item.id}`}
                      </button>{" "}
                      <StatusBadge status={item.status} />
                      <br />
                      <span className="muted">
                        {item.executionId
                          ? `Execution #${item.executionId} · `
                          : null}
                        {item.action ? `Action: ${item.action} · ` : null}
                        {item.revision ? `Rev: ${item.revision} · ` : null}
                        Version: {item.version} · Executed by: {item.executedBy}{" "}
                        · Executed at: {item.executedAt}
                      </span>
                      <pre className="sql-preview">{item.sqlQuery}</pre>
                    </li>
                  );
                })
              ) : (
                <li className="empty-state">Belum ada riwayat migration.</li>
              )}
            </ul>
          </Card>

          {selectedMigration ? (
            <Card title={`Migration #${selectedMigration.id}`}>
              <AlertMessage variant="error" message={detailError} />
              <AlertMessage variant="success" message={detailSuccess} />
              {submittedApplyReview ? (
                <p>
                  <Link
                    href={`/databases/${dbId}/migrations/${submittedApplyReview.migrationId}/reviews/${submittedApplyReview.reviewId}`}
                  >
                    Buka Review #{submittedApplyReview.reviewId}
                  </Link>
                </p>
              ) : null}

              <p>
                Version: {selectedMigration.version}
                {activeDefinition?.revision ?? selectedMigration.revision
                  ? ` · Rev ${activeDefinition?.revision ?? selectedMigration.revision}`
                  : null}
                {selectedMigration.executionId
                  ? ` · Execution #${selectedMigration.executionId}`
                  : null}
                {selectedMigration.action
                  ? ` · Action: ${selectedMigration.action}`
                  : null}{" "}
                · <StatusBadge status={selectedMigration.status} />
              </p>
              {selectedMigration.executedBy ? (
                <p>
                  ExecutedBy: {selectedMigration.executedBy}
                  {selectedMigration.status !== "DRAFT"
                    ? ` · ExecutedAt: ${selectedMigration.executedAt}`
                    : null}
                </p>
              ) : null}

              <h3>SQL Up</h3>
              <pre className="sql-preview">
                {activeDefinition?.sqlUp ?? selectedMigration.sqlQuery}
              </pre>

              {activeDefinition ? (
                activeDefinition.irreversible ? (
                  <>
                    <h3>Status Reversibilitas</h3>
                    <p className="muted">
                      Irreversible: {activeDefinition.irreversibleReason}
                    </p>
                  </>
                ) : (
                  <>
                    <h3>SQL Down</h3>
                    <pre className="sql-preview">
                      {activeDefinition.sqlDown}
                    </pre>
                  </>
                )
              ) : null}

              {canEdit && selectedMigration.status === "DRAFT" ? (
                <>
                  <h3>Edit Draft</h3>
                  <form
                    onSubmit={(event) => {
                      event.preventDefault();
                      void handleSaveDraft();
                    }}
                    noValidate
                  >
                    <DraftDefinitionFields
                      idPrefix="edit-draft"
                      sqlUp={editSqlUp}
                      sqlDown={editSqlDown}
                      irreversible={editIrreversible}
                      irreversibleReason={editIrreversibleReason}
                      onSqlUpChange={(value) => {
                        editDirtyRef.current = true;
                        setEditSqlUp(value);
                      }}
                      onSqlDownChange={(value) => {
                        editDirtyRef.current = true;
                        setEditSqlDown(value);
                      }}
                      onIrreversibleChange={(value) => {
                        editDirtyRef.current = true;
                        setEditIrreversible(value);
                      }}
                      onReasonChange={(value) => {
                        editDirtyRef.current = true;
                        setEditIrreversibleReason(value);
                      }}
                    />
                    <button type="submit">Save Draft</button>
                  </form>

                  <h3>Query Plan</h3>
                  <p className="muted">
                    Lihat estimasi rencana PostgreSQL. Query tidak dijalankan.
                  </p>
                  <button type="button" onClick={() => void handleExplain()}>
                    Lihat Query Plan
                  </button>
                  {explainPlan ? (
                    <pre className="sql-preview">{explainPlan}</pre>
                  ) : null}

                  <h3>Submit untuk Approval</h3>
                  <p className="muted">
                    Migrasi dijalankan secara async setelah disetujui oleh
                    seluruh leader grup terkait.
                  </p>
                  <form
                    onSubmit={(event) => {
                      event.preventDefault();
                      void handleSubmitApply();
                    }}
                    noValidate
                  >
                    <BatchConfigFields
                      idPrefix="apply"
                      mode={applyMode}
                      batchSize={applyBatchSize}
                      batchTable={applyBatchTable}
                      batchAttribute={applyBatchAttribute}
                      onModeChange={setApplyMode}
                      onBatchSizeChange={setApplyBatchSize}
                      onBatchTableChange={setApplyBatchTable}
                      onBatchAttributeChange={setApplyBatchAttribute}
                    />
                    <button type="submit">Submit untuk Approval</button>
                  </form>
                </>
              ) : null}
            </Card>
          ) : null}
        </>
      ) : (
        <Card title="Pending Reviews">
          <div className="section-header">
            <span className="muted">
              Review yang menunggu keputusan Anda sebagai leader grup.
            </span>
            <button
              type="button"
              className="button-subtle"
              onClick={() => void refreshPendingReviews()}
            >
              Refresh Reviews
            </button>
          </div>
          <AlertMessage variant="error" message={reviewError} />
          <AlertMessage variant="success" message={reviewSuccess} />
          {reviewNotice ? <p className="muted">{reviewNotice}</p> : null}

          <ul className="item-list">
            {pendingReviews.length > 0 ? (
              pendingReviews.map((review) => (
                <li key={review.reviewId}>
                  <Link
                    href={`/databases/${dbId}/migrations/${review.migrationId}/reviews/${review.reviewId}`}
                  >
                    {`Review #${review.reviewId}`}
                  </Link>{" "}
                  <StatusBadge status={review.status} />
                  <br />
                  <span className="muted">
                    Migration #{review.migrationId} · Action: {review.action} ·
                    Mode: {review.mode} ·{" "}
                    {review.action === "APPLY"
                      ? `Rev ${review.revision}`
                      : `Expected Execution #${review.expectedExecutionId ?? "-"}`}
                    {review.mode === "BATCH"
                      ? ` · Batch ${review.batchTable ?? "-"}.${review.batchAttribute ?? "-"} (${review.batchSize})`
                      : null}
                  </span>
                  <pre className="sql-preview">
                    {review.sqlSnapshot || "(kosong)"}
                  </pre>
                  {review.groups.map((group) => (
                    <div key={group.groupId} className="inline-actions">
                      <span>
                        Group #{group.groupId}{" "}
                        <StatusBadge status={group.status} />
                      </span>
                      {review.status === "PENDING_REVIEW" &&
                      group.status === "PENDING" ? (
                        <>
                          <button
                            type="button"
                            disabled={submittingReviewKey !== null}
                            onClick={() =>
                              void handleReviewDecision(
                                review,
                                group.groupId,
                                "APPROVED"
                              )
                            }
                          >
                            Approve
                          </button>
                          <button
                            type="button"
                            className="button-danger"
                            disabled={submittingReviewKey !== null}
                            onClick={() =>
                              void handleReviewDecision(
                                review,
                                group.groupId,
                                "REJECTED"
                              )
                            }
                          >
                            Reject
                          </button>
                        </>
                      ) : null}
                    </div>
                  ))}
                </li>
              ))
            ) : (
              <li className="empty-state">
                Tidak ada review yang menunggu keputusan Anda.
              </li>
            )}
          </ul>
        </Card>
      )}
    </>
  );
}
