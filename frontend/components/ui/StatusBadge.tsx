import type { AccessLevel, BadgeStatus } from "@/lib/types";

export function StatusBadge({ status }: { status: BadgeStatus }) {
  return (
    <span className="status" data-status={status}>
      {status}
    </span>
  );
}

export function formatAccessLevel(level: AccessLevel): string {
  return level === "VIEW_EDIT" ? "View + Edit" : "View Only";
}
