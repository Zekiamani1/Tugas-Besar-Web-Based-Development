"use client";

import { useContext, useState } from "react";
import { AlertMessage } from "@/components/ui/AlertMessage";
import { Card } from "@/components/ui/Card";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { ServicesContext } from "@/lib/api/context";
import type { MigrationRecord } from "@/lib/types";
import {
  applyCompletedMigration,
  resolveSelectedMigration,
} from "./logic";

function replaceUrl(url: string) {
  if (typeof window !== "undefined") {
    window.history.replaceState(null, "", url);
  }
}

export function MigrationManager({
  dbId,
  canEdit,
  initialDrafts,
  initialHistory,
  initialSelectedId,
}: {
  dbId: number;
  canEdit: boolean;
  initialDrafts: MigrationRecord[];
  initialHistory: MigrationRecord[];
  initialSelectedId?: number;
}) {
  const { migrations: migrationService } = useContext(ServicesContext);
  const initialMigration = resolveSelectedMigration({
    canEdit,
    drafts: initialDrafts,
    history: initialHistory,
    selectedId: initialSelectedId ?? null,
  });

  const [drafts, setDrafts] = useState<MigrationRecord[]>(initialDrafts);
  const [history, setHistory] = useState<MigrationRecord[]>(initialHistory);
  const [selectedId, setSelectedId] = useState<number | null>(
    initialMigration?.id ?? null
  );
  const [draftSql, setDraftSql] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);

  const [editedSql, setEditedSql] = useState(initialMigration?.sqlQuery ?? "");
  const [explainPlan, setExplainPlan] = useState<string | null>(null);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [detailError, setDetailError] = useState<string | null>(null);
  const [detailSuccess, setDetailSuccess] = useState<string | null>(null);

  const selectedMigration = resolveSelectedMigration({
    canEdit,
    drafts,
    history,
    selectedId,
  });

  function handleSelectMigration(migrationId: number) {
    const visible = canEdit ? [...drafts, ...history] : history;
    const next = visible.find((m) => m.id === migrationId);
    setSelectedId(migrationId);
    setEditedSql(next?.sqlQuery ?? "");
    setExplainPlan(null);
    setConfirmOpen(false);
    setDetailError(null);
    setDetailSuccess(null);
    replaceUrl(`/databases/${dbId}/migrations?selected=${migrationId}`);
  }

  async function handleCreateDraft() {
    if (!migrationService) return;
    const res = await migrationService.createDraft(dbId, draftSql);
    if (!res.ok) {
      setCreateError(res.error);
      return;
    }
    setCreateError(null);
    setDraftSql("");
    if (res.data) {
      const created = res.data;
      setDrafts((prev) => [...prev, created]);
      setSelectedId(created.id);
      setEditedSql(created.sqlQuery);
      setExplainPlan(null);
      setConfirmOpen(false);
      setDetailError(null);
      setDetailSuccess(null);
      replaceUrl(`/databases/${dbId}/migrations?selected=${created.id}`);
    }
  }

  async function handleSaveDraft() {
    if (!selectedMigration || !migrationService) return;
    const res = await migrationService.updateDraft(
      dbId,
      selectedMigration.id,
      editedSql
    );
    if (!res.ok) {
      setDetailError(res.error);
      setDetailSuccess(null);
      return;
    }
    if (res.data) {
      const updated = res.data;
      setDrafts((prev) => prev.map((d) => (d.id === updated.id ? updated : d)));
    }
    setDetailError(null);
    setDetailSuccess(res.message ?? "Draft berhasil disimpan.");
  }

  async function handleExplain() {
    if (!selectedMigration || !migrationService) return;
    const res = await migrationService.explainDraft(dbId, selectedMigration.id);
    if (!res.ok) {
      setDetailError(res.error);
      setDetailSuccess(null);
      setExplainPlan(null);
      return;
    }
    setDetailError(null);
    setDetailSuccess(null);
    setExplainPlan(res.data ?? "");
  }

  async function handleExecuteConfirmed() {
    if (!selectedMigration || !migrationService) return;
    const res = await migrationService.executeDraft(dbId, selectedMigration.id);
    setConfirmOpen(false);

    if (res.data) {
      const completed = res.data;
      const next = applyCompletedMigration({ drafts, history }, completed);
      setDrafts(next.drafts);
      setHistory(next.history);
    }

    if (!res.ok) {
      setDetailError(res.error);
      setDetailSuccess(null);
      return;
    }
    setDetailError(null);
    setDetailSuccess(res.message ?? "Migration berhasil dijalankan.");
  }

  return (
    <>
      {canEdit ? (
        <>
          <Card title="Create Draft">
            <form
              onSubmit={(event) => {
                event.preventDefault();
                void handleCreateDraft();
              }}
              noValidate
            >
              <label htmlFor="draft-sql">SQL Query</label>
              <textarea
                id="draft-sql"
                name="sql_query"
                value={draftSql}
                onChange={(event) => setDraftSql(event.target.value)}
                placeholder="CREATE TABLE ..."
                required
              />
              <AlertMessage variant="error" message={createError} />
              <button type="submit">Create Draft</button>
            </form>
          </Card>

          <Card title="Drafts">
            <ul className="item-list">
              {drafts.length > 0 ? (
                drafts.map((draft) => {
                  const isActive = selectedMigration?.id === draft.id;
                  return (
                    <li
                      key={draft.id}
                      className={isActive ? "item-active" : undefined}
                    >
                      <button
                        type="button"
                        className="selectable-link"
                        onClick={() => handleSelectMigration(draft.id)}
                      >
                        {`Draft #${draft.id}`}
                      </button>{" "}
                      <StatusBadge status={draft.status} />
                      <pre className="sql-preview">{draft.sqlQuery}</pre>
                    </li>
                  );
                })
              ) : (
                <li className="empty-state">No drafts yet.</li>
              )}
            </ul>
          </Card>
        </>
      ) : null}

      <Card title="History">
        <ul className="item-list">
          {history.length > 0 ? (
            history.map((item) => {
              const isActive = selectedMigration?.id === item.id;
              return (
                <li
                  key={item.id}
                  className={isActive ? "item-active" : undefined}
                >
                  <button
                    type="button"
                    className="selectable-link"
                    onClick={() => handleSelectMigration(item.id)}
                  >
                    {`Migration #${item.id}`}
                  </button>{" "}
                  <StatusBadge status={item.status} />
                  <br />
                  <span className="muted">
                    Version: {item.version} · Executed by: {item.executedBy} ·
                    Executed at: {item.executedAt}
                  </span>
                  <pre className="sql-preview">{item.sqlQuery}</pre>
                </li>
              );
            })
          ) : (
            <li className="empty-state">Belum ada riwayat migration.</li>
          )}
        </ul>
      </Card>

      {selectedMigration ? (
        <Card title={`Migration #${selectedMigration.id}`}>
          <AlertMessage variant="error" message={detailError} />
          <AlertMessage variant="success" message={detailSuccess} />

          <p>
            Version: {selectedMigration.version} ·{" "}
            <StatusBadge status={selectedMigration.status} />
          </p>
          {selectedMigration.executedBy ? (
            <p>
              ExecutedBy: {selectedMigration.executedBy}
              {selectedMigration.status !== "DRAFT"
                ? ` · ExecutedAt: ${selectedMigration.executedAt}`
                : null}
            </p>
          ) : null}

          <h3>SQL Query</h3>
          <pre className="sql-preview">{selectedMigration.sqlQuery}</pre>

          {canEdit && selectedMigration.status === "DRAFT" ? (
            <>
              <h3>Edit Draft</h3>
              <form
                onSubmit={(event) => {
                  event.preventDefault();
                  void handleSaveDraft();
                }}
                noValidate
              >
                <label htmlFor="edit-sql">SQL Query</label>
                <textarea
                  id="edit-sql"
                  name="sql_query"
                  value={editedSql}
                  onChange={(event) => setEditedSql(event.target.value)}
                  required
                />
                <button type="submit">Save Draft</button>
              </form>

              <h3>Query Plan</h3>
              <p className="muted">
                Lihat estimasi rencana PostgreSQL. Query tidak dijalankan.
              </p>
              <button type="button" onClick={() => void handleExplain()}>
                Lihat Query Plan
              </button>
              {explainPlan ? (
                <pre className="sql-preview">{explainPlan}</pre>
              ) : null}

              <h3>Execute Migration</h3>
              <p className="muted">
                SQL di atas akan dijalankan pada database target. Pastikan isinya
                sudah benar.
              </p>
              {!confirmOpen ? (
                <button
                  type="button"
                  className="button-danger"
                  onClick={() => setConfirmOpen(true)}
                >
                  Execute
                </button>
              ) : (
                <div
                  className="confirm-box"
                  role="region"
                  aria-label="Konfirmasi eksekusi migration"
                >
                  <p>Jalankan migration ini? Pastikan SQL sudah benar.</p>
                  <div className="inline-actions">
                    <button
                      type="button"
                      className="button-danger"
                      onClick={() => void handleExecuteConfirmed()}
                    >
                      Konfirmasi Execute
                    </button>
                    <button
                      type="button"
                      className="button-subtle"
                      onClick={() => setConfirmOpen(false)}
                    >
                      Batal
                    </button>
                  </div>
                </div>
              )}
            </>
          ) : null}
        </Card>
      ) : null}
    </>
  );
}
