import { describe, expect, it, vi } from "vitest";
import {
  submitAssignMember,
  submitCreateGroup,
  submitRegisterDatabase,
  submitRegisterUser,
  submitSetGroupAccess,
  submitSetGroupLeader,
} from "@/features/admin/logic";
import {
  buildExplorerUrl,
  EMPTY_TABLE_DATA,
  loadSchemaSelection,
  loadTableSelection,
} from "@/features/databases/logic";
import {
  buildDraftRequestBody,
  buildSubmissionPayload,
  isTerminalJobStatus,
  normalizeJob,
  normalizePosition,
  normalizeReview,
  parseJobIdFromLocation,
  resolveSelectedMigration,
  sortMigrationHistory,
  validateDraftInput,
} from "@/features/migrations/logic";
import type {
  AdminService,
  DatabaseService,
} from "@/lib/api/contract";
import type {
  GroupDetail,
  MigrationRecord,
  TargetDBInfo,
  UserSummary,
} from "@/lib/types";

function createFakeDatabaseService(
  overrides?: Partial<DatabaseService>
): DatabaseService {
  return {
    getTables: vi.fn(async () => []),
    getTableMetadata: vi.fn(async () => null),
    getTableData: vi.fn(async () => EMPTY_TABLE_DATA),
    ...overrides,
  };
}

function createFakeAdminService(
  overrides?: Partial<AdminService>
): AdminService {
  return {
    listUsers: vi.fn(async () => []),
    listGroups: vi.fn(async () => []),
    listDatabases: vi.fn(async () => []),
    registerUser: vi.fn(),
    createGroup: vi.fn(),
    assignUserToGroup: vi.fn(),
    setGroupLeader: vi.fn(),
    setGroupDbAccess: vi.fn(),
    removeUserFromGroup: vi.fn(),
    revokeGroupDbAccess: vi.fn(),
    registerDatabase: vi.fn(),
    ...overrides,
  };
}

