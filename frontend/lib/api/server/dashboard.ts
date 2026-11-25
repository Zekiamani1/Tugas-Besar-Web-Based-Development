import type { TargetDBInfo } from "@/lib/types";
import { getApiBaseUrl, requestJson } from "../http";
import { getServerSessionContext } from "./session";

export async function loadDashboardData(
  cookieHeader?: string,
  baseUrl = getApiBaseUrl()
): Promise<{ databases: TargetDBInfo[] }> {
  const normalizedBaseUrl = baseUrl.replace(/\/+$/, "");
  const resolvedCookie =
    cookieHeader ?? (await getServerSessionContext()).cookieHeader;

  const listRes = await requestJson<{ databases?: TargetDBInfo[] }>(
    `${normalizedBaseUrl}/databases`,
    {
      cache: "no-store",
      cookieHeader: resolvedCookie,
    }
  );

  return {
    databases: Array.isArray(listRes.databases) ? listRes.databases : [],
  };
}
