import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { SESSION_COOKIE } from "@/lib/session";
import type { AccessLevel, MigrationRecord } from "@/lib/types";

export const TEST_BACKEND_URL = "http://backend.test";

interface StoredUser {
  id: number;
  username: string;
  password: string;
  isAdmin: boolean;
}

interface StoredDB {
  id: number;
  name: string;
  dbType: string;
  host: string;
  port: number;
  user: string;
  password: string;
  dbName: string;
}

interface StoredGroup {
  id: number;
  name: string;
  leaderId: number | null;
  memberIds: number[];
  permissions: Array<{ targetDbId: number; accessLevel: AccessLevel }>;
}

interface StoredDefinition {
  migration_id: number;
  target_db_id: number;
  revision: number;
  sql_up: string;
  sql_down: string;
  irreversible: boolean;
  irreversible_reason: string;
}

interface StoredReviewGroup {
  group_id: number;
  status: "PENDING" | "APPROVED" | "REJECTED";
  decided_by: number | null;
  decided_at: string | null;
}

interface StoredReview {
  review_id: number;
  target_db_id: number;
  migration_id: number;
  status: "PENDING_REVIEW" | "APPROVED" | "REJECTED";
  actor_id: number;
  action: "APPLY" | "ROLLBACK" | "FORWARD";
  mode: "STANDARD" | "BATCH";
  batch_size: number;
  batch_table?: string;
  batch_attribute?: string;
  revision: number;
  expected_execution_id?: number;
  sql_snapshot: string;
  groups: StoredReviewGroup[];
}

interface StoredJob {
  job_id: number;
  execution_id: number;
  migration_id: number;
  target_db_id: number;
  action: "APPLY" | "ROLLBACK" | "FORWARD";
  mode: "STANDARD" | "BATCH";
  batch_size: number;
  batch_table?: string;
  batch_attribute?: string;
  status: "QUEUED" | "RUNNING" | "SUCCEEDED" | "FAILED";
  attempts: number;
  committed_batches: number;
  committed_rows: number;
  total_rows: number;
  cursor: number;
  error?: string;
  next_attempt_at: string;
  created_at: string;
  updated_at: string;
}

const PRODUK_ROWS = Array.from({ length: 12 }, (_, idx) => ({
  id: idx + 1,
  nama: idx === 10 ? "Standing Desk Mat" : `Produk #${idx + 1}`,
  harga: (idx + 1) * 100000,
  stok: idx + 5,
}));

function buildInitialState() {
  return {
    users: [
      { id: 1, username: "admin", password: "admin123", isAdmin: true },
      {
        id: 2,
        username: "budi_developer",
        password: "budi123",
        isAdmin: false,
      },
      { id: 3, username: "siti_analyst", password: "siti123", isAdmin: false },
    ] as StoredUser[],
    groups: [
      {
        id: 1,
        name: "tim_pengembang",
        leaderId: 2,
        memberIds: [2],
        permissions: [
          { targetDbId: 1, accessLevel: "VIEW_EDIT" },
          { targetDbId: 2, accessLevel: "VIEW_EDIT" },
        ],
      },
      {
        id: 2,
        name: "tim_analis",
        leaderId: 3,
        memberIds: [3],
        permissions: [{ targetDbId: 1, accessLevel: "VIEW_ONLY" }],
      },
    ] as StoredGroup[],
    databases: [
      {
        id: 1,
        name: "Toko Online Production",
        dbType: "postgres",
        host: "db-prod.internal",
        port: 5432,
        user: "postgres",
        password: "secret-prod-password",
        dbName: "db_toko_online",
      },
      {
        id: 2,
        name: "Analytics Mirror",
        dbType: "postgres",
        host: "db-analytics.internal",
        port: 5432,
        user: "postgres",
        password: "secret-analytics-password",
        dbName: "db_analytics",
      },
    ] as StoredDB[],
    definitions: [
      {
        migration_id: 1,
        target_db_id: 1,
        revision: 1,
        sql_up: "CREATE TABLE public.produk (id INT PRIMARY KEY);",
        sql_down: "DROP TABLE public.produk;",
        irreversible: false,
        irreversible_reason: "",
      },
    ] as StoredDefinition[],
    executions: [
      {
        id: 1,
        executionId: 1,
        action: "APPLY",
        revision: 1,
        targetDbId: 1,
        version: 1,
        sqlQuery: "CREATE TABLE public.produk (id INT PRIMARY KEY);",
        executedBy: "2",
        status: "APPLIED",
        executedAt: "2026-11-24T10:00:00Z",
      },
    ] as MigrationRecord[],
    reviews: [] as StoredReview[],
    jobs: [] as StoredJob[],
    sessions: new Map<string, number>(),
    pendingRotation: new Map<string, string>(),
    nextUserId: 4,
    nextGroupId: 3,
    nextDbId: 3,
    nextMigrationId: 2,
    nextExecutionId: 2,
    nextReviewId: 1,
    nextJobId: 1,
    nextVersion: 2,
    sessionCounter: 1,
  };
}

