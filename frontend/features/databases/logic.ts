import type { DatabaseService } from "@/lib/api/contract";
import type { ExplorerTab, TableDataResult, TableMetadata } from "@/lib/types";

export const EMPTY_TABLE_DATA: TableDataResult = {
  columns: [],
  rows: [],
  totalRows: 0,
  page: 1,
  pageSize: 10,
  totalPages: 1,
};

export function buildExplorerUrl(
  dbId: number,
  schema: string,
  table: string,
  tab: ExplorerTab
): string {
  const params = new URLSearchParams();
  if (schema) params.set("schema", schema);
  if (table) params.set("table", table);
  params.set("tab", tab);
  return `/databases/${dbId}?${params.toString()}`;
}

export async function loadSchemaSelection(
  databaseService: DatabaseService,
  dbId: number,
  schema: string
): Promise<{
  tables: string[];
  activeTable: string;
  metadata: TableMetadata | null;
  tableData: TableDataResult;
}> {
  const tables = await databaseService.getTables(dbId, schema);
  const activeTable = tables[0] ?? "";
  if (!activeTable) {
    return {
      tables,
      activeTable: "",
      metadata: null,
      tableData: EMPTY_TABLE_DATA,
    };
  }

  const [metadata, tableData] = await Promise.all([
    databaseService.getTableMetadata(dbId, schema, activeTable),
    databaseService.getTableData(dbId, schema, activeTable, 1, 10),
  ]);

  return {
    tables,
    activeTable,
    metadata,
    tableData,
  };
}

export async function loadTableSelection(
  databaseService: DatabaseService,
  dbId: number,
  schema: string,
  table: string
): Promise<{
  metadata: TableMetadata | null;
  tableData: TableDataResult;
}> {
  const [metadata, tableData] = await Promise.all([
    databaseService.getTableMetadata(dbId, schema, table),
    databaseService.getTableData(dbId, schema, table, 1, 10),
  ]);

  return { metadata, tableData };
}
