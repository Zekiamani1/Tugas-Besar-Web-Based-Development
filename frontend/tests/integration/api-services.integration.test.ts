import { NextRequest } from "next/server";
import {
  afterAll,
  afterEach,
  beforeAll,
  describe,
  expect,
  it,
} from "vitest";
import { adminService } from "@/lib/api/client/admin";
import { authService } from "@/lib/api/client/auth";
import { databaseService } from "@/lib/api/client/databases";
import { migrationService } from "@/lib/api/client/migrations";
import { ApiError } from "@/lib/api/http";
import { loadAdminData } from "@/lib/api/server/admin";
import { loadDashboardData } from "@/lib/api/server/dashboard";
import { loadDatabaseExplorerData } from "@/lib/api/server/databases";
import {
  loadMigrationReviewData,
  loadMigrationsData,
} from "@/lib/api/server/migrations";
import { parseSessionClaims, SESSION_COOKIE } from "@/lib/session";
import { proxy } from "@/proxy";
import { createMswBackend, TEST_BACKEND_URL } from "./msw-backend";

const backend = createMswBackend();
let originalPublicApiUrl: string | undefined;
let originalInternalApiUrl: string | undefined;

beforeAll(() => {
  originalPublicApiUrl = process.env.NEXT_PUBLIC_API_URL;
  originalInternalApiUrl = process.env.INTERNAL_API_URL;
  process.env.NEXT_PUBLIC_API_URL = TEST_BACKEND_URL;
  process.env.INTERNAL_API_URL = TEST_BACKEND_URL;
  backend.server.listen({ onUnhandledRequest: "error" });
});

afterEach(() => {
  backend.server.resetHandlers();
  backend.reset();
});

afterAll(() => {
  backend.server.close();
  if (originalPublicApiUrl === undefined) {
    delete process.env.NEXT_PUBLIC_API_URL;
  } else {
    process.env.NEXT_PUBLIC_API_URL = originalPublicApiUrl;
  }
  if (originalInternalApiUrl === undefined) {
    delete process.env.INTERNAL_API_URL;
  } else {
    process.env.INTERNAL_API_URL = originalInternalApiUrl;
  }
});

function readDocumentCookie(name: string): string | undefined {
  for (const part of document.cookie.split(";")) {
    const trimmed = part.trim();
    if (trimmed.startsWith(`${name}=`)) {
      return trimmed.slice(name.length + 1);
    }
  }
  return undefined;
}

