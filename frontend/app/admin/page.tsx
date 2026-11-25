import type { Metadata } from "next";
import Link from "next/link";
import { PageLayout } from "@/components/ui/PageLayout";
import { AdminPanel, type AdminTab } from "@/features/admin/AdminPanel";

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
  const resolved = await searchParams;

  return (
    <PageLayout
      subtitle="Database Manager"
      title="Admin Panel"
      nav={<Link href="/dashboard">Dashboard</Link>}
    >
      <AdminPanel
        initialTab={parseAdminTab(resolved.tab)}
        initialUsers={[]}
        initialGroups={[]}
        initialDatabases={[]}
      />
    </PageLayout>
  );
}