describe("Pure TypeScript feature logic", () => {
  describe("Database Explorer logic", () => {
    it("builds explorer URLs cleanly", () => {
      expect(buildExplorerUrl(1, "analytics", "daily_metrics", "data")).toBe(
        "/databases/1?schema=analytics&table=daily_metrics&tab=data"
      );
      expect(buildExplorerUrl(2, "", "", "structure")).toBe(
        "/databases/2?tab=structure"
      );
    });

    it("loads first table metadata and data when switching to non-empty schema", async () => {
      const metadata = {
        schema: "analytics",
        tableName: "daily_metrics",
        columns: [{ name: "metric_date", dataType: "date", isNullable: false }],
        constraints: [],
        indexes: [],
      };
      const tableData = {
        columns: ["metric_date"],
        rows: [{ metric_date: "2026-11-25" }],
        totalRows: 1,
        page: 1,
        pageSize: 10,
        totalPages: 1,
      };
      const dbService = createFakeDatabaseService({
        getTables: vi.fn(async () => ["daily_metrics", "empty_audit"]),
        getTableMetadata: vi.fn(async () => metadata),
        getTableData: vi.fn(async () => tableData),
      });

      const result = await loadSchemaSelection(dbService, 1, "analytics");
      expect(dbService.getTables).toHaveBeenCalledWith(1, "analytics");
      expect(dbService.getTableMetadata).toHaveBeenCalledWith(
        1,
        "analytics",
        "daily_metrics"
      );
      expect(dbService.getTableData).toHaveBeenCalledWith(
        1,
        "analytics",
        "daily_metrics",
        1,
        10
      );
      expect(result).toEqual({
        tables: ["daily_metrics", "empty_audit"],
        activeTable: "daily_metrics",
        metadata,
        tableData,
      });
    });

    it("returns empty metadata and rows when schema has no tables", async () => {
      const dbService = createFakeDatabaseService({
        getTables: vi.fn(async () => []),
      });

      const result = await loadSchemaSelection(dbService, 2, "empty_schema");
      expect(dbService.getTableMetadata).not.toHaveBeenCalled();
      expect(dbService.getTableData).not.toHaveBeenCalled();
      expect(result).toEqual({
        tables: [],
        activeTable: "",
        metadata: null,
        tableData: EMPTY_TABLE_DATA,
      });
    });

    it("loads table metadata and data in parallel in loadTableSelection", async () => {
      const dbService = createFakeDatabaseService();
      await loadTableSelection(dbService, 1, "public", "produk");
      expect(dbService.getTableMetadata).toHaveBeenCalledWith(
        1,
        "public",
        "produk"
      );
      expect(dbService.getTableData).toHaveBeenCalledWith(
        1,
        "public",
        "produk",
        1,
        10
      );
    });
  });

  describe("Migration Manager logic", () => {
    const history: MigrationRecord[] = [
      {
        id: 2,
        targetDbId: 1,
        version: 2,
        sqlQuery: "SELECT 2;",
        executedBy: "budi",
        status: "APPLIED",
        executedAt: "2026-11-25 10:00:00 UTC",
      },
      {
        id: 1,
        targetDbId: 1,
        version: 1,
        sqlQuery: "SELECT 1;",
        executedBy: "budi",
        status: "APPLIED",
        executedAt: "2026-11-24 10:00:00 UTC",
      },
    ];
    const drafts: MigrationRecord[] = [
      {
        id: 3,
        targetDbId: 1,
        version: 0,
        sqlQuery: "SELECT 3;",
        executedBy: "",
        status: "DRAFT",
        executedAt: "",
      },
    ];

    it("sorts history ascending by executedAt, executionId, and id", () => {
      const sameTime: MigrationRecord[] = [
        { ...history[1], executionId: 12 },
        { ...history[1], executionId: 10 },
      ];
      expect(sortMigrationHistory(history).map((m) => m.id)).toEqual([1, 2]);
      expect(
        sortMigrationHistory(sameTime).map((m) => m.executionId)
      ).toEqual([10, 12]);
    });

    it("resolves selected migration with role-appropriate fallbacks and picks latest execution from history", () => {
      expect(
        resolveSelectedMigration({
          canEdit: true,
          drafts,
          history,
          selectedId: null,
        })?.id
      ).toBe(3);

      expect(
        resolveSelectedMigration({
          canEdit: false,
          drafts,
          history,
          selectedId: 3,
        })?.id
      ).toBe(1);

      expect(
        resolveSelectedMigration({
          canEdit: false,
          drafts,
          history: sortMigrationHistory(history),
          selectedId: null,
        })?.id
      ).toBe(2);

      const multiExecHistory: MigrationRecord[] = [
        { ...history[1], executionId: 1, action: "APPLY", status: "APPLIED" },
        {
          ...history[1],
          executionId: 2,
          action: "ROLLBACK",
          status: "APPLIED",
        },
      ];
      expect(
        resolveSelectedMigration({
          canEdit: true,
          drafts: [],
          history: multiExecHistory,
          selectedId: 1,
        })?.executionId
      ).toBe(2);
    });

    it("validates structured drafts and builds clean request payloads", () => {
      expect(
        validateDraftInput({
          sqlUp: " ",
          sqlDown: "DROP TABLE x;",
          irreversible: false,
          irreversibleReason: "",
        })
      ).toBe("SQL Up wajib diisi.");

      expect(
        validateDraftInput({
          sqlUp: "CREATE TABLE x(id INT);",
          sqlDown: "",
          irreversible: false,
          irreversibleReason: "",
        })
      ).toBe("SQL Down wajib diisi jika migrasi dapat di-rollback.");

      expect(
        validateDraftInput({
          sqlUp: "DROP TABLE legacy;",
          sqlDown: "",
          irreversible: true,
          irreversibleReason: " ",
        })
      ).toBe("Alasan irreversible wajib diisi.");

      expect(
        buildDraftRequestBody({
          sqlUp: " CREATE TABLE x(id INT); ",
          sqlDown: " DROP TABLE x; ",
          irreversible: false,
          irreversibleReason: "should be stripped",
        })
      ).toEqual({
        sql_up: "CREATE TABLE x(id INT);",
        sql_down: "DROP TABLE x;",
        irreversible: false,
        irreversible_reason: "",
      });

      expect(
        buildDraftRequestBody({
          sqlUp: " DROP TABLE legacy; ",
          sqlDown: "ignored down",
          irreversible: true,
          irreversibleReason: " Destructive drop ",
        })
      ).toEqual({
        sql_up: "DROP TABLE legacy;",
        sql_down: "",
        irreversible: true,
        irreversible_reason: "Destructive drop",
      });
    });

    it("builds strict submission payloads for APPLY/ROLLBACK/FORWARD in STANDARD and BATCH modes", () => {
      expect(
        buildSubmissionPayload({
          action: "APPLY",
          mode: "STANDARD",
          revision: 2,
          batchSize: 500,
        })
      ).toEqual({
        ok: true,
        body: { action: "APPLY", mode: "STANDARD", revision: 2 },
      });

      expect(
        buildSubmissionPayload({
          action: "APPLY",
          mode: "STANDARD",
          revision: 0,
        })
      ).toEqual({
        ok: false,
        error: "Revision draft wajib berupa angka positif.",
      });

      expect(
        buildSubmissionPayload({
          action: "ROLLBACK",
          mode: "BATCH",
          expectedExecutionId: 9,
          batchSize: 500,
          batchTable: " public.orders ",
          batchAttribute: " id ",
        })
      ).toEqual({
        ok: true,
        body: {
          action: "ROLLBACK",
          mode: "BATCH",
          expected_execution_id: 9,
          batch_size: 500,
          batch_table: "public.orders",
          batch_attribute: "id",
        },
      });

      expect(
        buildSubmissionPayload({
          action: "FORWARD",
          mode: "BATCH",
          expectedExecutionId: 9,
          batchSize: 20000,
          batchTable: "public.orders",
          batchAttribute: "id",
        })
      ).toEqual({
        ok: false,
        error: "Batch size harus berupa angka antara 1 sampai 10000.",
      });
    });

    it("parses Location job IDs, detects terminal job statuses, and normalizes API DTOs", () => {
      expect(
        parseJobIdFromLocation("/databases/1/migrations/5?job_id=42")
      ).toBe(42);
      expect(parseJobIdFromLocation(null)).toBeNull();

      expect(isTerminalJobStatus("QUEUED")).toBe(false);
      expect(isTerminalJobStatus("RUNNING")).toBe(false);
      expect(isTerminalJobStatus("SUCCEEDED")).toBe(true);
      expect(isTerminalJobStatus("FAILED")).toBe(true);
      expect(isTerminalJobStatus("PARTIALLY_REVERTED")).toBe(true);

      expect(
        normalizePosition({
          active_head: { execution_id: 10, migration_id: 3, revision: 2 },
          next_forward: null,
        })
      ).toEqual({
        activeHead: { executionId: 10, migrationId: 3, revision: 2 },
        nextForward: null,
      });

      expect(
        normalizeReview({
          review_id: 7,
          target_db_id: 1,
          migration_id: 3,
          status: "PENDING_REVIEW",
          actor_id: 2,
          action: "APPLY",
          mode: "STANDARD",
          batch_size: 0,
          revision: 1,
          sql_snapshot: "SELECT 1;",
          groups: [
            {
              group_id: 1,
              status: "PENDING",
              decided_by: null,
              decided_at: null,
            },
          ],
        })
      ).toMatchObject({
        reviewId: 7,
        status: "PENDING_REVIEW",
        groups: [{ groupId: 1, status: "PENDING", decidedBy: null }],
      });

      expect(
        normalizeJob({
          job_id: 42,
          execution_id: 15,
          migration_id: 3,
          target_db_id: 1,
          action: "APPLY",
          mode: "BATCH",
          batch_size: 100,
          batch_table: "public.orders",
          batch_attribute: "id",
          status: "SUCCEEDED",
          attempts: 1,
          committed_batches: 3,
          committed_rows: 250,
          total_rows: 250,
          cursor: 250,
          created_at: "2026-11-26T00:00:00Z",
          updated_at: "2026-11-26T00:01:00Z",
        })
      ).toMatchObject({
        jobId: 42,
        status: "SUCCEEDED",
        committedBatches: 3,
        committedRows: 250,
      });
    });
  });

  describe("Admin Panel logic", () => {
    it("returns validation error without calling listUsers, then refreshes users on success", async () => {
      const users: UserSummary[] = [
        { id: 1, username: "admin", isAdmin: true },
        { id: 2, username: "andi_qa", isAdmin: false },
      ];
      const adminService = createFakeAdminService({
        registerUser: vi
          .fn<AdminService["registerUser"]>()
          .mockResolvedValueOnce({
            ok: false,
            error: "Periksa kembali data pengguna.",
          })
          .mockResolvedValueOnce({
            ok: true,
            message: "berhasil menambahkan andi_qa",
            data: users[1],
          }),
        listUsers: vi.fn(async () => users),
      });

      const failed = await submitRegisterUser(adminService, "", "");
      expect(failed).toEqual({
        ok: false,
        error: "Periksa kembali data pengguna.",
      });
      expect(adminService.listUsers).not.toHaveBeenCalled();

      const succeeded = await submitRegisterUser(
        adminService,
        "andi_qa",
        "andi123"
      );
      expect(succeeded).toEqual({
        ok: true,
        message: "berhasil menambahkan andi_qa",
        users,
      });
    });

    it("handles createGroup, assignMember, setGroupAccess, and registerDatabase with list refreshes", async () => {
      const updatedGroups: GroupDetail[] = [
        {
          id: 1,
          name: "tim_qa",
          members: [{ id: 2, username: "andi_qa" }],
          databaseAccess: [
            {
              targetDbId: 4,
              databaseName: "QA SQLite",
              accessLevel: "VIEW_ONLY",
            },
          ],
        },
      ];
      const updatedDatabases: TargetDBInfo[] = [
        {
          id: 4,
          name: "QA SQLite",
          dbType: "sqlite",
          host: "",
          port: 0,
          user: "",
          dbName: "/data/qa.db",
        },
      ];

      const adminService = createFakeAdminService({
        createGroup: vi.fn<AdminService["createGroup"]>(async () => ({
          ok: true,
          message: "Group dibuat.",
        })),
        assignUserToGroup: vi.fn<AdminService["assignUserToGroup"]>(
          async () => ({ ok: true, message: "Anggota ditambahkan." })
        ),
        setGroupDbAccess: vi.fn<AdminService["setGroupDbAccess"]>(async () => ({
          ok: true,
          message: "Akses disimpan.",
        })),
        setGroupLeader: vi.fn<AdminService["setGroupLeader"]>(async () => ({
          ok: true,
          message: "Leader disimpan.",
        })),
        registerDatabase: vi.fn<AdminService["registerDatabase"]>(async () => ({
          ok: true,
          message: "DB ditambahkan dengan id 4",
        })),
        listGroups: vi.fn(async () => updatedGroups),
        listDatabases: vi.fn(async () => updatedDatabases),
      });

      const groupRes = await submitCreateGroup(adminService, "tim_qa");
      expect(groupRes).toEqual({
        ok: true,
        message: "Group dibuat.",
        groups: updatedGroups,
      });

      const memberRes = await submitAssignMember(adminService, "2", "1");
      expect(adminService.assignUserToGroup).toHaveBeenCalledWith(2, 1);
      expect(memberRes).toEqual({
        ok: true,
        message: "Anggota ditambahkan.",
        groups: updatedGroups,
      });

      const accessRes = await submitSetGroupAccess(
        adminService,
        "1",
        "4",
        "VIEW_ONLY"
      );
      expect(adminService.setGroupDbAccess).toHaveBeenCalledWith(
        1,
        4,
        "VIEW_ONLY"
      );
      expect(accessRes).toEqual({
        ok: true,
        message: "Akses disimpan.",
        groups: updatedGroups,
      });

      const leaderRes = await submitSetGroupLeader(adminService, "1", "2");
      expect(adminService.setGroupLeader).toHaveBeenCalledWith(1, 2);
      expect(leaderRes).toEqual({
        ok: true,
        message: "Leader disimpan.",
        groups: updatedGroups,
      });

      const dbRes = await submitRegisterDatabase(adminService, {
        name: "QA SQLite",
        dbType: "sqlite",
        host: "",
        port: "",
        user: "",
        password: "",
        dbName: "/data/qa.db",
      });
      expect(dbRes).toEqual({
        ok: true,
        message: "DB ditambahkan dengan id 4",
        databases: updatedDatabases,
      });
    });
  });
});
