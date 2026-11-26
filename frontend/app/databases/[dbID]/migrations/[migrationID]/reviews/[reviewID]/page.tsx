import type { Metadata } from "next";
import Link from "next/link";
import { redirect } from "next/navigation";
import { AlertMessage } from "@/components/ui/AlertMessage";
import { Card } from "@/components/ui/Card";
import { PageLayout } from "@/components/ui/PageLayout";
import { AccountNav } from "@/features/auth/AccountNav";
import { MigrationReviewPanel } from "@/features/migrations/MigrationReviewPanel";
import { ApiError } from "@/lib/api/http";
import { loadMigrationReviewData } from "@/lib/api/server/migrations";
import { getServerSessionContext } from "@/lib/api/server/session";
import type { MigrationJob, MigrationReview } from "@/lib/types";

export const dynamic = "force-dynamic";

export const metadata: Metadata = {
  title: "Migration Review · Database Manager",
};

type ParamsPromise = Promise<{
  dbID: string;
  migrationID: string;
  reviewID: string;
}>;
type SearchParamsPromise = Promise<Record<string, string | string[] | undefined>>;

export default async function MigrationReviewPage({
  params,
  searchParams,
}: {
  params: ParamsPromise;
  searchParams: SearchParamsPromise;
}) {
  const [{ dbID, migrationID, reviewID }, resolvedSearch, { user }] =
    await Promise.all([params, searchParams, getServerSessionContext()]);

  const parsedDbId = /^\d+$/.test(dbID) ? Number.parseInt(dbID, 10) : 0;
  const parsedMigrationId = /^\d+$/.test(migrationID)
    ? Number.parseInt(migrationID, 10)
    : 0;
  const parsedReviewId = /^\d+$/.test(reviewID)
    ? Number.parseInt(reviewID, 10)
    : 0;

  if (parsedDbId <= 0 || parsedMigrationId <= 0 || parsedReviewId <= 0) {
    return (
      <PageLayout
        subtitle="Database Manager"
        title="Review Tidak Valid"
        nav={
          <>
            <Link href="/dashboard">Dashboard</Link>
            <AccountNav user={user} />
          </>
        }
      >
        <Card>
          <AlertMessage
            variant="error"
            message="Parameter database, migration, atau review tidak valid."
          />
          <p>
            <Link href="/dashboard">Kembali ke Dashboard</Link>
          </p>
        </Card>
      </PageLayout>
    );
  }

  const rawJobId = Array.isArray(resolvedSearch.job_id)
    ? resolvedSearch.job_id[0]
    : resolvedSearch.job_id;
  const jobId =
    rawJobId && /^\d+$/.test(rawJobId)
      ? Number.parseInt(rawJobId, 10)
      : undefined;

  let redirectTarget: string | null = null;
  let notFound = false;
  let review: MigrationReview | null = null;
  let initialJob: MigrationJob | null = null;

  try {
    ({ review, initialJob } = await loadMigrationReviewData(
      parsedDbId,
      parsedMigrationId,
      parsedReviewId,
      { jobId }
    ));
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) {
      redirectTarget = "/login?reason=session";
    } else if (err instanceof ApiError && err.status === 403) {
      redirectTarget = "/forbidden?reason=database";
    } else if (err instanceof ApiError && err.status === 404) {
      notFound = true;
    } else {
      throw err;
    }
  }

  if (redirectTarget) {
    redirect(redirectTarget);
  }

  if (notFound || !review) {
    return (
      <PageLayout
        subtitle={`Database #${parsedDbId} · Migration #${parsedMigrationId}`}
        title={`Review #${parsedReviewId}`}
        nav={
          <>
            <Link href="/dashboard">Dashboard</Link>
            <Link href={`/databases/${parsedDbId}/migrations`}>Migrations</Link>
            <AccountNav user={user} />
          </>
        }
      >
        <Card>
          <AlertMessage
            variant="error"
            message="Migration review tidak ditemukan."
          />
          <p>
            <Link href={`/databases/${parsedDbId}/migrations`}>
              Kembali ke Migrations
            </Link>
          </p>
        </Card>
      </PageLayout>
    );
  }

  return (
    <PageLayout
      subtitle={`Database #${parsedDbId} · Migration #${parsedMigrationId}`}
      title={`Review #${parsedReviewId}`}
      nav={
        <>
          <Link href="/dashboard">Dashboard</Link>
          <Link href={`/databases/${parsedDbId}`}>Browse data</Link>
          <Link href={`/databases/${parsedDbId}/migrations`}>Migrations</Link>
          <AccountNav user={user} />
        </>
      }
    >
      <MigrationReviewPanel
        dbId={parsedDbId}
        initialReview={review}
        initialJob={initialJob}
        currentUserId={user?.id ?? null}
      />
    </PageLayout>
  );
}
