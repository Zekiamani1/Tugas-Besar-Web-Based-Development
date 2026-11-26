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
  memberIds: number[];
  permissions: Array<{ targetDbId: number; accessLevel: AccessLevel }>;
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
        memberIds: [2],
        permissions: [
          { targetDbId: 1, accessLevel: "VIEW_EDIT" },
          { targetDbId: 2, accessLevel: "VIEW_EDIT" },
        ],
      },
      {
        id: 2,
        name: "tim_analis",
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
    migrations: [
      {
        id: 1,
        targetDbId: 1,
        version: 1,
        sqlQuery: "CREATE TABLE public.produk (id INT PRIMARY KEY);",
        executedBy: "2",
        status: "APPLIED",
        executedAt: "2026-11-24T10:00:00Z",
      },
    ] as MigrationRecord[],
    sessions: new Map<string, number>(),
    pendingRotation: new Map<string, string>(),
    nextUserId: 4,
    nextGroupId: 3,
    nextDbId: 3,
    nextMigrationId: 2,
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
        const allForDb = state.migrations.filter(
          (m) => m.targetDbId === auth.dbId
        );
        const history = allForDb.filter((m) => m.status !== "DRAFT");
        const drafts = canEdit
          ? allForDb.filter((m) => m.status === "DRAFT")
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

    http.post(
      `${TEST_BACKEND_URL}/databases/:dbID/migrations`,
      async ({ request, params }) => {
        const auth = requireDb(request, params.dbID, "VIEW_EDIT");
        if ("error" in auth) return auth.error;
        const body = (await request.json()) as Record<string, unknown>;
        const sqlQuery =
          typeof body.sql_query === "string" ? body.sql_query : "";
        const id = state.nextMigrationId++;
        state.migrations.push({
          id,
          targetDbId: auth.dbId,
          version: 0,
          sqlQuery,
          executedBy: "",
          status: "DRAFT",
          executedAt: "",
        });
        return HttpResponse.json(
          { id, db_id: auth.dbId },
          { status: 201, headers: withSessionRotation({}, auth.rotatedToken) }
        );
      }
    ),

    http.get(
      `${TEST_BACKEND_URL}/databases/:dbID/migrations/:migrationID`,
      ({ request, params }) => {
        const auth = requireDb(request, params.dbID, "VIEW_ONLY");
        if ("error" in auth) return auth.error;
        const migrationId = Number.parseInt(String(params.migrationID), 10);
        const record = state.migrations.find(
          (m) => m.targetDbId === auth.dbId && m.id === migrationId
        );
        if (!record) {
          return HttpResponse.json(
            { error: "migration not found" },
            { status: 500 }
          );
        }
        return HttpResponse.json(
          { migration: record, can_edit: auth.granted === "VIEW_EDIT" },
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
        const body = (await request.json()) as Record<string, unknown>;
        const sqlQuery =
          typeof body.sql_query === "string" ? body.sql_query : "";
        const record = state.migrations.find(
          (m) =>
            m.targetDbId === auth.dbId &&
            m.id === migrationId &&
            m.status === "DRAFT"
        );
        if (!record) {
          return HttpResponse.json(
            { error: "draft not found" },
            { status: 500 }
          );
        }
        record.sqlQuery = sqlQuery;
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
        const record = state.migrations.find(
          (m) => m.targetDbId === auth.dbId && m.id === migrationId
        );
        if (!record) {
          return HttpResponse.json(
            { error: "draft not found" },
            { status: 500 }
          );
        }
        return HttpResponse.json(
          {
            db_id: auth.dbId,
            migration_id: migrationId,
            explain_plan: `Seq Scan on public.produk  (cost=0.00..12.00 rows=12 width=32) -- ${record.sqlQuery}`,
          },
          { status: 200, headers: withSessionRotation({}, auth.rotatedToken) }
        );
      }
    ),

    http.post(
      `${TEST_BACKEND_URL}/databases/:dbID/migrations/:migrationID/execute`,
      ({ request, params }) => {
        const auth = requireDb(request, params.dbID, "VIEW_EDIT");
        if ("error" in auth) return auth.error;
        const migrationId = Number.parseInt(String(params.migrationID), 10);
        const record = state.migrations.find(
          (m) =>
            m.targetDbId === auth.dbId &&
            m.id === migrationId &&
            m.status === "DRAFT"
        );
        if (!record) {
          return HttpResponse.json(
            { error: "draft not found" },
            { status: 500 }
          );
        }
        record.executedBy = String(auth.user.id);
        record.executedAt = "2026-11-25T12:00:00Z";
        if (record.sqlQuery.includes("FAIL")) {
          record.status = "FAILED";
          return HttpResponse.json(
            { error: "execution failed and rolled back" },
            { status: 500, headers: withSessionRotation({}, auth.rotatedToken) }
          );
        }
        record.version = state.nextVersion++;
        record.status = "APPLIED";
        return HttpResponse.json(
          { migration: record },
          { status: 200, headers: withSessionRotation({}, auth.rotatedToken) }
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
