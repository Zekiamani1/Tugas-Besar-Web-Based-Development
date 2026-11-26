import { EMPTY_TABLE_DATA } from "@/features/databases/logic";
import type {
  DatabaseExplorerInitialData,
  ExplorerTab,
  TableMetadata,
  TableRow,
} from "@/lib/types";
import { getApiBaseUrl, requestJson } from "../http";
import { getServerSessionContext } from "./session";

export async function loadDatabaseExplorerData(
  dbId: number,
  opts: {
    schema?: string;
    table?: string;
    tab: ExplorerTab;
    cookieHeader?: string;
    baseUrl?: string;
  }
): Promise<DatabaseExplorerInitialData> {
  const normalizedBaseUrl = (opts.baseUrl ?? getApiBaseUrl()).replace(
    /\/+$/,
    ""
  );
  const resolvedCookie =
    opts.cookieHeader ?? (await getServerSessionContext()).cookieHeader;

  const schemasRes = await requestJson<{ schemas?: string[] }>(
    `${normalizedBaseUrl}/databases/${dbId}/schemas`,
    {
      cache: "no-store",
      cookieHeader: resolvedCookie,
    }
  );
  const schemas = Array.isArray(schemasRes.schemas) ? schemasRes.schemas : [];
  const activeSchema =
    opts.schema && schemas.includes(opts.schema)
      ? opts.schema
      : (schemas[0] ?? "");

  if (!activeSchema) {
    return {
      schemas,
      activeSchema: "",
      tables: [],
      activeTable: "",
      activeTab: opts.tab,
      metadata: null,
      tableData: EMPTY_TABLE_DATA,
    };
  }

  const tablesRes = await requestJson<{ tables?: string[] }>(
    `${normalizedBaseUrl}/databases/${dbId}/schemas/${encodeURIComponent(activeSchema)}/tables`,
    {
      cache: "no-store",
      cookieHeader: resolvedCookie,
    }
  );
  const tables = Array.isArray(tablesRes.tables) ? tablesRes.tables : [];
  const activeTable =
    opts.table && tables.includes(opts.table)
      ? opts.table
      : (tables[0] ?? "");

  if (!activeTable) {
    return {
      schemas,
      activeSchema,
      tables,
      activeTable: "",
      activeTab: opts.tab,
      metadata: null,
      tableData: EMPTY_TABLE_DATA,
    };
  }

  const encodedSchema = encodeURIComponent(activeSchema);
  const encodedTable = encodeURIComponent(activeTable);
  const [metadataRes, dataRes] = await Promise.all([
    requestJson<{ metadata?: TableMetadata | null }>(
      `${normalizedBaseUrl}/databases/${dbId}/schemas/${encodedSchema}/tables/${encodedTable}`,
      {
        cache: "no-store",
        cookieHeader: resolvedCookie,
      }
    ),
    requestJson<{ columns?: string[]; rows?: TableRow[] }>(
      `${normalizedBaseUrl}/databases/${dbId}/schemas/${encodedSchema}/tables/${encodedTable}/data?limit=11&offset=0`,
      {
        cache: "no-store",
        cookieHeader: resolvedCookie,
      }
    ),
  ]);

  const rawRows = Array.isArray(dataRes.rows) ? dataRes.rows : [];
  const hasMore = rawRows.length > 10;
  const rows = hasMore ? rawRows.slice(0, 10) : rawRows;
  const columns = Array.isArray(dataRes.columns)
    ? dataRes.columns
    : rows.length > 0
      ? Object.keys(rows[0]).sort()
      : [];

  return {
    schemas,
    activeSchema,
    tables,
    activeTable,
    activeTab: opts.tab,
    metadata: metadataRes.metadata ?? null,
    tableData: {
      columns,
      rows,
      totalRows: rawRows.length,
      page: 1,
      pageSize: 10,
      totalPages: hasMore ? 2 : 1,
    },
  };
}
