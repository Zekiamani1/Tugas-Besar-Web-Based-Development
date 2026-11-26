import type {
  AccessLevel,
  ActionResult,
  Group,
  GroupDetail,
  MigrationRecord,
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
  createDraft(
    dbId: number,
    sqlQuery: string
  ): Promise<ActionResult<MigrationRecord>>;
  updateDraft(
    dbId: number,
    migrationId: number,
    sqlQuery: string
  ): Promise<ActionResult<MigrationRecord>>;
  explainDraft(dbId: number, migrationId: number): Promise<ActionResult<string>>;
  executeDraft(
    dbId: number,
    migrationId: number
  ): Promise<ActionResult<MigrationRecord>>;
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
