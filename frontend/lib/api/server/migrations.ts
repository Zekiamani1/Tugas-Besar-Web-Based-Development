import {
  normalizeJob,
  normalizePosition,
  normalizeReview,
  sortMigrationHistory,
} from "@/features/migrations/logic";
import type {
  MigrationJob,
  MigrationPosition,
  MigrationRecord,
  MigrationReview,
} from "@/lib/types";
import { getApiBaseUrl, requestJson } from "../http";
import { getServerSessionContext } from "./session";

export async function loadMigrationsData(
  dbId: number,
  options: {
    cookieHeader?: string;
    baseUrl?: string;
    hideRollback?: boolean;
    jobId?: number;
    selectedId?: number;
  } = {}
): Promise<{
  canEdit: boolean;
  drafts: MigrationRecord[];
  history: MigrationRecord[];
  position: MigrationPosition;
  pendingReviews: MigrationReview[];
  activeJobs: MigrationJob[];
  initialJob: MigrationJob | null;
}> {
  const normalizedBaseUrl = (options.baseUrl ?? getApiBaseUrl()).replace(
    /\/+$/,
    ""
  );
  const resolvedCookie =
    options.cookieHeader ?? (await getServerSessionContext()).cookieHeader;
  const suffix = options.hideRollback ? "?hide_rollback=true" : "";

  const mainPromise = requestJson<{
    migrations?: MigrationRecord[];
    drafts?: MigrationRecord[];
    can_edit?: boolean;
  }>(`${normalizedBaseUrl}/databases/${dbId}/migrations${suffix}`, {
    cache: "no-store",
    cookieHeader: resolvedCookie,
  });

  const positionPromise = requestJson<unknown>(
    `${normalizedBaseUrl}/databases/${dbId}/migrations/position`,
    {
      cache: "no-store",
      cookieHeader: resolvedCookie,
    }
  )
    .then(normalizePosition)
    .catch((): MigrationPosition => ({ activeHead: null, nextForward: null }));

  const reviewsPromise = requestJson<unknown>(
    `${normalizedBaseUrl}/databases/${dbId}/migration-reviews`,
    {
      cache: "no-store",
      cookieHeader: resolvedCookie,
    }
  )
    .then((raw) => (Array.isArray(raw) ? raw.map(normalizeReview) : []))
    .catch((): MigrationReview[] => []);

  const activeJobsPromise = requestJson<unknown>(
    `${normalizedBaseUrl}/databases/${dbId}/migrations/jobs`,
    {
      cache: "no-store",
      cookieHeader: resolvedCookie,
    }
  )
    .then((raw) => (Array.isArray(raw) ? raw.map(normalizeJob) : []))
    .catch((): MigrationJob[] => []);

  const jobPromise =
    options.jobId && options.selectedId
      ? requestJson<unknown>(
          `${normalizedBaseUrl}/databases/${dbId}/migrations/${options.selectedId}?job_id=${options.jobId}`,
          {
            cache: "no-store",
            cookieHeader: resolvedCookie,
          }
        )
          .then(normalizeJob)
          .catch(() => null)
      : Promise.resolve(null);

  const [res, position, pendingReviews, activeJobs, initialJob] =
    await Promise.all([
      mainPromise,
      positionPromise,
      reviewsPromise,
      activeJobsPromise,
      jobPromise,
    ]);

  return {
    canEdit: Boolean(res.can_edit),
    drafts: Array.isArray(res.drafts) ? res.drafts : [],
    history: sortMigrationHistory(
      Array.isArray(res.migrations) ? res.migrations : []
    ),
    position,
    pendingReviews,
    activeJobs,
    initialJob,
  };
}

export async function loadMigrationReviewData(
  dbId: number,
  migrationId: number,
  reviewId: number,
  options: {
    cookieHeader?: string;
    baseUrl?: string;
    jobId?: number;
  } = {}
): Promise<{
  review: MigrationReview;
  initialJob: MigrationJob | null;
}> {
  const normalizedBaseUrl = (options.baseUrl ?? getApiBaseUrl()).replace(
    /\/+$/,
    ""
  );
  const resolvedCookie =
    options.cookieHeader ?? (await getServerSessionContext()).cookieHeader;

  const [rawReview, initialJob] = await Promise.all([
    requestJson<unknown>(
      `${normalizedBaseUrl}/databases/${dbId}/migrations/${migrationId}/reviews/${reviewId}`,
      {
        cache: "no-store",
        cookieHeader: resolvedCookie,
      }
    ),
    options.jobId
      ? requestJson<unknown>(
          `${normalizedBaseUrl}/databases/${dbId}/migrations/${migrationId}?job_id=${options.jobId}`,
          {
            cache: "no-store",
            cookieHeader: resolvedCookie,
          }
        )
          .then(normalizeJob)
          .catch(() => null)
      : Promise.resolve(null),
  ]);

  return {
    review: normalizeReview(rawReview),
    initialJob,
  };
}
