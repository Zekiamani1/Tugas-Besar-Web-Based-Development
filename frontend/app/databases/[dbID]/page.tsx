import type { Metadata } from "next";
import Link from "next/link";
import { redirect } from "next/navigation";
import { AlertMessage } from "@/components/ui/AlertMessage";
import { Card } from "@/components/ui/Card";
import { PageLayout } from "@/components/ui/PageLayout";
import { AccountNav } from "@/features/auth/AccountNav";
import {
  DatabaseExplorer,
  type ExplorerTab,
} from "@/features/databases/DatabaseExplorer";
import { ApiError } from "@/lib/api/http";
import { loadDatabaseExplorerData } from "@/lib/api/server/databases";
import { getServerSessionContext } from "@/lib/api/server/session";
import type { DatabaseExplorerInitialData } from "@/lib/types";

export const dynamic = "force-dynamic";

export const metadata: Metadata = {
  title: "Database Explorer · Database Manager",
};

type ParamsPromise = Promise<{ dbID: string }>;
type SearchParamsPromise = Promise<Record<string, string | string[] | undefined>>;

function firstParam(value: string | string[] | undefined): string | undefined {
  return Array.isArray(value) ? value[0] : value;
}

export default async function DatabaseExplorerPage({
  params,
  searchParams,
}: {
  params: ParamsPromise;
  searchParams: SearchParamsPromise;
}) {
  const [{ dbID }, resolvedSearch, { user }] = await Promise.all([
    params,
    searchParams,
    getServerSessionContext(),
  ]);
  const isValidId = /^\d+$/.test(dbID);
  const parsedDbId = isValidId ? Number.parseInt(dbID, 10) : 0;

  if (!isValidId || parsedDbId <= 0) {
    return (
      <PageLayout
        subtitle="Database Manager"
        title="Database Tidak Valid"
        nav={
          <>
            <Link href="/dashboard">Dashboard</Link>
            <AccountNav user={user} />
          </>
        }
      >
        <Card>
          <AlertMessage variant="error" message="ID database tidak valid." />
          <p>
            <Link href="/dashboard">Kembali ke Dashboard</Link>
          </p>
        </Card>
      </PageLayout>
    );
  }

  const schema = firstParam(resolvedSearch.schema);
  const table = firstParam(resolvedSearch.table);
  const rawTab = firstParam(resolvedSearch.tab);
  const tab: ExplorerTab = rawTab === "data" ? "data" : "structure";

  let redirectTarget: string | null = null;
  let initialData: DatabaseExplorerInitialData | null = null;

  try {
    initialData = await loadDatabaseExplorerData(parsedDbId, {
      schema,
      table,
      tab,
    });
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) {
      redirectTarget = "/login?reason=session";
    } else if (err instanceof ApiError && err.status === 403) {
      redirectTarget = "/forbidden?reason=database";
    } else {
      throw err;
    }
  }

  if (redirectTarget || !initialData) {
    redirect(redirectTarget ?? "/forbidden?reason=database");
  }

  return (
    <PageLayout
      subtitle={`Database #${parsedDbId}`}
      title="Database Explorer"
      nav={
        <>
          <Link href="/dashboard">Dashboard</Link>
          <Link href={`/databases/${parsedDbId}/migrations`}>Migrations</Link>
          <AccountNav user={user} />
        </>
      }
    >
      <DatabaseExplorer dbId={parsedDbId} initialData={initialData} />
    </PageLayout>
  );
}
