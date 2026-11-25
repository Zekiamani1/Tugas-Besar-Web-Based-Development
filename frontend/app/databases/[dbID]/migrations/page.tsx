import type { Metadata } from "next";
import Link from "next/link";
import { AlertMessage } from "@/components/ui/AlertMessage";
import { Card } from "@/components/ui/Card";
import { PageLayout } from "@/components/ui/PageLayout";
import { MigrationManager } from "@/features/migrations/MigrationManager";

export const metadata: Metadata = {
  title: "Migrations · Database Manager",
};

type ParamsPromise = Promise<{ dbID: string }>;
type SearchParamsPromise = Promise<Record<string, string | string[] | undefined>>;

function firstParam(value: string | string[] | undefined): string | undefined {
  return Array.isArray(value) ? value[0] : value;
}

export default async function MigrationsPage({
  params,
  searchParams,
}: {
  params: ParamsPromise;
  searchParams: SearchParamsPromise;
}) {
  const [{ dbID }, resolvedSearch] = await Promise.all([params, searchParams]);
  const isValidId = /^\d+$/.test(dbID);
  const parsedDbId = isValidId ? Number.parseInt(dbID, 10) : 0;

  if (!isValidId || parsedDbId <= 0) {
    return (
      <PageLayout
        subtitle="Database Manager"
        title="Database Tidak Valid"
        nav={<Link href="/dashboard">Dashboard</Link>}
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

  const rawSelected = firstParam(resolvedSearch.selected);
  const selectedId =
    rawSelected && /^\d+$/.test(rawSelected)
      ? Number.parseInt(rawSelected, 10)
      : undefined;

  return (
    <PageLayout
      subtitle={`Database #${parsedDbId}`}
      title="Migrations"
      nav={
        <>
          <Link href="/dashboard">Dashboard</Link>
          <Link href={`/databases/${parsedDbId}`}>Browse data</Link>
        </>
      }
    >
      <MigrationManager
        dbId={parsedDbId}
        canEdit={false}
        initialDrafts={[]}
        initialHistory={[]}
        initialSelectedId={selectedId}
      />
    </PageLayout>
  );
}
