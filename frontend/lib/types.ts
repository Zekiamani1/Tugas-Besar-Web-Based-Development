export type AccessLevel = "VIEW_ONLY" | "VIEW_EDIT";

export type MigrationStatus = "DRAFT" | "APPLIED" | "FAILED";

export type DBType = "postgres" | "mysql" | "sqlite";

export interface UserSummary {
  id: number;
  username: string;
  isAdmin: boolean;
}

export interface Group {
  id: number;
  name: string;
}

export interface GroupMemberDisplay {
  id: number;
  username: string;
}

export interface GroupDatabaseAccessDisplay {
  targetDbId: number;
  databaseName: string;
  accessLevel: AccessLevel;
}

export interface GroupDetail {
  id: number;
  name: string;
  members: GroupMemberDisplay[];
  databaseAccess: GroupDatabaseAccessDisplay[];
}

export interface TargetDBInfo {
  id: number;
  name: string;
  dbType: DBType;
  host: string;
  port: number;
  user?: string;
  dbName: string;
}

export interface RegisterTargetDBInput {
  name: string;
  dbType: string;
  host: string;
  port: string;
  user: string;
  password: string;
  dbName: string;
}

export interface ColumnInfo {
  name: string;
  dataType: string;
  isNullable: boolean;
}

export interface ConstraintInfo {
  name: string;
  type: string;
}

export interface IndexInfo {
  name: string;
  isUnique: boolean;
}

export interface TableMetadata {
  schema: string;
  tableName: string;
  columns: ColumnInfo[];
  constraints: ConstraintInfo[];
  indexes: IndexInfo[];
}

export type TableCellValue = string | number | boolean | null;

export type TableRow = Record<string, TableCellValue>;

export interface TableDataResult {
  columns: string[];
  rows: TableRow[];
  totalRows: number;
  page: number;
  pageSize: number;
  totalPages: number;
}

export interface MigrationRecord {
  id: number;
  targetDbId: number;
  version: number;
  sqlQuery: string;
  executedBy: string;
  status: MigrationStatus;
  executedAt: string;
}

export type ExplorerTab = "structure" | "data";

export type AdminTab = "manage" | "users" | "groups" | "databases";

export interface DatabaseExplorerInitialData {
  schemas: string[];
  activeSchema: string;
  tables: string[];
  activeTable: string;
  activeTab: ExplorerTab;
  metadata: TableMetadata | null;
  tableData: TableDataResult;
}

export type ActionResult<T = void> =
  | { ok: true; message?: string; data?: T }
  | { ok: false; error: string; data?: T };
