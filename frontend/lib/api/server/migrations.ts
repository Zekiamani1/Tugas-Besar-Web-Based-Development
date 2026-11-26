import { sortMigrationHistory } from "@/features/migrations/logic";
import type { MigrationRecord } from "@/lib/types";
import { getApiBaseUrl, requestJson } from "../http";
import { getServerSessionContext } from "./session";

export async function loadMigrationsData(
  dbId: number,
  cookieHeader?: string,
  baseUrl = getApiBaseUrl()
): Promise<{
  canEdit: boolean;
  drafts: MigrationRecord[];
  history: MigrationRecord[];
}> {
  const normalizedBaseUrl = baseUrl.replace(/\/+$/, "");
  const resolvedCookie =
    cookieHeader ?? (await getServerSessionContext()).cookieHeader;
  const res = await requestJson<{
    migrations?: MigrationRecord[];
    drafts?: MigrationRecord[];
    can_edit?: boolean;
  }>(`${normalizedBaseUrl}/databases/${dbId}/migrations`, {
    cache: "no-store",
    cookieHeader: resolvedCookie,
  });

  return {
    canEdit: Boolean(res.can_edit),
    drafts: Array.isArray(res.drafts) ? res.drafts : [],
    history: sortMigrationHistory(
      Array.isArray(res.migrations) ? res.migrations : []
    ),
  };
}
