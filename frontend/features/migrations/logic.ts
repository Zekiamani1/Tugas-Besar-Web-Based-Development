import type {
  MigrationAction,
  MigrationDraftInput,
  MigrationExecutionMode,
  MigrationExecutionRef,
  MigrationJob,
  MigrationJobStatus,
  MigrationPosition,
  MigrationRecord,
  MigrationReview,
  MigrationReviewGroup,
  MigrationReviewGroupStatus,
  MigrationReviewStatus,
  MigrationSubmissionInput,
} from "@/lib/types";

export function sortMigrationHistory(
  items: ReadonlyArray<MigrationRecord>
): MigrationRecord[] {
  return [...items].sort((a, b) => {
    if (a.executedAt !== b.executedAt) {
      return a.executedAt < b.executedAt ? -1 : 1;
    }
    const execDiff = (a.executionId ?? 0) - (b.executionId ?? 0);
    if (execDiff !== 0) {
      return execDiff;
    }
    return a.id - b.id;
  });
}

export function resolveSelectedMigration({
  canEdit,
  drafts,
  history,
  selectedId,
}: {
  canEdit: boolean;
  drafts: ReadonlyArray<MigrationRecord>;
  history: ReadonlyArray<MigrationRecord>;
  selectedId: number | null;
}): MigrationRecord | null {
  if (selectedId !== null) {
    if (canEdit) {
      const draftMatch = drafts.find((m) => m.id === selectedId);
      if (draftMatch) return draftMatch;
    }
    for (let i = history.length - 1; i >= 0; i -= 1) {
      if (history[i].id === selectedId) {
        return history[i];
      }
    }
  }
  if (canEdit && drafts.length > 0) {
    return drafts[0];
  }
  if (history.length > 0) {
    return history[history.length - 1];
  }
  return null;
}

export function validateDraftInput(input: MigrationDraftInput): string | null {
  if (!input.sqlUp.trim()) {
    return "SQL Up wajib diisi.";
  }
  if (input.irreversible && !input.irreversibleReason.trim()) {
    return "Alasan irreversible wajib diisi.";
  }
  if (!input.irreversible && !input.sqlDown.trim()) {
    return "SQL Down wajib diisi jika migrasi dapat di-rollback.";
  }
  return null;
}

export function buildDraftRequestBody(input: MigrationDraftInput): {
  sql_up: string;
  sql_down: string;
  irreversible: boolean;
  irreversible_reason: string;
} {
  return {
    sql_up: input.sqlUp.trim(),
    sql_down: input.irreversible ? "" : input.sqlDown.trim(),
    irreversible: input.irreversible,
    irreversible_reason: input.irreversible
      ? input.irreversibleReason.trim()
      : "",
  };
}

export interface SubmissionRequestBody {
  action: MigrationAction;
  mode: MigrationExecutionMode;
  revision?: number;
  expected_execution_id?: number;
  batch_size?: number;
  batch_table?: string;
  batch_attribute?: string;
}

export function buildSubmissionPayload(
  input: MigrationSubmissionInput
): { ok: true; body: SubmissionRequestBody } | { ok: false; error: string } {
  if (
    input.action !== "APPLY" &&
    input.action !== "ROLLBACK" &&
    input.action !== "FORWARD"
  ) {
    return { ok: false, error: "Aksi migrasi tidak valid." };
  }
  if (input.mode !== "STANDARD" && input.mode !== "BATCH") {
    return { ok: false, error: "Mode eksekusi tidak valid." };
  }

  const body: SubmissionRequestBody = {
    action: input.action,
    mode: input.mode,
  };

  if (input.action === "APPLY") {
    if (!Number.isSafeInteger(input.revision) || (input.revision ?? 0) <= 0) {
      return { ok: false, error: "Revision draft wajib berupa angka positif." };
    }
    body.revision = input.revision;
  } else {
    if (
      !Number.isSafeInteger(input.expectedExecutionId) ||
      (input.expectedExecutionId ?? 0) <= 0
    ) {
      return {
        ok: false,
        error: "Expected execution ID wajib berupa angka positif.",
      };
    }
    body.expected_execution_id = input.expectedExecutionId;
  }

  if (input.mode === "BATCH") {
    const batchSize = input.batchSize ?? 0;
    const batchTable = input.batchTable?.trim() ?? "";
    const batchAttribute = input.batchAttribute?.trim() ?? "";
    if (
      !Number.isSafeInteger(batchSize) ||
      batchSize <= 0 ||
      batchSize > 10000
    ) {
      return {
        ok: false,
        error: "Batch size harus berupa angka antara 1 sampai 10000.",
      };
    }
    if (!batchTable || !batchAttribute) {
      return {
        ok: false,
        error: "Batch table dan batch attribute wajib diisi untuk mode BATCH.",
      };
    }
    body.batch_size = batchSize;
    body.batch_table = batchTable;
    body.batch_attribute = batchAttribute;
  }

  return { ok: true, body };
}

