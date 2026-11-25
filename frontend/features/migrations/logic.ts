import type { MigrationRecord } from "@/lib/types";

export function sortMigrationHistory(
  items: ReadonlyArray<MigrationRecord>
): MigrationRecord[] {
  return [...items].sort((a, b) => {
    if (a.executedAt !== b.executedAt) {
      return a.executedAt < b.executedAt ? -1 : 1;
    }
    return a.id - b.id;
  });
}

export function resolveSelectedMigration({
  canEdit,
  drafts,
  history,
  selectedId,
}: {
  canEdit: boolean;
  drafts: ReadonlyArray<MigrationRecord>;
  history: ReadonlyArray<MigrationRecord>;
  selectedId: number | null;
}): MigrationRecord | null {
  const visible = canEdit ? [...drafts, ...history] : history;
  if (selectedId !== null) {
    const found = visible.find((m) => m.id === selectedId);
    if (found) return found;
  }
  if (canEdit && drafts.length > 0) {
    return drafts[0];
  }
  if (history.length > 0) {
    return history[history.length - 1];
  }
  return null;
}

export function applyCompletedMigration(
  state: {
    drafts: ReadonlyArray<MigrationRecord>;
    history: ReadonlyArray<MigrationRecord>;
  },
  completed: MigrationRecord
): {
  drafts: MigrationRecord[];
  history: MigrationRecord[];
} {
  if (completed.status === "DRAFT") {
    return {
      drafts: [...state.drafts],
      history: [...state.history],
    };
  }
  return {
    drafts: state.drafts.filter((d) => d.id !== completed.id),
    history: sortMigrationHistory([
      ...state.history.filter((h) => h.id !== completed.id),
      completed,
    ]),
  };
}
