import type { AdminService } from "@/lib/api/contract";
import type {
  AccessLevel,
  GroupDetail,
  RegisterTargetDBInput,
  TargetDBInfo,
  UserSummary,
} from "@/lib/types";

export async function submitRegisterUser(
  adminService: AdminService,
  username: string,
  password: string
): Promise<
  { ok: true; message: string; users: UserSummary[] } | { ok: false; error: string }
> {
  const res = await adminService.registerUser(username, password);
  if (!res.ok) {
    return { ok: false, error: res.error };
  }
  const users = await adminService.listUsers();
  return {
    ok: true,
    message: res.message ?? "User berhasil ditambahkan.",
    users,
  };
}

export async function submitCreateGroup(
  adminService: AdminService,
  groupName: string
): Promise<
  { ok: true; message: string; groups: GroupDetail[] } | { ok: false; error: string }
> {
  const res = await adminService.createGroup(groupName);
  if (!res.ok) {
    return { ok: false, error: res.error };
  }
  const groups = await adminService.listGroups();
  return {
    ok: true,
    message: res.message ?? "Group berhasil dibuat.",
    groups,
  };
}

export async function submitAssignMember(
  adminService: AdminService,
  userIdRaw: string,
  groupIdRaw: string
): Promise<
  { ok: true; message: string; groups: GroupDetail[] } | { ok: false; error: string }
> {
  const res = await adminService.assignUserToGroup(
    Number.parseInt(userIdRaw, 10),
    Number.parseInt(groupIdRaw, 10)
  );
  if (!res.ok) {
    return { ok: false, error: res.error };
  }
  const groups = await adminService.listGroups();
  return {
    ok: true,
    message: res.message ?? "User berhasil dimasukkan ke group.",
    groups,
  };
}

export async function submitSetGroupLeader(
  adminService: AdminService,
  groupIdRaw: string,
  userIdRaw: string
): Promise<
  { ok: true; message: string; groups: GroupDetail[] } | { ok: false; error: string }
> {
  const res = await adminService.setGroupLeader(
    Number.parseInt(groupIdRaw, 10),
    Number.parseInt(userIdRaw, 10)
  );
  if (!res.ok) {
    return { ok: false, error: res.error };
  }
  const groups = await adminService.listGroups();
  return {
    ok: true,
    message: res.message ?? "Leader group berhasil ditetapkan.",
    groups,
  };
}

export async function submitSetGroupAccess(
  adminService: AdminService,
  groupIdRaw: string,
  dbIdRaw: string,
  accessLevel: AccessLevel
): Promise<
  { ok: true; message: string; groups: GroupDetail[] } | { ok: false; error: string }
> {
  const res = await adminService.setGroupDbAccess(
    Number.parseInt(groupIdRaw, 10),
    Number.parseInt(dbIdRaw, 10),
    accessLevel
  );
  if (!res.ok) {
    return { ok: false, error: res.error };
  }
  const groups = await adminService.listGroups();
  return {
    ok: true,
    message: res.message ?? "Hak akses berhasil disimpan.",
    groups,
  };
}

export async function submitRegisterDatabase(
  adminService: AdminService,
  input: RegisterTargetDBInput
): Promise<
  | { ok: true; message: string; databases: TargetDBInfo[] }
  | { ok: false; error: string }
> {
  const res = await adminService.registerDatabase(input);
  if (!res.ok) {
    return { ok: false, error: res.error };
  }
  const databases = await adminService.listDatabases();
  return {
    ok: true,
    message: res.message ?? "Database berhasil didaftarkan.",
    databases,
  };
}
