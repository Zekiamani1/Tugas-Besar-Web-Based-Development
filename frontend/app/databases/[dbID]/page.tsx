import type { Metadata } from "next";
import Link from "next/link";
import { AlertMessage } from "@/components/ui/AlertMessage";
import { Card } from "@/components/ui/Card";
import { PageLayout } from "@/components/ui/PageLayout";
import {
  DatabaseExplorer,
  type ExplorerTab,
} from "@/features/databases/DatabaseExplorer";
import { EMPTY_TABLE_DATA } from "@/features/databases/logic";
import type { DatabaseExplorerInitialData } from "@/lib/types";

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

  const schema = firstParam(resolvedSearch.schema);
  const table = firstParam(resolvedSearch.table);
  const rawTab = firstParam(resolvedSearch.tab);
  const tab: ExplorerTab = rawTab === "data" ? "data" : "structure";

  const initialData: DatabaseExplorerInitialData = {
    schemas: [],
    activeSchema: schema ?? "",
    tables: [],
    activeTable: table ?? "",
    activeTab: tab,
    metadata: null,
    tableData: EMPTY_TABLE_DATA,
  };

  return (
    <PageLayout
      subtitle={`Database #${parsedDbId}`}
      title="Database Explorer"
      nav={
        <>
          <Link href="/dashboard">Dashboard</Link>
          <Link href={`/databases/${parsedDbId}/migrations`}>Migrations</Link>
        </>
      }
    >
      <DatabaseExplorer dbId={parsedDbId} initialData={initialData} />
    </PageLayout>
  );
}
