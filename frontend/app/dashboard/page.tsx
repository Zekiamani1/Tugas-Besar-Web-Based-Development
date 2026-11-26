import type { Metadata } from "next";
import { redirect } from "next/navigation";
import { PageLayout } from "@/components/ui/PageLayout";
import { AccountNav } from "@/features/auth/AccountNav";
import { DatabaseList } from "@/features/dashboard/DatabaseList";
import { ApiError } from "@/lib/api/http";
import { loadDashboardData } from "@/lib/api/server/dashboard";
import { getServerSessionContext } from "@/lib/api/server/session";
import type { TargetDBInfo } from "@/lib/types";

export const dynamic = "force-dynamic";

export const metadata: Metadata = {
  title: "Dashboard · Database Manager",
};

export default async function DashboardPage() {
  const { user } = await getServerSessionContext();

  let redirectTarget: string | null = null;
  let databases: TargetDBInfo[] = [];

  try {
    ({ databases } = await loadDashboardData());
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) {
      redirectTarget = "/login?reason=session";
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
      title="Dashboard"
      nav={<AccountNav user={user} showAdminLink />}
    >
      <DatabaseList databases={databases} />
    </PageLayout>
  );
}