describe("Frontend API services & SSR loaders (direct Go CORS backend via MSW)", () => {
  it("runs authService, adminService, databaseService, and migrationService directly against MSW backend", async () => {
    const badLogin = await authService.login("admin", "wrong-pass");
    expect(badLogin).toEqual({
      ok: false,
      error: "Username atau password salah.",
    });

    const okLogin = await authService.login("admin", "admin123");
    expect(okLogin.ok).toBe(true);
    expect(okLogin.data).toMatchObject({ username: "admin", isAdmin: true });
    expect(parseSessionClaims(readDocumentCookie(SESSION_COOKIE))).toMatchObject({
      username: "admin",
      isAdmin: true,
      accessibleDbIds: [1, 2],
    });

    const regUser = await adminService.registerUser("andi_qa", "andi123");
    expect(regUser.ok).toBe(true);
    const users = await adminService.listUsers();
    const andi = users.find((u) => u.username === "andi_qa");
    expect(andi).toBeDefined();

    const regGroup = await adminService.createGroup("tim_qa");
    expect(regGroup.ok).toBe(true);
    const groupsBeforeAssign = await adminService.listGroups();
    const qaGroup = groupsBeforeAssign.find((g) => g.name === "tim_qa");
    expect(qaGroup).toBeDefined();

    const assigned = await adminService.assignUserToGroup(andi!.id, qaGroup!.id);
    expect(assigned.ok).toBe(true);

    const invalidLeader = await adminService.setGroupLeader(qaGroup!.id, 1);
    expect(invalidLeader.ok).toBe(false);

    const assignedLeader = await adminService.setGroupLeader(
      qaGroup!.id,
      andi!.id
    );
    expect(assignedLeader.ok).toBe(true);

    const invalidDb = await adminService.registerDatabase({
      name: "Broken Postgres",
      dbType: "postgres",
      host: "localhost",
      port: "abc",
      user: "postgres",
      password: "pw",
      dbName: "broken",
    });
    expect(invalidDb.ok).toBe(false);

    const regDb = await adminService.registerDatabase({
      name: "QA SQLite",
      dbType: "sqlite",
      host: "",
      port: "",
      user: "",
      password: "",
      dbName: "/data/qa.db",
    });
    expect(regDb.ok).toBe(true);
    const dbs = await adminService.listDatabases();
    const qaDb = dbs.find((db) => db.name === "QA SQLite");
    expect(qaDb).toBeDefined();
    expect(qaDb?.user).toBeUndefined();

    const permRes = await adminService.setGroupDbAccess(
      qaGroup!.id,
      qaDb!.id,
      "VIEW_ONLY"
    );
    expect(permRes.ok).toBe(true);
    const groupsAfter = await adminService.listGroups();
    expect(
      groupsAfter.find((g) => g.id === qaGroup!.id)?.databaseAccess
    ).toEqual([
      {
        targetDbId: qaDb!.id,
        databaseName: "QA SQLite",
        accessLevel: "VIEW_ONLY",
      },
    ]);

    const tables = await databaseService.getTables(1, "public");
    expect(tables).toEqual(["produk", "pesanan"]);

    const meta = await databaseService.getTableMetadata(1, "public", "produk");
    expect(meta?.columns.map((c) => c.name)).toEqual(["id", "nama"]);

    const page1 = await databaseService.getTableData(1, "public", "produk", 1, 10);
    expect(page1.rows).toHaveLength(10);
    expect(page1.totalPages).toBe(2);

    const page2 = await databaseService.getTableData(1, "public", "produk", 2, 10);
    expect(page2.rows).toHaveLength(2);
    expect(page2.rows[0].nama).toBe("Standing Desk Mat");

    const created = await migrationService.createDraft(1, {
      sqlUp: "CREATE TABLE demo (id INT);",
      sqlDown: "DROP TABLE demo;",
      irreversible: false,
      irreversibleReason: "",
    });
    expect(created.ok).toBe(true);
    expect(created.data?.status).toBe("DRAFT");
    expect(created.data?.executedAt).toBe("");

    const draftId = created.data!.id;
    const def1 = await migrationService.getDefinition(1, draftId);
    expect(def1.ok).toBe(true);
    expect(def1.data?.revision).toBe(1);

    const updated = await migrationService.updateDraft(1, draftId, {
      sqlUp: "CREATE TABLE demo (id INT PRIMARY KEY);",
      sqlDown: "DROP TABLE demo;",
      irreversible: false,
      irreversibleReason: "",
    });
    expect(updated.ok).toBe(true);

    const def2 = await migrationService.getDefinition(1, draftId);
    expect(def2.ok).toBe(true);
    expect(def2.data?.revision).toBe(2);

    const explained = await migrationService.explainDraft(1, draftId);
    expect(explained.ok).toBe(true);
    expect(explained.data).toContain("Seq Scan on public.produk");

    const submitted = await migrationService.submitMigration(1, draftId, {
      action: "APPLY",
      mode: "STANDARD",
      revision: 2,
    });
    expect(submitted.ok).toBe(true);
    const applyReviewId = submitted.data!.reviewId;

    await authService.login("budi_developer", "budi123");
    const budiPending = await migrationService.listPendingReviews(1);
    expect(budiPending.ok).toBe(true);
    expect(budiPending.data?.some((r) => r.reviewId === applyReviewId)).toBe(
      true
    );
    const budiDecision = await migrationService.decideReview(
      1,
      draftId,
      applyReviewId,
      1,
      "APPROVED"
    );
    expect(budiDecision.ok).toBe(true);
    expect(budiDecision.data?.jobId).toBeNull();

    await authService.login("siti_analyst", "siti123");
    const sitiDecision = await migrationService.decideReview(
      1,
      draftId,
      applyReviewId,
      2,
      "APPROVED"
    );
    expect(sitiDecision.ok).toBe(true);
    const applyJobId = sitiDecision.data!.jobId!;
    expect(applyJobId).toBeGreaterThan(0);

    const applyJob = await migrationService.getJob(1, draftId, applyJobId);
    expect(applyJob.ok).toBe(true);
    expect(applyJob.data?.status).toBe("SUCCEEDED");
    expect(applyJob.data?.executionId).toBeGreaterThan(1);

    const posAfterApply = await migrationService.getPosition(1);
    expect(posAfterApply.ok).toBe(true);
    expect(posAfterApply.data?.activeHead).toMatchObject({
      migrationId: draftId,
      executionId: applyJob.data!.executionId,
    });

    await authService.login("budi_developer", "budi123");
    const rollbackSubmit = await migrationService.submitMigration(1, draftId, {
      action: "ROLLBACK",
      mode: "STANDARD",
      expectedExecutionId: applyJob.data!.executionId,
    });
    expect(rollbackSubmit.ok).toBe(true);
    const rollbackReviewId = rollbackSubmit.data!.reviewId;
    expect(
      (
        await migrationService.decideReview(
          1,
          draftId,
          rollbackReviewId,
          1,
          "APPROVED"
        )
      ).ok
    ).toBe(true);

    await authService.login("siti_analyst", "siti123");
    const rollbackSiti = await migrationService.decideReview(
      1,
      draftId,
      rollbackReviewId,
      2,
      "APPROVED"
    );
    expect(rollbackSiti.ok).toBe(true);

    const fullState = await migrationService.listState(1, {
      hideRollback: false,
    });
    const hiddenState = await migrationService.listState(1, {
      hideRollback: true,
    });
    expect(fullState.data?.history).toHaveLength(3);
    expect(hiddenState.data?.history).toHaveLength(1);

    const posAfterRollback = await migrationService.getPosition(1);
    expect(posAfterRollback.data?.activeHead?.migrationId).toBe(1);
    expect(posAfterRollback.data?.nextForward?.migrationId).toBe(draftId);

    await authService.login("admin", "admin123");
    const failDraft = await migrationService.createDraft(1, {
      sqlUp: "FAIL BAD SQL;",
      sqlDown: "",
      irreversible: true,
      irreversibleReason: "Test failure flow",
    });
    expect(failDraft.ok).toBe(true);
    const failSubmit = await migrationService.submitMigration(
      1,
      failDraft.data!.id,
      { action: "APPLY", mode: "STANDARD", revision: 1 }
    );
    expect(failSubmit.ok).toBe(true);
    await authService.login("budi_developer", "budi123");
    await migrationService.decideReview(
      1,
      failDraft.data!.id,
      failSubmit.data!.reviewId,
      1,
      "APPROVED"
    );
    await authService.login("siti_analyst", "siti123");
    const failFinal = await migrationService.decideReview(
      1,
      failDraft.data!.id,
      failSubmit.data!.reviewId,
      2,
      "APPROVED"
    );
    const failJob = await migrationService.getJob(
      1,
      failDraft.data!.id,
      failFinal.data!.jobId!
    );
    expect(failJob.ok).toBe(true);
    expect(failJob.data?.status).toBe("FAILED");

    await authService.logout();
    expect(readDocumentCookie(SESSION_COOKIE)).toBeUndefined();
    expect(backend.getActiveSessionCookie()).toBe("");
  });

  it("syncs wbd_session cookie with proxy(), keeps rotated session_id working, and clears wbd_session on 401", async () => {
    const loginResult = await authService.login("siti_analyst", "siti123");
    expect(loginResult.ok).toBe(true);

    const rawClaims = readDocumentCookie(SESSION_COOKIE);
    expect(rawClaims).toBeTruthy();
    expect(parseSessionClaims(rawClaims)).toMatchObject({
      username: "siti_analyst",
      isAdmin: false,
      accessibleDbIds: [1],
    });

    const cookieHeader = `${SESSION_COOKIE}=${rawClaims}`;
    expect(
      proxy(
        new NextRequest("http://localhost:3000/databases/1", {
          headers: { cookie: cookieHeader },
        })
      ).status
    ).toBe(200);
    expect(
      proxy(
        new NextRequest("http://localhost:3000/databases/2", {
          headers: { cookie: cookieHeader },
        })
      ).headers.get("x-middleware-rewrite")
    ).toBe("http://localhost:3000/forbidden?reason=database");
    expect(
      proxy(
        new NextRequest("http://localhost:3000/admin", {
          headers: { cookie: cookieHeader },
        })
      ).headers.get("x-middleware-rewrite")
    ).toBe("http://localhost:3000/forbidden?reason=admin");

    const staleRedirectTarget = proxy(
      new NextRequest("http://localhost:3000/login?reason=session", {
        headers: { cookie: cookieHeader },
      })
    );
    expect(staleRedirectTarget.status).toBe(200);
    expect(staleRedirectTarget.headers.get("set-cookie")).toContain(
      `${SESSION_COOKIE}=; Path=/; Max-Age=0`
    );

    const activeToken = backend
      .getActiveSessionCookie()
      .replace(/^session_id=/, "");
    expect(activeToken).toBeTruthy();
    backend.scheduleSessionRotation(activeToken, "rotated-sess-999");

    const rotatedTables = await databaseService.getTables(1, "public");
    expect(rotatedTables).toEqual(["produk", "pesanan"]);
    expect(backend.getActiveSessionCookie()).toBe("session_id=rotated-sess-999");

    backend.revokeSession("rotated-sess-999");
    await expect(
      databaseService.getTables(1, "public")
    ).rejects.toMatchObject({ status: 401 });
    expect(readDocumentCookie(SESSION_COOKIE)).toBeUndefined();
  });

  it("loads SSR dashboard, explorer, migrations, and admin data and raises typed ApiError for 401/403", async () => {
    await expect(
      loadDashboardData("", TEST_BACKEND_URL)
    ).rejects.toBeInstanceOf(ApiError);

    await authService.login("siti_analyst", "siti123");

    const analystCookie = backend.getActiveSessionCookie();
    const dash = await loadDashboardData(analystCookie, TEST_BACKEND_URL);
    expect(dash.databases.map((d) => d.id)).toEqual([1]);

    await expect(
      loadAdminData(analystCookie, TEST_BACKEND_URL)
    ).rejects.toMatchObject({ status: 403 });

    const explorer = await loadDatabaseExplorerData(1, {
      cookieHeader: analystCookie,
      baseUrl: TEST_BACKEND_URL,
      tab: "structure",
    });
    expect(explorer.schemas).toEqual(["public", "analytics"]);
    expect(explorer.activeSchema).toBe("public");
    expect(explorer.activeTable).toBe("produk");
    expect(explorer.tableData.totalPages).toBe(2);

    const migs = await loadMigrationsData(1, {
      cookieHeader: analystCookie,
      baseUrl: TEST_BACKEND_URL,
    });
    expect(migs.canEdit).toBe(false);
    expect(migs.history).toHaveLength(1);
    expect(migs.position.activeHead?.migrationId).toBe(1);

    await expect(
      loadMigrationReviewData(1, 1, 999, {
        cookieHeader: analystCookie,
        baseUrl: TEST_BACKEND_URL,
      })
    ).rejects.toMatchObject({ status: 404 });

    await authService.login("budi_developer", "budi123");
    const reviewDraft = await migrationService.createDraft(1, {
      sqlUp: "CREATE TABLE review_ssr (id INT);",
      sqlDown: "DROP TABLE review_ssr;",
      irreversible: false,
      irreversibleReason: "",
    });
    const submitted = await migrationService.submitMigration(
      1,
      reviewDraft.data!.id,
      { action: "APPLY", mode: "STANDARD", revision: 1 }
    );
    const loadedReview = await loadMigrationReviewData(
      1,
      reviewDraft.data!.id,
      submitted.data!.reviewId,
      {
        cookieHeader: backend.getActiveSessionCookie(),
        baseUrl: TEST_BACKEND_URL,
      }
    );
    expect(loadedReview.review.reviewId).toBe(submitted.data!.reviewId);
    expect(loadedReview.review.status).toBe("PENDING_REVIEW");
  });
});
