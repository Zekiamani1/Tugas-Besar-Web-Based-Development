import {
  buildDraftRequestBody,
  buildSubmissionPayload,
  normalizeJob,
  normalizePosition,
  normalizeReview,
  parseJobIdFromLocation,
  sortMigrationHistory,
  validateDraftInput,
} from "@/features/migrations/logic";
import type { MigrationRecord } from "@/lib/types";
import type { MigrationService } from "../contract";
import { requestJson } from "../http";

async function fetchMigrationDetail(
  dbId: number,
  migrationId: number
): Promise<MigrationRecord | undefined> {
  try {
    const detailRes = await requestJson<{ migration?: MigrationRecord }>(
      `/databases/${dbId}/migrations/${migrationId}`
    );
    return detailRes.migration;
  } catch {
    return undefined;
  }
}

export const migrationService: MigrationService = {
  async listState(dbId, options) {
    const suffix = options?.hideRollback ? "?hide_rollback=true" : "";
    try {
      const res = await requestJson<{
        migrations?: MigrationRecord[];
        drafts?: MigrationRecord[];
        can_edit?: boolean;
      }>(`/databases/${dbId}/migrations${suffix}`);
      return {
        ok: true,
        data: {
          canEdit: Boolean(res.can_edit),
          drafts: Array.isArray(res.drafts) ? res.drafts : [],
          history: sortMigrationHistory(
            Array.isArray(res.migrations) ? res.migrations : []
          ),
        },
      };
    } catch (err) {
      return {
        ok: false,
        error:
          err instanceof Error && err.message
            ? err.message
            : "Daftar migrasi gagal dimuat.",
      };
    }
  },

  async getPosition(dbId) {
    try {
      const res = await requestJson<unknown>(
        `/databases/${dbId}/migrations/position`
      );
      return { ok: true, data: normalizePosition(res) };
    } catch (err) {
      return {
        ok: false,
        error:
          err instanceof Error && err.message
            ? err.message
            : "Posisi migrasi gagal dimuat.",
      };
    }
  },

  async getDefinition(dbId, migrationId) {
    try {
      const res = await requestJson<{
        migration_id?: number;
        target_db_id?: number;
        revision?: number;
        sql_up?: string;
        sql_down?: string;
        irreversible?: boolean;
        irreversible_reason?: string;
      }>(`/databases/${dbId}/migrations/${migrationId}/definition`);
      return {
        ok: true,
        data: {
          migrationId: res.migration_id ?? migrationId,
          targetDbId: res.target_db_id ?? dbId,
          revision: res.revision ?? 1,
          sqlUp: res.sql_up ?? "",
          sqlDown: res.sql_down ?? "",
          irreversible: Boolean(res.irreversible),
          irreversibleReason: res.irreversible_reason ?? "",
        },
      };
    } catch (err) {
      return {
        ok: false,
        error:
          err instanceof Error && err.message
            ? err.message
            : "Definisi migrasi gagal dimuat.",
      };
    }
  },

  async createDraft(dbId, input) {
    const validationError = validateDraftInput(input);
    if (validationError) {
      return { ok: false, error: validationError };
    }
    try {
      const res = await requestJson<{ id: number }>(
        `/databases/${dbId}/migrations`,
        {
          method: "POST",
          body: JSON.stringify(buildDraftRequestBody(input)),
        }
      );
      const detail = await fetchMigrationDetail(dbId, res.id);
      return {
        ok: true,
        data: detail ?? {
          id: res.id,
          targetDbId: dbId,
          version: 0,
          revision: 1,
          sqlQuery: input.sqlUp.trim(),
          executedBy: "",
          status: "DRAFT",
          executedAt: "",
        },
      };
    } catch (err) {
      return {
        ok: false,
        error:
          err instanceof Error && err.message
            ? err.message
            : "Draft tidak dapat dibuat.",
      };
    }
  },

  async updateDraft(dbId, migrationId, input) {
    const validationError = validateDraftInput(input);
    if (validationError) {
      return { ok: false, error: validationError };
    }
    try {
      const res = await requestJson<{ message?: string }>(
        `/databases/${dbId}/migrations/${migrationId}`,
        {
          method: "PUT",
          body: JSON.stringify(buildDraftRequestBody(input)),
        }
      );
      const detail = await fetchMigrationDetail(dbId, migrationId);
      return {
        ok: true,
        message: res.message ?? "Draft berhasil disimpan.",
        data: detail ?? {
          id: migrationId,
          targetDbId: dbId,
          version: 0,
          revision: 1,
          sqlQuery: input.sqlUp.trim(),
          executedBy: "",
          status: "DRAFT",
          executedAt: "",
        },
      };
    } catch (err) {
      return {
        ok: false,
        error:
          err instanceof Error && err.message
            ? err.message
            : "Draft tidak dapat disimpan.",
      };
    }
  },

  async explainDraft(dbId, migrationId) {
    try {
      const res = await requestJson<{ explain_plan?: string }>(
        `/databases/${dbId}/migrations/${migrationId}/explain`,
        { method: "POST" }
      );
      return {
        ok: true,
        data: res.explain_plan ?? "",
      };
    } catch (err) {
      return {
        ok: false,
        error:
          err instanceof Error && err.message
            ? err.message
            : "Query plan tidak dapat dibuat.",
      };
    }
  },

  async submitMigration(dbId, migrationId, input) {
    const built = buildSubmissionPayload(input);
    if (!built.ok) {
      return { ok: false, error: built.error };
    }
    try {
      const res = await requestJson<{ review_id?: string }>(
        `/databases/${dbId}/migrations/${migrationId}/submit`,
        {
          method: "POST",
          body: JSON.stringify(built.body),
        }
      );
      const reviewId = Number.parseInt(res?.review_id ?? "", 10);
      if (!Number.isSafeInteger(reviewId) || reviewId <= 0) {
        return { ok: false, error: "Review ID tidak valid." };
      }
      return {
        ok: true,
        message: `Pengajuan review #${reviewId} berhasil dibuat.`,
        data: { reviewId },
      };
    } catch (err) {
      return {
        ok: false,
        error:
          err instanceof Error && err.message
            ? err.message
            : "Pengajuan migrasi gagal dikirim.",
      };
    }
  },

  async listPendingReviews(dbId) {
    try {
      const res = await requestJson<unknown>(
        `/databases/${dbId}/migration-reviews`
      );
      return {
        ok: true,
        data: Array.isArray(res) ? res.map(normalizeReview) : [],
      };
    } catch (err) {
      return {
        ok: false,
        error:
          err instanceof Error && err.message
            ? err.message
            : "Daftar review gagal dimuat.",
      };
    }
  },

  async getReview(dbId, migrationId, reviewId) {
    try {
      const res = await requestJson<unknown>(
        `/databases/${dbId}/migrations/${migrationId}/reviews/${reviewId}`
      );
      return {
        ok: true,
        data: normalizeReview(res),
      };
    } catch (err) {
      return {
        ok: false,
        error:
          err instanceof Error && err.message
            ? err.message
            : "Detail review gagal dimuat.",
      };
    }
  },

  async decideReview(dbId, migrationId, reviewId, groupId, decision) {
    if (!Number.isSafeInteger(groupId) || groupId <= 0) {
      return { ok: false, error: "Group ID tidak valid." };
    }
    if (decision !== "APPROVED" && decision !== "REJECTED") {
      return { ok: false, error: "Keputusan review tidak valid." };
    }
    let location: string | null = null;
    try {
      await requestJson<null>(
        `/databases/${dbId}/migrations/${migrationId}/reviews/${reviewId}/decision`,
        {
          method: "POST",
          body: JSON.stringify({ group_id: groupId, decision }),
          onResponse: (response) => {
            location = response.headers.get("Location");
          },
        }
      );
      return {
        ok: true,
        message:
          decision === "APPROVED"
            ? "Persetujuan group berhasil disimpan."
            : "Penolakan group berhasil disimpan.",
        data: {
          jobId: parseJobIdFromLocation(location),
          location,
        },
      };
    } catch (err) {
      return {
        ok: false,
        error:
          err instanceof Error && err.message
            ? err.message
            : "Keputusan review tidak dapat disimpan.",
      };
    }
  },

  async getJob(dbId, migrationId, jobId) {
    if (!Number.isSafeInteger(jobId) || jobId <= 0) {
      return { ok: false, error: "Job ID tidak valid." };
    }
    try {
      const res = await requestJson<unknown>(
        `/databases/${dbId}/migrations/${migrationId}?job_id=${jobId}`
      );
      return {
        ok: true,
        data: normalizeJob(res),
      };
    } catch (err) {
      return {
        ok: false,
        error:
          err instanceof Error && err.message
            ? err.message
            : "Status job gagal dimuat.",
      };
    }
  },
};
