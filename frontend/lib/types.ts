export type AccessLevel = "VIEW_ONLY" | "VIEW_EDIT";

export type MigrationStatus =
  | "DRAFT"
  | "APPLIED"
  | "FAILED"
  | "RUNNING"
  | "UNCERTAIN"
  | "PARTIAL"
  | "PARTIALLY_REVERTED"
  | "COMPENSATED";

export type MigrationAction = "APPLY" | "ROLLBACK" | "FORWARD";

export type MigrationExecutionMode = "STANDARD" | "BATCH";

export type MigrationReviewStatus = "PENDING_REVIEW" | "APPROVED" | "REJECTED";

export type MigrationReviewGroupStatus = "PENDING" | "APPROVED" | "REJECTED";

export type MigrationReviewDecision = "APPROVED" | "REJECTED";

export type MigrationJobStatus =
  | "QUEUED"
  | "RUNNING"
  | "SUCCEEDED"
  | "FAILED"
  | "PARTIAL"
  | "PARTIALLY_REVERTED"
  | "UNCERTAIN";

export type BadgeStatus =
  | MigrationStatus
  | MigrationReviewStatus
  | MigrationReviewGroupStatus
  | MigrationJobStatus;

export type MigrationTab = "migrations" | "reviews";

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
  leaderId?: number | null;
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
  executionId?: number;
  action?: MigrationAction;
  revision?: number;
  revertsExecutionId?: number | null;
  targetDbId: number;
  version: number;
  sqlQuery: string;
  executedBy: string;
  status: MigrationStatus;
  executedAt: string;
  finishedAt?: string | null;
}

export interface MigrationDraftInput {
  sqlUp: string;
  sqlDown: string;
  irreversible: boolean;
  irreversibleReason: string;
}

export interface MigrationDefinition {
  migrationId: number;
  targetDbId: number;
  revision: number;
  sqlUp: string;
  sqlDown: string;
  irreversible: boolean;
  irreversibleReason: string;
}

export interface MigrationExecutionRef {
  executionId: number;
  migrationId: number;
  revision: number;
}

export interface MigrationPosition {
  activeHead: MigrationExecutionRef | null;
  nextForward: MigrationExecutionRef | null;
}

export interface MigrationSubmissionInput {
  action: MigrationAction;
  mode: MigrationExecutionMode;
  revision?: number;
  expectedExecutionId?: number;
  batchSize?: number;
  batchTable?: string;
  batchAttribute?: string;
}

export interface MigrationReviewGroup {
  groupId: number;
  status: MigrationReviewGroupStatus;
  decidedBy: number | null;
  decidedAt: string | null;
}

export interface MigrationReview {
  reviewId: number;
  targetDbId: number;
  migrationId: number;
  status: MigrationReviewStatus;
  actorId: number;
  action: MigrationAction;
  mode: MigrationExecutionMode;
  batchSize: number;
  batchTable?: string;
  batchAttribute?: string;
  revision: number;
  expectedExecutionId?: number;
  sqlSnapshot: string;
  groups: MigrationReviewGroup[];
}

export interface MigrationJob {
  jobId: number;
  executionId: number;
  migrationId: number;
  targetDbId: number;
  action: MigrationAction;
  mode: MigrationExecutionMode;
  batchSize: number;
  batchTable?: string;
  batchAttribute?: string;
  status: MigrationJobStatus;
  attempts: number;
  committedBatches: number;
  committedRows: number;
  totalRows: number;
  cursor: number;
  error?: string;
  nextAttemptAt?: string;
  createdAt: string;
  updatedAt: string;
}

export interface MigrationListState {
  canEdit: boolean;
  drafts: MigrationRecord[];
  history: MigrationRecord[];
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
