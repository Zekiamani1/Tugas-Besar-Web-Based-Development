import type { Metadata } from "next";
import Link from "next/link";
import { AlertMessage } from "@/components/ui/AlertMessage";
import { Card } from "@/components/ui/Card";
import { PageLayout } from "@/components/ui/PageLayout";

export const metadata: Metadata = {
  title: "Akses Ditolak · Database Manager",
};

type SearchParamsPromise = Promise<Record<string, string | string[] | undefined>>;

export default async function ForbiddenPage({
  searchParams,
}: {
  searchParams: SearchParamsPromise;
}) {
  const resolved = await searchParams;
  const rawReason = resolved.reason;
  const reason = Array.isArray(rawReason) ? rawReason[0] : rawReason;
  const message =
    reason === "admin"
      ? "Akses ditolak. Halaman ini hanya dapat diakses oleh Platform Admin."
      : "Akses database ditolak atau database tidak ditemukan.";

  return (
    <PageLayout
      subtitle="Database Manager"
      title="Akses Ditolak"
      nav={<Link href="/dashboard">Dashboard</Link>}
    >
      <Card>
        <AlertMessage variant="error" message={message} />
        <p>
          <Link href="/dashboard">Kembali ke Dashboard</Link>
        </p>
      </Card>
    </PageLayout>
  );
}