export function parseJobIdFromLocation(location: string | null): number | null {
  if (!location) return null;
  const match = /[?&]job_id=(\d+)/.exec(location);
  if (!match) return null;
  const parsed = Number.parseInt(match[1], 10);
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : null;
}

export function isTerminalJobStatus(status: MigrationJobStatus): boolean {
  return status !== "QUEUED" && status !== "RUNNING";
}

function normalizeExecutionRef(raw: unknown): MigrationExecutionRef | null {
  if (!raw || typeof raw !== "object") return null;
  const record = raw as Record<string, unknown>;
  const executionId = Number(record.execution_id ?? 0);
  const migrationId = Number(record.migration_id ?? 0);
  const revision = Number(record.revision ?? 0);
  if (executionId <= 0 || migrationId <= 0) return null;
  return { executionId, migrationId, revision };
}

export function normalizePosition(raw: unknown): MigrationPosition {
  if (!raw || typeof raw !== "object") {
    return { activeHead: null, nextForward: null };
  }
  const record = raw as Record<string, unknown>;
  return {
    activeHead: normalizeExecutionRef(record.active_head),
    nextForward: normalizeExecutionRef(record.next_forward),
  };
}

export function normalizeReview(raw: unknown): MigrationReview {
  const r = (raw && typeof raw === "object" ? raw : {}) as Record<
    string,
    unknown
  >;
  const rawGroups = Array.isArray(r.groups) ? r.groups : [];
  const groups: MigrationReviewGroup[] = rawGroups.map((item) => {
    const g = (item && typeof item === "object" ? item : {}) as Record<
      string,
      unknown
    >;
    return {
      groupId: Number(g.group_id ?? 0),
      status: (g.status as MigrationReviewGroupStatus) ?? "PENDING",
      decidedBy: typeof g.decided_by === "number" ? g.decided_by : null,
      decidedAt: typeof g.decided_at === "string" ? g.decided_at : null,
    };
  });

  return {
    reviewId: Number(r.review_id ?? 0),
    targetDbId: Number(r.target_db_id ?? 0),
    migrationId: Number(r.migration_id ?? 0),
    status: (r.status as MigrationReviewStatus) ?? "PENDING_REVIEW",
    actorId: Number(r.actor_id ?? 0),
    action: (r.action as MigrationAction) ?? "APPLY",
    mode: (r.mode as MigrationExecutionMode) ?? "STANDARD",
    batchSize: Number(r.batch_size ?? 0),
    batchTable: typeof r.batch_table === "string" ? r.batch_table : undefined,
    batchAttribute:
      typeof r.batch_attribute === "string" ? r.batch_attribute : undefined,
    revision: Number(r.revision ?? 0),
    expectedExecutionId:
      typeof r.expected_execution_id === "number" && r.expected_execution_id > 0
        ? r.expected_execution_id
        : undefined,
    sqlSnapshot: typeof r.sql_snapshot === "string" ? r.sql_snapshot : "",
    groups,
  };
}

export function normalizeJob(raw: unknown): MigrationJob {
  const r = (raw && typeof raw === "object" ? raw : {}) as Record<
    string,
    unknown
  >;
  return {
    jobId: Number(r.job_id ?? 0),
    executionId: Number(r.execution_id ?? 0),
    migrationId: Number(r.migration_id ?? 0),
    targetDbId: Number(r.target_db_id ?? 0),
    action: (r.action as MigrationAction) ?? "APPLY",
    mode: (r.mode as MigrationExecutionMode) ?? "STANDARD",
    batchSize: Number(r.batch_size ?? 0),
    batchTable: typeof r.batch_table === "string" ? r.batch_table : undefined,
    batchAttribute:
      typeof r.batch_attribute === "string" ? r.batch_attribute : undefined,
    status: (r.status as MigrationJobStatus) ?? "QUEUED",
    attempts: Number(r.attempts ?? 0),
    committedBatches: Number(r.committed_batches ?? 0),
    committedRows: Number(r.committed_rows ?? 0),
    totalRows: Number(r.total_rows ?? 0),
    cursor: Number(r.cursor ?? 0),
    error: typeof r.error === "string" ? r.error : undefined,
    nextAttemptAt:
      typeof r.next_attempt_at === "string" ? r.next_attempt_at : undefined,
    createdAt: typeof r.created_at === "string" ? r.created_at : "",
    updatedAt: typeof r.updated_at === "string" ? r.updated_at : "",
  };
}
