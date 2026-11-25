import type { Metadata } from "next";
import Link from "next/link";
import { redirect } from "next/navigation";
import { AlertMessage } from "@/components/ui/AlertMessage";
import { Card } from "@/components/ui/Card";
import { PageLayout } from "@/components/ui/PageLayout";
import { AccountNav } from "@/features/auth/AccountNav";
import { MigrationManager } from "@/features/migrations/MigrationManager";
import { ApiError } from "@/lib/api/http";
import { loadMigrationsData } from "@/lib/api/server/migrations";
import { getServerSessionContext } from "@/lib/api/server/session";
import type { MigrationRecord } from "@/lib/types";

export const dynamic = "force-dynamic";

export const metadata: Metadata = {
  title: "Migrations · Database Manager",
};

type ParamsPromise = Promise<{ dbID: string }>;
type SearchParamsPromise = Promise<Record<string, string | string[] | undefined>>;

export default async function MigrationsPage({
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

  const rawSelected = Array.isArray(resolvedSearch.selected)
    ? resolvedSearch.selected[0]
    : resolvedSearch.selected;
  const selectedId =
    rawSelected && /^\d+$/.test(rawSelected)
      ? Number.parseInt(rawSelected, 10)
      : undefined;

  let redirectTarget: string | null = null;
  let canEdit = false;
  let drafts: MigrationRecord[] = [];
  let history: MigrationRecord[] = [];

  try {
    ({ canEdit, drafts, history } = await loadMigrationsData(parsedDbId));
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) {
      redirectTarget = "/login?reason=session";
    } else if (err instanceof ApiError && err.status === 403) {
      redirectTarget = "/forbidden?reason=database";
    } else {
      throw err;
    }
  }

  if (redirectTarget) {
    redirect(redirectTarget);
  }

  return (
    <PageLayout
      subtitle={`Database #${parsedDbId}`}
      title="Migrations"
      nav={
        <>
          <Link href="/dashboard">Dashboard</Link>
          <Link href={`/databases/${parsedDbId}`}>Browse data</Link>
          <AccountNav user={user} />
        </>
      }
    >
      <MigrationManager
        dbId={parsedDbId}
        canEdit={canEdit}
        initialDrafts={drafts}
        initialHistory={history}
        initialSelectedId={selectedId}
      />
    </PageLayout>
  );
}
