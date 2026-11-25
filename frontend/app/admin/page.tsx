import type { Metadata } from "next";
import Link from "next/link";
import { redirect } from "next/navigation";
import { PageLayout } from "@/components/ui/PageLayout";
import { AdminPanel, type AdminTab } from "@/features/admin/AdminPanel";
import { AccountNav } from "@/features/auth/AccountNav";
import { ApiError } from "@/lib/api/http";
import { loadAdminData } from "@/lib/api/server/admin";
import { getServerSessionContext } from "@/lib/api/server/session";
import type { GroupDetail, TargetDBInfo, UserSummary } from "@/lib/types";

export const dynamic = "force-dynamic";

export const metadata: Metadata = {
  title: "Admin · Database Manager",
};

type SearchParamsPromise = Promise<Record<string, string | string[] | undefined>>;

const VALID_TABS: ReadonlySet<AdminTab> = new Set([
  "manage",
  "users",
  "groups",
  "databases",
]);

function parseAdminTab(raw: string | string[] | undefined): AdminTab {
  const value = Array.isArray(raw) ? raw[0] : raw;
  if (value && VALID_TABS.has(value as AdminTab)) {
    return value as AdminTab;
  }
  return "manage";
}

export default async function AdminPage({
  searchParams,
}: {
  searchParams: SearchParamsPromise;
}) {
  const [resolved, { user }] = await Promise.all([
    searchParams,
    getServerSessionContext(),
  ]);

  let redirectTarget: string | null = null;
  let users: UserSummary[] = [];
  let groups: GroupDetail[] = [];
  let databases: TargetDBInfo[] = [];

  try {
    ({ users, groups, databases } = await loadAdminData());
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) {
      redirectTarget = "/login?reason=session";
    } else if (err instanceof ApiError && err.status === 403) {
      redirectTarget = "/forbidden?reason=admin";
    } else {
      throw err;
    }
  }

  if (redirectTarget) {
    redirect(redirectTarget);
  }

  return (
    <PageLayout
      subtitle="Database Manager"
      title="Admin Panel"
      nav={
        <>
          <Link href="/dashboard">Dashboard</Link>
          <AccountNav user={user} />
        </>
      }
    >
      <AdminPanel
        initialTab={parseAdminTab(resolved.tab)}
        initialUsers={users}
        initialGroups={groups}
        initialDatabases={databases}
      />
    </PageLayout>
  );
}
