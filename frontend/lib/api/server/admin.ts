import type { GroupDetail, TargetDBInfo, UserSummary } from "@/lib/types";
import { getApiBaseUrl, requestJson } from "../http";
import { getServerSessionContext } from "./session";

export async function loadAdminData(
  cookieHeader?: string,
  baseUrl = getApiBaseUrl()
): Promise<{
  users: UserSummary[];
  groups: GroupDetail[];
  databases: TargetDBInfo[];
}> {
  const normalizedBaseUrl = baseUrl.replace(/\/+$/, "");
  const resolvedCookie =
    cookieHeader ?? (await getServerSessionContext()).cookieHeader;

  const [usersRes, groupsRes, dbsRes] = await Promise.all([
    requestJson<{ users?: UserSummary[] }>(`${normalizedBaseUrl}/admin/users`, {
      cache: "no-store",
      cookieHeader: resolvedCookie,
    }),
    requestJson<{ groups?: GroupDetail[] }>(
      `${normalizedBaseUrl}/admin/groups`,
      {
        cache: "no-store",
        cookieHeader: resolvedCookie,
      }
    ),
    requestJson<{ databases?: TargetDBInfo[] }>(
      `${normalizedBaseUrl}/admin/databases`,
      {
        cache: "no-store",
        cookieHeader: resolvedCookie,
      }
    ),
  ]);

  return {
    users: Array.isArray(usersRes.users) ? usersRes.users : [],
    groups: Array.isArray(groupsRes.groups) ? groupsRes.groups : [],
    databases: Array.isArray(dbsRes.databases) ? dbsRes.databases : [],
  };
}
