import type { AccessLevel, MigrationStatus } from "@/lib/types";

export function StatusBadge({ status }: { status: MigrationStatus }) {
  return (
    <span className="status" data-status={status}>
      {status}
    </span>
  );
}

export function formatAccessLevel(level: AccessLevel): string {
  return level === "VIEW_EDIT" ? "View + Edit" : "View Only";
}
