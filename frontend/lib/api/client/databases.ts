import type { TableMetadata, TableRow } from "@/lib/types";
import type { DatabaseService } from "../contract";
import { requestJson } from "../http";

export const databaseService: DatabaseService = {
  async getTables(dbId, schema) {
    const res = await requestJson<{ tables?: unknown }>(
      `/databases/${dbId}/schemas/${encodeURIComponent(schema)}/tables`
    );
    return Array.isArray(res.tables)
      ? res.tables.filter((t): t is string => typeof t === "string")
      : [];
  },

  async getTableMetadata(dbId, schema, table) {
    const res = await requestJson<{ metadata?: TableMetadata | null }>(
      `/databases/${dbId}/schemas/${encodeURIComponent(schema)}/tables/${encodeURIComponent(table)}`
    );
    return res.metadata ?? null;
  },

  async getTableData(dbId, schema, table, page = 1, pageSize = 10) {
    const safePage = Number.isSafeInteger(page) && page > 0 ? page : 1;
    const safePageSize =
      Number.isSafeInteger(pageSize) && pageSize > 0 ? pageSize : 10;
    const offset = (safePage - 1) * safePageSize;
    const res = await requestJson<{ columns?: string[]; rows?: TableRow[] }>(
      `/databases/${dbId}/schemas/${encodeURIComponent(schema)}/tables/${encodeURIComponent(table)}/data?limit=${safePageSize + 1}&offset=${offset}`
    );

    const rawRows = Array.isArray(res.rows) ? res.rows : [];
    const hasMore = rawRows.length > safePageSize;
    const rows = hasMore ? rawRows.slice(0, safePageSize) : rawRows;
    const columns = Array.isArray(res.columns)
      ? res.columns
      : rows.length > 0
        ? Object.keys(rows[0]).sort()
        : [];

    return {
      columns,
      rows,
      totalRows: (safePage - 1) * safePageSize + rawRows.length,
      page: safePage,
      pageSize: safePageSize,
      totalPages: hasMore ? safePage + 1 : Math.max(1, safePage),
    };
  },
};
