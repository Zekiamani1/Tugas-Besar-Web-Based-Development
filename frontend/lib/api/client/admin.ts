import type {
  AccessLevel,
  ActionResult,
  DBType,
  Group,
  GroupDetail,
  RegisterTargetDBInput,
  TargetDBInfo,
  UserSummary,
} from "@/lib/types";
import type { AdminService } from "../contract";
import { requestJson } from "../http";

export const adminService: AdminService = {
  async removeUserFromGroup(userId, groupId) {
    if (!Number.isSafeInteger(userId) || userId <= 0 || !Number.isSafeInteger(groupId) || groupId <= 0) return { ok: false, error: "Select a valid user and group." };
    try {
      await requestJson(`/admin/groups/${groupId}/members/${userId}`, { method: "DELETE" });
      return { ok: true, message: "User removed from group." };
    } catch (err) { return { ok: false, error: err instanceof Error ? err.message : "Could not remove user from group." }; }
  },

  async revokeGroupDbAccess(groupId, targetDbId) {
    if (!Number.isSafeInteger(groupId) || groupId <= 0 || !Number.isSafeInteger(targetDbId) || targetDbId <= 0) return { ok: false, error: "Select a valid group and database." };
    try {
      await requestJson(`/admin/groups/${groupId}/databases/${targetDbId}/access`, { method: "DELETE" });
      return { ok: true, message: "Database access revoked." };
    } catch (err) { return { ok: false, error: err instanceof Error ? err.message : "Could not revoke database access." }; }
  },
  async listUsers() {
    const res = await requestJson<{ users?: UserSummary[] }>("/admin/users");
    return Array.isArray(res.users) ? res.users : [];
  },

  async listGroups() {
    const res = await requestJson<{ groups?: GroupDetail[] }>("/admin/groups");
    return Array.isArray(res.groups) ? res.groups : [];
  },

  async listDatabases() {
    const res = await requestJson<{ databases?: TargetDBInfo[] }>(
      "/admin/databases"
    );
    return Array.isArray(res.databases) ? res.databases : [];
  },

  async registerUser(username, password) {
    const trimmedUsername = username.trim();
    if (!trimmedUsername || !password) {
      return { ok: false, error: "Periksa kembali data pengguna." };
    }
    try {
      const res = await requestJson<{ message?: string }>("/admin/users", {
        method: "POST",
        body: JSON.stringify({ username: trimmedUsername, password }),
      });
      return {
        ok: true,
        message: res.message,
        data: { id: 0, username: trimmedUsername, isAdmin: false },
      };
    } catch (err) {
      return {
        ok: false,
        error:
          err instanceof Error && err.message
            ? err.message
            : "Pengguna tidak dapat ditambahkan.",
      };
    }
  },

  async createGroup(groupName): Promise<ActionResult<Group>> {
    const trimmedName = groupName.trim();
    if (!trimmedName) {
      return { ok: false, error: "Periksa kembali nama grup." };
    }
    try {
      const res = await requestJson<{ message?: string }>("/admin/groups", {
        method: "POST",
        body: JSON.stringify({ group_name: trimmedName }),
      });
      return {
        ok: true,
        message: res.message,
        data: { id: 0, name: trimmedName },
      };
    } catch (err) {
      return {
        ok: false,
        error:
          err instanceof Error && err.message
            ? err.message
            : "Grup tidak dapat dibuat.",
      };
    }
  },

  async assignUserToGroup(userId, groupId) {
    if (
      !Number.isSafeInteger(userId) ||
      userId <= 0 ||
      !Number.isSafeInteger(groupId) ||
      groupId <= 0
    ) {
      return { ok: false, error: "Pilih user dan group yang valid." };
    }
    try {
      const res = await requestJson<{ message?: string }>(
        "/admin/groups/members",
        {
          method: "POST",
          body: JSON.stringify({ user_id: userId, group_id: groupId }),
        }
      );
      return { ok: true, message: res.message };
    } catch (err) {
      return {
        ok: false,
        error:
          err instanceof Error && err.message
            ? err.message
            : "Pengguna tidak dapat dimasukkan ke grup.",
      };
    }
  },

  async setGroupLeader(groupId, userId) {
    if (
      !Number.isSafeInteger(groupId) ||
      groupId <= 0 ||
      !Number.isSafeInteger(userId) ||
      userId <= 0
    ) {
      return { ok: false, error: "Pilih group dan user yang valid." };
    }
    try {
      await requestJson<null>("/admin/groups/leader", {
        method: "POST",
        body: JSON.stringify({ group_id: groupId, user_id: userId }),
      });
      return { ok: true, message: "Leader group berhasil ditetapkan." };
    } catch (err) {
      return {
        ok: false,
        error:
          err instanceof Error && err.message
            ? err.message
            : "Leader group tidak dapat ditetapkan.",
      };
    }
  },

  async setGroupDbAccess(groupId, targetDbId, accessLevel: AccessLevel) {
    if (
      !Number.isSafeInteger(groupId) ||
      groupId <= 0 ||
      !Number.isSafeInteger(targetDbId) ||
      targetDbId <= 0
    ) {
      return { ok: false, error: "Pilih group dan database yang valid." };
    }
    try {
      const res = await requestJson<{ message?: string }>(
        "/admin/groups/permissions",
        {
          method: "POST",
          body: JSON.stringify({
            group_id: groupId,
            target_db_id: targetDbId,
            access_level: accessLevel,
          }),
        }
      );
      return { ok: true, message: res.message };
    } catch (err) {
      return {
        ok: false,
        error:
          err instanceof Error && err.message
            ? err.message
            : "Izin database tidak dapat disimpan.",
      };
    }
  },

  async registerDatabase(input: RegisterTargetDBInput) {
    const name = input.name.trim();
    const dbType = input.dbType.trim();
    const dbName = input.dbName.trim();
    const host = input.host.trim();
    const portStr = input.port.trim();
    const user = input.user.trim();
    const password = input.password;

    if (
      !name ||
      !dbName ||
      (dbType !== "postgres" && dbType !== "mysql" && dbType !== "sqlite")
    ) {
      return {
        ok: false,
        error: "Port harus berupa angka dan semua data koneksi perlu diisi.",
      };
    }

    let payload: {
      name: string;
      dbType: DBType;
      host: string;
      port: number;
      user: string;
      password: string;
      dbName: string;
    };

    if (dbType === "sqlite") {
      payload = {
        name,
        dbType,
        host: "",
        port: 0,
        user: "",
        password: "",
        dbName,
      };
    } else {
      const port = Number.parseInt(portStr, 10);
      if (
        !host ||
        !user ||
        !password ||
        !/^\d+$/.test(portStr) ||
        !Number.isSafeInteger(port) ||
        port < 1 ||
        port > 65535
      ) {
        return {
          ok: false,
          error: "Port harus berupa angka dan semua data koneksi perlu diisi.",
        };
      }
      payload = {
        name,
        dbType,
        host,
        port,
        user,
        password,
        dbName,
      };
    }

    try {
      const res = await requestJson<{ id: number; message?: string }>(
        "/admin/databases",
        {
          method: "POST",
          body: JSON.stringify(payload),
        }
      );
      return {
        ok: true,
        message: res.message,
        data: {
          id: res.id,
          name: payload.name,
          dbType: payload.dbType,
          host: payload.host,
          port: payload.port,
          dbName: payload.dbName,
        },
      };
    } catch (err) {
      return {
        ok: false,
        error:
          err instanceof Error && err.message
            ? err.message
            : "Database tidak dapat didaftarkan.",
      };
    }
  },
};
