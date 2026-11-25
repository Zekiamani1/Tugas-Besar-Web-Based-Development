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
  async createDraft(dbId, sqlQuery) {
    if (!sqlQuery.trim()) {
      return { ok: false, error: "SQL query wajib diisi." };
    }
    try {
      const res = await requestJson<{ id: number }>(
        `/databases/${dbId}/migrations`,
        {
          method: "POST",
          body: JSON.stringify({ sql_query: sqlQuery }),
        }
      );
      const detail = await fetchMigrationDetail(dbId, res.id);
      return {
        ok: true,
        data: detail ?? {
          id: res.id,
          targetDbId: dbId,
          version: 0,
          sqlQuery,
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

  async updateDraft(dbId, migrationId, sqlQuery) {
    if (!sqlQuery.trim()) {
      return { ok: false, error: "SQL query wajib diisi." };
    }
    try {
      const res = await requestJson<{ message?: string }>(
        `/databases/${dbId}/migrations/${migrationId}`,
        {
          method: "PUT",
          body: JSON.stringify({ sql_query: sqlQuery }),
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
          sqlQuery,
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

  async executeDraft(dbId, migrationId) {
    try {
      const res = await requestJson<{ migration?: MigrationRecord }>(
        `/databases/${dbId}/migrations/${migrationId}/execute`,
        { method: "POST" }
      );
      return {
        ok: true,
        message: "Migration berhasil dijalankan.",
        data: res.migration,
      };
    } catch (err) {
      const persisted = await fetchMigrationDetail(dbId, migrationId);
      return {
        ok: false,
        error:
          err instanceof Error && err.message
            ? err.message
            : "Migration gagal dijalankan.",
        data: persisted,
      };
    }
  },
};