export function createMswBackend() {
  let state = buildInitialState();
  let activeSessionToken: string | null = null;

  function parseSessionToken(request: Request): string | null {
    const cookieHeader = request.headers.get("cookie") ?? "";
    for (const part of cookieHeader.split(";")) {
      const [name, ...rest] = part.trim().split("=");
      if (name === "session_id") {
        return rest.join("=") || null;
      }
    }
    return null;
  }

  function forbidden() {
    return new HttpResponse("forbidden\n", {
      status: 403,
      headers: { "Content-Type": "text/plain; charset=utf-8" },
    });
  }

  function withSessionRotation(
    headersInit: Record<string, string>,
    rotatedToken: string | null
  ): Headers {
    const headers = new Headers(headersInit);
    if (rotatedToken) {
      headers.set(
        "Set-Cookie",
        `session_id=${rotatedToken}; Path=/; HttpOnly; SameSite=Lax`
      );
    }
    return headers;
  }

  function requireAuth(request: Request) {
    const token = parseSessionToken(request);
    const userId = token ? state.sessions.get(token) : undefined;
    const user = userId ? state.users.find((u) => u.id === userId) : undefined;
    if (!token || !user) {
      return {
        error: new HttpResponse("unauthorized\n", {
          status: 401,
          headers: { "Content-Type": "text/plain; charset=utf-8" },
        }),
      } as const;
    }

    const rotatedToken = state.pendingRotation.get(token) ?? null;
    if (rotatedToken) {
      state.pendingRotation.delete(token);
      state.sessions.delete(token);
      state.sessions.set(rotatedToken, user.id);
      if (activeSessionToken === token) {
        activeSessionToken = rotatedToken;
      }
    }
    return { user, rotatedToken } as const;
  }

  function requireAdmin(request: Request) {
    const resolved = requireAuth(request);
    if ("error" in resolved) return { error: resolved.error } as const;
    if (!resolved.user.isAdmin) return { error: forbidden() } as const;
    return resolved;
  }

  function requireDb(
    request: Request,
    dbIdParam: unknown,
    level: AccessLevel
  ) {
    const resolved = requireAuth(request);
    if ("error" in resolved) return { error: resolved.error } as const;
    const dbId = Number.parseInt(String(dbIdParam), 10);
    if (!Number.isSafeInteger(dbId) || dbId <= 0) {
      return {
        error: new HttpResponse("invalid database ID\n", {
          status: 400,
          headers: { "Content-Type": "text/plain; charset=utf-8" },
        }),
      } as const;
    }

    let granted: AccessLevel | null = resolved.user.isAdmin ? "VIEW_EDIT" : null;
    if (!granted) {
      for (const group of state.groups) {
        if (!group.memberIds.includes(resolved.user.id)) continue;
        for (const perm of group.permissions) {
          if (perm.targetDbId !== dbId) continue;
          if (perm.accessLevel === "VIEW_EDIT") {
            granted = "VIEW_EDIT";
            break;
          }
          granted = "VIEW_ONLY";
        }
        if (granted === "VIEW_EDIT") break;
      }
    }

    if (!granted || (level === "VIEW_EDIT" && granted !== "VIEW_EDIT")) {
      return { error: forbidden() } as const;
    }
    return { ...resolved, dbId, granted } as const;
  }

  function summarizeDatabases() {
    return state.databases.map((db) => ({
      id: db.id,
      name: db.name,
      dbType: db.dbType,
      host: db.host,
      port: db.port,
      dbName: db.dbName,
    }));
  }

  const handlers = [
    http.get(`${TEST_BACKEND_URL}/health`, () =>
      HttpResponse.json({ status: "ok" }, { status: 200 })
    ),

    http.get(`${TEST_BACKEND_URL}/me`, ({ request }) => {
      const auth = requireAuth(request);
      if ("error" in auth) return auth.error;
      return HttpResponse.json(
        {
          user: {
            id: auth.user.id,
            username: auth.user.username,
            isAdmin: auth.user.isAdmin,
          },
        },
        { status: 200, headers: withSessionRotation({}, auth.rotatedToken) }
      );
    }),

    http.post(`${TEST_BACKEND_URL}/login`, async ({ request }) => {
      let body: Record<string, unknown>;
      try {
        body = (await request.json()) as Record<string, unknown>;
      } catch {
        return HttpResponse.json(
          { error: "invalid request body" },
          { status: 400 }
        );
      }
      const username = typeof body.username === "string" ? body.username : "";
      const password = typeof body.password === "string" ? body.password : "";
      const user = state.users.find(
        (u) => u.username === username && u.password === password
      );
      if (!user) {
        return HttpResponse.json(
          { error: "invalid credentials" },
          { status: 401 }
        );
      }
      const token = `sess-${user.id}-${state.sessionCounter++}`;
      state.sessions.set(token, user.id);
      activeSessionToken = token;
      return HttpResponse.json(
        { message: "login successful" },
        {
          status: 200,
          headers: {
            "Set-Cookie": `session_id=${token}; Path=/; HttpOnly; SameSite=Lax`,
          },
        }
      );
    }),

    http.post(`${TEST_BACKEND_URL}/logout`, ({ request }) => {
      const auth = requireAuth(request);
      if ("error" in auth) return auth.error;
      const token = parseSessionToken(request);
      if (token) {
        state.sessions.delete(token);
        if (activeSessionToken === token) {
          activeSessionToken = null;
        }
      }
      return HttpResponse.json(
        { message: "logged out" },
        {
          status: 200,
          headers: {
            "Set-Cookie":
              "session_id=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax",
          },
        }
      );
    }),

    http.get(`${TEST_BACKEND_URL}/databases`, ({ request }) => {
      const auth = requireAuth(request);
      if ("error" in auth) return auth.error;
      let accessible = summarizeDatabases();
      if (!auth.user.isAdmin) {
        const accessibleIds = new Set<number>();
        for (const group of state.groups) {
          if (!group.memberIds.includes(auth.user.id)) continue;
          for (const perm of group.permissions) {
            if (perm.targetDbId > 0) {
              accessibleIds.add(perm.targetDbId);
            }
          }
        }
        accessible = accessible.filter((db) => accessibleIds.has(db.id));
      }
      return HttpResponse.json(
        { databases: accessible },
        { status: 200, headers: withSessionRotation({}, auth.rotatedToken) }
      );
    }),

    http.get(`${TEST_BACKEND_URL}/admin/users`, ({ request }) => {
      const auth = requireAdmin(request);
      if ("error" in auth) return auth.error;
      return HttpResponse.json(
        {
          users: state.users.map((u) => ({
            id: u.id,
            username: u.username,
            isAdmin: u.isAdmin,
          })),
        },
        { status: 200, headers: withSessionRotation({}, auth.rotatedToken) }
      );
    }),

    http.post(`${TEST_BACKEND_URL}/admin/users`, async ({ request }) => {
      const auth = requireAdmin(request);
      if ("error" in auth) return auth.error;
      const body = (await request.json()) as Record<string, unknown>;
      const username =
        typeof body.username === "string" ? body.username.trim() : "";
      const password = typeof body.password === "string" ? body.password : "";
      if (!username || !password) {
        return HttpResponse.json(
          { error: "username and password required" },
          { status: 500 }
        );
      }
      if (state.users.some((u) => u.username === username)) {
        return HttpResponse.json(
          { error: "username already exists" },
          { status: 500 }
        );
      }
      state.users.push({
        id: state.nextUserId++,
        username,
        password,
        isAdmin: false,
      });
      return HttpResponse.json(
        { message: `berhasil menambahkan ${username}` },
        { status: 200, headers: withSessionRotation({}, auth.rotatedToken) }
      );
    }),

    http.get(`${TEST_BACKEND_URL}/admin/groups`, ({ request }) => {
      const auth = requireAdmin(request);
      if ("error" in auth) return auth.error;
      const groups = state.groups.map((g) => ({
        id: g.id,
        name: g.name,
        leaderId: g.leaderId,
        members: g.memberIds
          .map((uid) => state.users.find((u) => u.id === uid))
          .filter((u): u is StoredUser => Boolean(u))
          .map((u) => ({ id: u.id, username: u.username })),
        databaseAccess: g.permissions.map((p) => {
          const db = state.databases.find((d) => d.id === p.targetDbId);
          return {
            targetDbId: p.targetDbId,
            databaseName: db?.name ?? "",
            accessLevel: p.accessLevel,
          };
        }),
      }));
      return HttpResponse.json(
        { groups },
        { status: 200, headers: withSessionRotation({}, auth.rotatedToken) }
      );
    }),

    http.post(`${TEST_BACKEND_URL}/admin/groups`, async ({ request }) => {
      const auth = requireAdmin(request);
      if ("error" in auth) return auth.error;
      const body = (await request.json()) as Record<string, unknown>;
      const groupName =
        typeof body.group_name === "string" ? body.group_name.trim() : "";
      if (!groupName) {
        return HttpResponse.json(
          { error: "group_name required" },
          { status: 500 }
        );
      }
      state.groups.push({
        id: state.nextGroupId++,
        name: groupName,
        leaderId: null,
        memberIds: [],
        permissions: [],
      });
      return HttpResponse.json(
        { message: `berhasil menambahkan group ${groupName}` },
        { status: 200, headers: withSessionRotation({}, auth.rotatedToken) }
      );
    }),

    http.post(
      `${TEST_BACKEND_URL}/admin/groups/members`,
      async ({ request }) => {
        const auth = requireAdmin(request);
        if ("error" in auth) return auth.error;
        const body = (await request.json()) as Record<string, unknown>;
        const userId = Number(body.user_id);
        const groupId = Number(body.group_id);
        const group = state.groups.find((g) => g.id === groupId);
        const user = state.users.find((u) => u.id === userId);
        if (!group || !user) {
          return HttpResponse.json(
            { error: "user or group not found" },
            { status: 500 }
          );
        }
        if (!group.memberIds.includes(userId)) {
          group.memberIds.push(userId);
        }
        return HttpResponse.json(
          {
            message: `berhasil memasukkan user_id ${userId} ke group_id ${groupId}`,
          },
          { status: 200, headers: withSessionRotation({}, auth.rotatedToken) }
        );
      }
    ),

    http.post(
      `${TEST_BACKEND_URL}/admin/groups/leader`,
      async ({ request }) => {
        const auth = requireAdmin(request);
        if ("error" in auth) return auth.error;
        const body = (await request.json()) as Record<string, unknown>;
        const groupId = Number(body.group_id);
        const userId = Number(body.user_id);
        const group = state.groups.find((g) => g.id === groupId);
        if (
          !group ||
          groupId <= 0 ||
          userId <= 0 ||
          !group.memberIds.includes(userId)
        ) {
          return HttpResponse.json(
            { error: "unable to assign group leader" },
            { status: 400 }
          );
        }
        group.leaderId = userId;
        return new HttpResponse(null, {
          status: 204,
          headers: withSessionRotation({}, auth.rotatedToken),
        });
      }
    ),

    http.post(
      `${TEST_BACKEND_URL}/admin/groups/permissions`,
      async ({ request }) => {
        const auth = requireAdmin(request);
        if ("error" in auth) return auth.error;
        const body = (await request.json()) as Record<string, unknown>;
        const groupId = Number(body.group_id);
        const targetDbId = Number(body.target_db_id);
        const accessLevel =
          body.access_level === "VIEW_EDIT" ? "VIEW_EDIT" : "VIEW_ONLY";
        const group = state.groups.find((g) => g.id === groupId);
        if (!group) {
          return HttpResponse.json(
            { error: "group not found" },
            { status: 500 }
          );
        }
        const existing = group.permissions.find(
          (p) => p.targetDbId === targetDbId
        );
        if (existing) {
          existing.accessLevel = accessLevel;
        } else {
          group.permissions.push({ targetDbId, accessLevel });
        }
        return HttpResponse.json(
          {
            message: `berhasil meng-set hak akses group_id ${groupId} pada target_db_id ${targetDbId} menjadi ${accessLevel}`,
          },
          { status: 200, headers: withSessionRotation({}, auth.rotatedToken) }
        );
      }
    ),

    http.get(`${TEST_BACKEND_URL}/admin/databases`, ({ request }) => {
      const auth = requireAdmin(request);
      if ("error" in auth) return auth.error;
      return HttpResponse.json(
        { databases: summarizeDatabases() },
        { status: 200, headers: withSessionRotation({}, auth.rotatedToken) }
      );
    }),

    http.post(`${TEST_BACKEND_URL}/admin/databases`, async ({ request }) => {
      const auth = requireAdmin(request);
      if ("error" in auth) return auth.error;
      const body = (await request.json()) as Record<string, unknown>;
      const name = typeof body.name === "string" ? body.name : "";
      const dbType = typeof body.dbType === "string" ? body.dbType : "";
      const host = typeof body.host === "string" ? body.host : "";
      const port = typeof body.port === "number" ? body.port : 0;
      const user = typeof body.user === "string" ? body.user : "";
      const password = typeof body.password === "string" ? body.password : "";
      const dbName = typeof body.dbName === "string" ? body.dbName : "";

      if (!name || !dbType || !dbName) {
        return HttpResponse.json(
          { error: "failed to register database" },
          { status: 500 }
        );
      }
      if (dbType !== "sqlite" && (!host || !user || !password || port <= 0)) {
        return HttpResponse.json(
          { error: "failed to register database" },
          { status: 500 }
        );
      }

      const dbId = state.nextDbId++;
      state.databases.push({
        id: dbId,
        name,
        dbType,
        host,
        port,
        user,
        password,
        dbName,
      });
      return HttpResponse.json(
        { id: dbId, message: `DB ditambahkan dengan id ${dbId}` },
        { status: 200, headers: withSessionRotation({}, auth.rotatedToken) }
      );
    }),

    http.get(
      `${TEST_BACKEND_URL}/databases/:dbID/schemas`,
      ({ request, params }) => {
        const auth = requireDb(request, params.dbID, "VIEW_ONLY");
        if ("error" in auth) return auth.error;
        const schemas = auth.dbId === 1 ? ["public", "analytics"] : ["public"];
        return HttpResponse.json(
          { db_id: auth.dbId, schemas },
          { status: 200, headers: withSessionRotation({}, auth.rotatedToken) }
        );
      }
    ),

    http.get(
      `${TEST_BACKEND_URL}/databases/:dbID/schemas/:schema/tables`,
      ({ request, params }) => {
        const auth = requireDb(request, params.dbID, "VIEW_ONLY");
        if ("error" in auth) return auth.error;
        const schema = String(params.schema);
        const tables =
          schema === "analytics"
            ? ["daily_metrics", "empty_audit"]
            : ["produk", "pesanan"];
        return HttpResponse.json(
          { db_id: auth.dbId, schema, tables },
          { status: 200, headers: withSessionRotation({}, auth.rotatedToken) }
        );
      }
    ),

    http.get(
      `${TEST_BACKEND_URL}/databases/:dbID/schemas/:schema/tables/:table`,
      ({ request, params }) => {
        const auth = requireDb(request, params.dbID, "VIEW_ONLY");
        if ("error" in auth) return auth.error;
        const schema = String(params.schema);
        const table = String(params.table);
        if (table === "empty_audit") {
          return HttpResponse.json(
            {
              db_id: auth.dbId,
              metadata: {
                schema,
                tableName: table,
                columns: [],
                constraints: [],
                indexes: [],
              },
            },
            { status: 200, headers: withSessionRotation({}, auth.rotatedToken) }
          );
        }
        return HttpResponse.json(
          {
            db_id: auth.dbId,
            metadata: {
              schema,
              tableName: table,
              columns: [
                { name: "id", dataType: "integer", isNullable: false },
                { name: "nama", dataType: "text", isNullable: false },
              ],
              constraints: [{ name: `${table}_pkey`, type: "PRIMARY KEY" }],
              indexes: [{ name: `${table}_pkey`, isUnique: true }],
            },
          },
          { status: 200, headers: withSessionRotation({}, auth.rotatedToken) }
        );
      }
    ),

    http.get(
      `${TEST_BACKEND_URL}/databases/:dbID/schemas/:schema/tables/:table/data`,
      ({ request, params }) => {
        const auth = requireDb(request, params.dbID, "VIEW_ONLY");
        if ("error" in auth) return auth.error;
        const schema = String(params.schema);
        const table = String(params.table);
        const url = new URL(request.url);
        const limit = Number.parseInt(url.searchParams.get("limit") ?? "10", 10);
        const offset = Number.parseInt(
          url.searchParams.get("offset") ?? "0",
          10
        );

        const sourceRows = table === "produk" ? PRODUK_ROWS : [];
        const sliced = sourceRows.slice(offset, offset + limit);
        const columns = sliced.length > 0 ? Object.keys(sliced[0]).sort() : [];

        return HttpResponse.json(
          {
            db_id: auth.dbId,
            schema,
            table_name: table,
            columns,
            rows: sliced,
          },
          { status: 200, headers: withSessionRotation({}, auth.rotatedToken) }
        );
      }
    ),

    http.get(
      `${TEST_BACKEND_URL}/databases/:dbID/migrations`,
      ({ request, params }) => {
        const auth = requireDb(request, params.dbID, "VIEW_ONLY");
        if ("error" in auth) return auth.error;
        const canEdit = auth.granted === "VIEW_EDIT";
        const url = new URL(request.url);
        const hideRollback = url.searchParams.get("hide_rollback") === "true";
        let history = state.executions
          .filter((e) => e.targetDbId === auth.dbId)
          .sort((a, b) => (a.executionId ?? 0) - (b.executionId ?? 0));
        if (hideRollback) {
          const applied = new Map<number, MigrationRecord>();
          for (const entry of history) {
            if (
              entry.executionId &&
              entry.status === "APPLIED" &&
              (entry.action === "APPLY" || entry.action === "FORWARD")
            ) {
              applied.set(entry.executionId, entry);
            }
          }
          const hidden = new Set<number>();
          for (const entry of history) {
            if (
              !entry.executionId ||
              entry.action !== "ROLLBACK" ||
              entry.status !== "APPLIED" ||
              !entry.revertsExecutionId
            ) {
              continue;
            }
            const target = applied.get(entry.revertsExecutionId);
            if (
              target &&
              (target.executionId ?? 0) < entry.executionId &&
              target.id === entry.id &&
              target.targetDbId === entry.targetDbId
            ) {
              hidden.add(entry.executionId);
              hidden.add(target.executionId!);
            }
          }
          history = history.filter(
            (e) => !e.executionId || !hidden.has(e.executionId)
          );
        }

        const drafts: MigrationRecord[] = canEdit
          ? state.definitions
              .filter(
                (def) =>
                  def.target_db_id === auth.dbId &&
                  !state.executions.some(
                    (e) =>
                      e.targetDbId === auth.dbId && e.id === def.migration_id
                  )
              )
              .map((def) => ({
                id: def.migration_id,
                revision: def.revision,
                targetDbId: def.target_db_id,
                version: 0,
                sqlQuery: def.sql_up,
                executedBy: "",
                status: "DRAFT" as const,
                executedAt: "",
              }))
          : [];

        return HttpResponse.json(
          {
            db_id: auth.dbId,
            migrations: history,
            drafts,
            can_edit: canEdit,
          },
          { status: 200, headers: withSessionRotation({}, auth.rotatedToken) }
        );
      }
    ),

    http.get(
      `${TEST_BACKEND_URL}/databases/:dbID/migrations/position`,
      ({ request, params }) => {
        const auth = requireDb(request, params.dbID, "VIEW_ONLY");
        if ("error" in auth) return auth.error;
        const applied = state.executions
          .filter((e) => e.targetDbId === auth.dbId && e.status === "APPLIED")
          .sort((a, b) => (a.executionId ?? 0) - (b.executionId ?? 0));
        const active: MigrationRecord[] = [];
        let forward: MigrationRecord[] = [];
        for (const attempt of applied) {
          if (attempt.action === "APPLY") {
            active.push(attempt);
            forward = [];
          } else if (attempt.action === "ROLLBACK") {
            const popped = active.pop();
            if (popped) forward.push(popped);
          } else if (attempt.action === "FORWARD") {
            const popped = forward.pop();
            if (popped) active.push(popped);
          }
        }
        const toRef = (item?: MigrationRecord) =>
          item
            ? {
                execution_id: item.executionId ?? 0,
                migration_id: item.id,
                revision: item.revision ?? 1,
              }
            : null;
        return HttpResponse.json(
          {
            active_head: toRef(active[active.length - 1]),
            next_forward: toRef(forward[forward.length - 1]),
          },
          { status: 200, headers: withSessionRotation({}, auth.rotatedToken) }
        );
      }
    ),

    http.get(
      `${TEST_BACKEND_URL}/databases/:dbID/migration-reviews`,
      ({ request, params }) => {
        const auth = requireDb(request, params.dbID, "VIEW_ONLY");
        if ("error" in auth) return auth.error;
        const leaderGroupIds = new Set(
          state.groups
            .filter(
              (g) =>
                g.leaderId === auth.user.id &&
                g.memberIds.includes(auth.user.id) &&
                g.permissions.some((p) => p.targetDbId === auth.dbId)
            )
            .map((g) => g.id)
        );
        const pending = state.reviews.filter(
          (rev) =>
            rev.target_db_id === auth.dbId &&
            rev.status === "PENDING_REVIEW" &&
            rev.groups.some(
              (g) => g.status === "PENDING" && leaderGroupIds.has(g.group_id)
            )
        );
        return HttpResponse.json(pending, {
          status: 200,
          headers: withSessionRotation({}, auth.rotatedToken),
        });
      }
    ),

    http.post(
      `${TEST_BACKEND_URL}/databases/:dbID/migrations`,
      async ({ request, params }) => {
        const auth = requireDb(request, params.dbID, "VIEW_EDIT");
        if ("error" in auth) return auth.error;
        const body = (await request.json()) as Record<string, unknown>;
        let sqlUp = "";
        let sqlDown = "";
        let irreversible = false;
        let reason = "";

        if (typeof body.sql_up === "string") {
          sqlUp = body.sql_up;
          sqlDown = typeof body.sql_down === "string" ? body.sql_down : "";
          irreversible = Boolean(body.irreversible);
          reason =
            typeof body.irreversible_reason === "string"
              ? body.irreversible_reason.trim()
              : "";
          const hasDown = sqlDown.trim() !== "";
          const hasReason = reason !== "";
          if (
            sqlUp.trim() === "" ||
            (irreversible && (hasDown || !hasReason)) ||
            (!irreversible && (!hasDown || hasReason))
          ) {
            return HttpResponse.json(
              {
                error:
                  "provide SQL up and down, or mark irreversible and provide a reason",
              },
              { status: 400 }
            );
          }
        } else if (typeof body.sql_query === "string" && body.sql_query.trim()) {
          sqlUp = body.sql_query;
          irreversible = true;
          reason = "SQL down was not supplied by the M1 SQL-up-only interface.";
        } else {
          return HttpResponse.json(
            { error: "invalid request body" },
            { status: 400 }
          );
        }

        const id = state.nextMigrationId++;
        state.definitions.push({
          migration_id: id,
          target_db_id: auth.dbId,
          revision: 1,
          sql_up: sqlUp,
          sql_down: sqlDown,
          irreversible,
          irreversible_reason: reason,
        });
        return HttpResponse.json(
          { id, db_id: auth.dbId },
          { status: 201, headers: withSessionRotation({}, auth.rotatedToken) }
        );
      }
    ),

    http.get(
      `${TEST_BACKEND_URL}/databases/:dbID/migrations/:migrationID/definition`,
      ({ request, params }) => {
        const auth = requireDb(request, params.dbID, "VIEW_ONLY");
        if ("error" in auth) return auth.error;
        const migrationId = Number.parseInt(String(params.migrationID), 10);
        const def = state.definitions.find(
          (d) => d.target_db_id === auth.dbId && d.migration_id === migrationId
        );
        if (!def) {
          return HttpResponse.json(
            { error: "migration not found" },
            { status: 404 }
          );
        }
        return HttpResponse.json(def, {
          status: 200,
          headers: withSessionRotation({}, auth.rotatedToken),
        });
      }
    ),

    http.get(
      `${TEST_BACKEND_URL}/databases/:dbID/migrations/:migrationID`,
      ({ request, params }) => {
        const auth = requireDb(request, params.dbID, "VIEW_ONLY");
        if ("error" in auth) return auth.error;
        const migrationId = Number.parseInt(String(params.migrationID), 10);
        const url = new URL(request.url);
        const jobIdParam = url.searchParams.get("job_id");
        if (jobIdParam !== null) {
          const jobId = Number.parseInt(jobIdParam, 10);
          const job = state.jobs.find(
            (j) =>
              j.target_db_id === auth.dbId &&
              j.migration_id === migrationId &&
              j.job_id === jobId
          );
          if (!job) {
            return HttpResponse.json(
              { error: "job not found" },
              { status: 404 }
            );
          }
          return HttpResponse.json(job, {
            status: 200,
            headers: withSessionRotation({}, auth.rotatedToken),
          });
        }

        const def = state.definitions.find(
          (d) => d.target_db_id === auth.dbId && d.migration_id === migrationId
        );
        if (!def) {
          return HttpResponse.json(
            { error: "migration not found" },
            { status: 404 }
          );
        }
        const execs = state.executions
          .filter(
            (e) => e.targetDbId === auth.dbId && e.id === migrationId
          )
          .sort((a, b) => (b.executionId ?? 0) - (a.executionId ?? 0));
        const migration: MigrationRecord = execs[0] ?? {
          id: def.migration_id,
          revision: def.revision,
          targetDbId: def.target_db_id,
          version: 0,
          sqlQuery: def.sql_up,
          executedBy: "",
          status: "DRAFT",
          executedAt: "",
        };
        return HttpResponse.json(
          { migration, can_edit: auth.granted === "VIEW_EDIT" },
          { status: 200, headers: withSessionRotation({}, auth.rotatedToken) }
        );
      }
    ),

    http.put(
      `${TEST_BACKEND_URL}/databases/:dbID/migrations/:migrationID`,
      async ({ request, params }) => {
        const auth = requireDb(request, params.dbID, "VIEW_EDIT");
        if ("error" in auth) return auth.error;
        const migrationId = Number.parseInt(String(params.migrationID), 10);
        const def = state.definitions.find(
          (d) => d.target_db_id === auth.dbId && d.migration_id === migrationId
        );
        const hasExecutions = state.executions.some(
          (e) => e.targetDbId === auth.dbId && e.id === migrationId
        );
        if (!def || hasExecutions) {
          return HttpResponse.json(
            { error: "editable draft not found" },
            { status: 409 }
          );
        }

        const body = (await request.json()) as Record<string, unknown>;
        if (typeof body.sql_up === "string") {
          const sqlUp = body.sql_up;
          const sqlDown =
            typeof body.sql_down === "string" ? body.sql_down : "";
          const irreversible = Boolean(body.irreversible);
          const reason =
            typeof body.irreversible_reason === "string"
              ? body.irreversible_reason.trim()
              : "";
          const hasDown = sqlDown.trim() !== "";
          const hasReason = reason !== "";
          if (
            sqlUp.trim() === "" ||
            (irreversible && (hasDown || !hasReason)) ||
            (!irreversible && (!hasDown || hasReason))
          ) {
            return HttpResponse.json(
              { error: "invalid request body" },
              { status: 400 }
            );
          }
          def.sql_up = sqlUp;
          def.sql_down = sqlDown;
          def.irreversible = irreversible;
          def.irreversible_reason = reason;
        } else if (typeof body.sql_query === "string" && body.sql_query.trim()) {
          def.sql_up = body.sql_query;
        } else {
          return HttpResponse.json(
            { error: "invalid request body" },
            { status: 400 }
          );
        }

        def.revision += 1;
        return HttpResponse.json(
          { id: migrationId, db_id: auth.dbId, message: "draft updated" },
          { status: 200, headers: withSessionRotation({}, auth.rotatedToken) }
        );
      }
    ),

    http.post(
      `${TEST_BACKEND_URL}/databases/:dbID/migrations/:migrationID/explain`,
      ({ request, params }) => {
        const auth = requireDb(request, params.dbID, "VIEW_EDIT");
        if ("error" in auth) return auth.error;
        const migrationId = Number.parseInt(String(params.migrationID), 10);
        const def = state.definitions.find(
          (d) => d.target_db_id === auth.dbId && d.migration_id === migrationId
        );
        if (!def) {
          return HttpResponse.json(
            { error: "draft not found" },
            { status: 500 }
          );
        }
        return HttpResponse.json(
          {
            db_id: auth.dbId,
            migration_id: migrationId,
            explain_plan: `Seq Scan on public.produk  (cost=0.00..12.00 rows=12 width=32) -- ${def.sql_up}`,
          },
          { status: 200, headers: withSessionRotation({}, auth.rotatedToken) }
        );
      }
    ),

    http.post(
      `${TEST_BACKEND_URL}/databases/:dbID/migrations/:migrationID/submit`,
      async ({ request, params }) => {
        const auth = requireDb(request, params.dbID, "VIEW_EDIT");
        if ("error" in auth) return auth.error;
        const migrationId = Number.parseInt(String(params.migrationID), 10);
        const body = (await request.json()) as Record<string, unknown>;
        const action = String(body.action ?? "") as
          | "APPLY"
          | "ROLLBACK"
          | "FORWARD";
        const mode = (body.mode ? String(body.mode) : "STANDARD") as
          | "STANDARD"
          | "BATCH";
        const revision = Number(body.revision ?? 0);
        const expectedExecutionId = Number(body.expected_execution_id ?? 0);
        const batchSize = Number(body.batch_size ?? 0);
        const batchTable =
          typeof body.batch_table === "string" ? body.batch_table : "";
        const batchAttribute =
          typeof body.batch_attribute === "string" ? body.batch_attribute : "";

        if (
          (mode !== "STANDARD" && mode !== "BATCH") ||
          (mode === "STANDARD" &&
            (batchSize !== 0 || batchTable !== "" || batchAttribute !== "")) ||
          (mode === "BATCH" &&
            (batchSize <= 0 ||
              batchSize > 10000 ||
              !batchTable ||
              !batchAttribute))
        ) {
          return new HttpResponse("invalid migration mode or batch configuration", {
            status: 400,
          });
        }

        if (action === "APPLY") {
          if (revision <= 0 || expectedExecutionId !== 0) {
            return new HttpResponse("apply requires a draft revision", {
              status: 400,
            });
          }
        } else if (
          (action !== "ROLLBACK" && action !== "FORWARD") ||
          expectedExecutionId <= 0 ||
          revision !== 0
        ) {
          return new HttpResponse(
            "rollback and forward require an expected execution ID",
            { status: 400 }
          );
        }

        const def = state.definitions.find(
          (d) => d.target_db_id === auth.dbId && d.migration_id === migrationId
        );
        if (!def) {
          return new HttpResponse("invalid migration submission", {
            status: 400,
          });
        }
        if (action === "APPLY" && def.revision !== revision) {
          return new HttpResponse("migration cannot be submitted for review", {
            status: 409,
          });
        }
        if (action === "ROLLBACK" && (def.irreversible || !def.sql_down)) {
          return new HttpResponse("migration cannot be submitted for review", {
            status: 409,
          });
        }

        const dbGroups = state.groups.filter((g) =>
          g.permissions.some((p) => p.targetDbId === auth.dbId)
        );
        if (
          dbGroups.length === 0 ||
          dbGroups.some(
            (g) => g.leaderId === null || !g.memberIds.includes(g.leaderId)
          )
        ) {
          return new HttpResponse("migration cannot be submitted for review", {
            status: 409,
          });
        }

        const reviewId = state.nextReviewId++;
        const review: StoredReview = {
          review_id: reviewId,
          target_db_id: auth.dbId,
          migration_id: migrationId,
          status: "PENDING_REVIEW",
          actor_id: auth.user.id,
          action,
          mode,
          batch_size: batchSize,
          batch_table: mode === "BATCH" ? batchTable : undefined,
          batch_attribute: mode === "BATCH" ? batchAttribute : undefined,
          revision,
          expected_execution_id:
            expectedExecutionId > 0 ? expectedExecutionId : undefined,
          sql_snapshot: action === "ROLLBACK" ? def.sql_down : def.sql_up,
          groups: dbGroups.map((g) => ({
            group_id: g.id,
            status: "PENDING",
            decided_by: null,
            decided_at: null,
          })),
        };
        state.reviews.push(review);

        return HttpResponse.json(
          { review_id: String(reviewId) },
          { status: 202, headers: withSessionRotation({}, auth.rotatedToken) }
        );
      }
    ),

    http.get(
      `${TEST_BACKEND_URL}/databases/:dbID/migrations/:migrationID/reviews/:reviewID`,
      ({ request, params }) => {
        const auth = requireDb(request, params.dbID, "VIEW_ONLY");
        if ("error" in auth) return auth.error;
        const migrationId = Number.parseInt(String(params.migrationID), 10);
        const reviewId = Number.parseInt(String(params.reviewID), 10);
        const review = state.reviews.find(
          (r) =>
            r.target_db_id === auth.dbId &&
            r.migration_id === migrationId &&
            r.review_id === reviewId
        );
        if (!review) {
          return new HttpResponse("migration review not found", {
            status: 404,
          });
        }
        return HttpResponse.json(review, {
          status: 200,
          headers: withSessionRotation({}, auth.rotatedToken),
        });
      }
    ),

    http.post(
      `${TEST_BACKEND_URL}/databases/:dbID/migrations/:migrationID/reviews/:reviewID/decision`,
      async ({ request, params }) => {
        const auth = requireDb(request, params.dbID, "VIEW_ONLY");
        if ("error" in auth) return auth.error;
        const migrationId = Number.parseInt(String(params.migrationID), 10);
        const reviewId = Number.parseInt(String(params.reviewID), 10);
        const review = state.reviews.find(
          (r) =>
            r.target_db_id === auth.dbId &&
            r.migration_id === migrationId &&
            r.review_id === reviewId
        );
        if (!review) {
          return new HttpResponse("migration review not found", {
            status: 404,
          });
        }
        const body = (await request.json()) as Record<string, unknown>;
        const groupId = Number(body.group_id);
        const decision = String(body.decision ?? "") as
          | "APPROVED"
          | "REJECTED";
        if (
          groupId <= 0 ||
          (decision !== "APPROVED" && decision !== "REJECTED")
        ) {
          return new HttpResponse("invalid decision", { status: 400 });
        }
        const group = state.groups.find((g) => g.id === groupId);
        const reviewGroup = review.groups.find((g) => g.group_id === groupId);
        if (
          review.status !== "PENDING_REVIEW" ||
          !group ||
          !reviewGroup ||
          reviewGroup.status !== "PENDING" ||
          group.leaderId !== auth.user.id ||
          !group.memberIds.includes(auth.user.id)
        ) {
          return new HttpResponse("migration decision not permitted", {
            status: 409,
          });
        }

        const now = new Date().toISOString();
        reviewGroup.status = decision;
        reviewGroup.decided_by = auth.user.id;
        reviewGroup.decided_at = now;

        if (decision === "REJECTED") {
          review.status = "REJECTED";
          return new HttpResponse(null, {
            status: 204,
            headers: withSessionRotation({}, auth.rotatedToken),
          });
        }

        const allApproved = review.groups.every((g) => g.status === "APPROVED");
        const headers = withSessionRotation({}, auth.rotatedToken);
        if (allApproved) {
          review.status = "APPROVED";
          const executionId = state.nextExecutionId++;
          const failed = review.sql_snapshot.includes("FAIL");
          const execRecord: MigrationRecord = {
            id: review.migration_id,
            executionId,
            action: review.action,
            revision: review.revision > 0 ? review.revision : 1,
            revertsExecutionId:
              review.action === "ROLLBACK"
                ? (review.expected_execution_id ?? null)
                : null,
            targetDbId: review.target_db_id,
            version: failed ? 0 : state.nextVersion++,
            sqlQuery: review.sql_snapshot,
            executedBy: String(review.actor_id),
            status: failed ? "FAILED" : "APPLIED",
            executedAt: now,
          };
          state.executions.push(execRecord);

          const jobId = state.nextJobId++;
          state.jobs.push({
            job_id: jobId,
            execution_id: executionId,
            migration_id: review.migration_id,
            target_db_id: review.target_db_id,
            action: review.action,
            mode: review.mode,
            batch_size: review.batch_size,
            batch_table: review.batch_table,
            batch_attribute: review.batch_attribute,
            status: failed ? "FAILED" : "SUCCEEDED",
            attempts: 1,
            committed_batches: review.mode === "BATCH" && !failed ? 1 : 0,
            committed_rows: review.mode === "BATCH" && !failed ? 10 : 0,
            total_rows: review.mode === "BATCH" && !failed ? 10 : 0,
            cursor: review.mode === "BATCH" && !failed ? 10 : 0,
            error: failed ? "execution failed and rolled back" : undefined,
            next_attempt_at: now,
            created_at: now,
            updated_at: now,
          });
          headers.set(
            "Location",
            `/databases/${review.target_db_id}/migrations/${review.migration_id}?job_id=${jobId}`
          );
          headers.set("Access-Control-Expose-Headers", "Location");
        }

        return new HttpResponse(null, { status: 204, headers });
      }
    ),

    http.post(
      `${TEST_BACKEND_URL}/databases/:dbID/migrations/:migrationID/execute`,
      ({ request, params }) => {
        const auth = requireDb(request, params.dbID, "VIEW_EDIT");
        if ("error" in auth) return auth.error;
        return HttpResponse.json(
          { error: "submit this migration for approval before execution" },
          { status: 409, headers: withSessionRotation({}, auth.rotatedToken) }
        );
      }
    ),
  ];

  const server = setupServer(...handlers);

  function reset() {
    state = buildInitialState();
    activeSessionToken = null;
    if (typeof document !== "undefined") {
      document.cookie = `${SESSION_COOKIE}=; Path=/; Max-Age=0; SameSite=Lax`;
    }
    if (typeof localStorage !== "undefined") {
      localStorage.clear();
    }
  }

  function scheduleSessionRotation(oldToken: string, newToken: string) {
    state.pendingRotation.set(oldToken, newToken);
  }

  function revokeSession(token: string) {
    state.sessions.delete(token);
    if (activeSessionToken === token) {
      activeSessionToken = null;
    }
  }

  function getActiveSessionCookie(): string {
    return activeSessionToken ? `session_id=${activeSessionToken}` : "";
  }

  return {
    server,
    reset,
    scheduleSessionRotation,
    revokeSession,
    getActiveSessionCookie,
  };
}
