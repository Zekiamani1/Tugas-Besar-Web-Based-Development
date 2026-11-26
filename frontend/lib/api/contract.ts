import type {
  AccessLevel,
  ActionResult,
  Group,
  GroupDetail,
  MigrationDefinition,
  MigrationDraftInput,
  MigrationJob,
  MigrationListState,
  MigrationPosition,
  MigrationRecord,
  MigrationReview,
  MigrationReviewDecision,
  MigrationSubmissionInput,
  RegisterTargetDBInput,
  TableDataResult,
  TableMetadata,
  TargetDBInfo,
  UserSummary,
} from "@/lib/types";

export interface AuthService {
  login(username: string, password: string): Promise<ActionResult<UserSummary>>;
  logout(): Promise<void>;
}

export interface DatabaseService {
  getTables(dbId: number, schema: string): Promise<string[]>;
  getTableMetadata(
    dbId: number,
    schema: string,
    table: string
  ): Promise<TableMetadata | null>;
  getTableData(
    dbId: number,
    schema: string,
    table: string,
    page?: number,
    pageSize?: number
  ): Promise<TableDataResult>;
}

export interface MigrationService {
  listState(
    dbId: number,
    options?: { hideRollback?: boolean }
  ): Promise<ActionResult<MigrationListState>>;
  getPosition(dbId: number): Promise<ActionResult<MigrationPosition>>;
  getDefinition(
    dbId: number,
    migrationId: number
  ): Promise<ActionResult<MigrationDefinition>>;
  createDraft(
    dbId: number,
    input: MigrationDraftInput
  ): Promise<ActionResult<MigrationRecord>>;
  updateDraft(
    dbId: number,
    migrationId: number,
    input: MigrationDraftInput
  ): Promise<ActionResult<MigrationRecord>>;
  explainDraft(dbId: number, migrationId: number): Promise<ActionResult<string>>;
  submitMigration(
    dbId: number,
    migrationId: number,
    input: MigrationSubmissionInput
  ): Promise<ActionResult<{ reviewId: number }>>;
  listPendingReviews(dbId: number): Promise<ActionResult<MigrationReview[]>>;
  getReview(
    dbId: number,
    migrationId: number,
    reviewId: number
  ): Promise<ActionResult<MigrationReview>>;
  decideReview(
    dbId: number,
    migrationId: number,
    reviewId: number,
    groupId: number,
    decision: MigrationReviewDecision
  ): Promise<ActionResult<{ jobId: number | null; location: string | null }>>;
  getJob(
    dbId: number,
    migrationId: number,
    jobId: number
  ): Promise<ActionResult<MigrationJob>>;
}

export interface AdminService {
  listUsers(): Promise<UserSummary[]>;
  listGroups(): Promise<GroupDetail[]>;
  listDatabases(): Promise<TargetDBInfo[]>;
  registerUser(
    username: string,
    password: string
  ): Promise<ActionResult<UserSummary>>;
  createGroup(groupName: string): Promise<ActionResult<Group>>;
  assignUserToGroup(userId: number, groupId: number): Promise<ActionResult>;
  setGroupLeader(groupId: number, userId: number): Promise<ActionResult>;
  setGroupDbAccess(
    groupId: number,
    targetDbId: number,
    accessLevel: AccessLevel
  ): Promise<ActionResult>;
  registerDatabase(
    input: RegisterTargetDBInput
  ): Promise<ActionResult<TargetDBInfo>>;
}

export interface AppServices {
  auth: AuthService;
  databases: DatabaseService;
  migrations: MigrationService;
  admin: AdminService;
}
