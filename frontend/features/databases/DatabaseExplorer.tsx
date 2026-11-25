"use client";

import { useContext, useRef, useState } from "react";
import { AlertMessage } from "@/components/ui/AlertMessage";
import { Card } from "@/components/ui/Card";
import { DataTable, type DataTableColumn } from "@/components/ui/DataTable";
import { Tabs, type TabItem } from "@/components/ui/Tabs";
import { ServicesContext } from "@/lib/api/context";
import type {
  DatabaseExplorerInitialData,
  ExplorerTab,
  TableDataResult,
  TableMetadata,
  TableRow,
} from "@/lib/types";
import {
  buildExplorerUrl,
  loadSchemaSelection,
  loadTableSelection,
} from "./logic";

export type { ExplorerTab };

const EXPLORER_TABS: ReadonlyArray<TabItem<ExplorerTab>> = [
  { id: "structure", label: "Table Details" },
  { id: "data", label: "Table Data" },
];

function replaceUrl(url: string) {
  if (typeof window !== "undefined") {
    window.history.replaceState(null, "", url);
  }
}

export function DatabaseExplorer({
  dbId,
  initialData,
}: {
  dbId: number;
  initialData: DatabaseExplorerInitialData;
}) {
  const { databases: databaseService } = useContext(ServicesContext);
  const [activeSchema, setActiveSchema] = useState<string>(
    initialData.activeSchema
  );
  const [tables, setTables] = useState<string[]>(initialData.tables);
  const [activeTable, setActiveTable] = useState<string>(
    initialData.activeTable
  );
  const [activeTab, setActiveTab] = useState<ExplorerTab>(
    initialData.activeTab
  );
  const [page, setPage] = useState<number>(initialData.tableData.page);
  const [metadata, setMetadata] = useState<TableMetadata | null>(
    initialData.metadata
  );
  const [tableData, setTableData] = useState<TableDataResult>(
    initialData.tableData
  );
  const [error, setError] = useState<string | null>(null);

  const requestSeq = useRef(0);

  async function handleSelectSchema(schema: string) {
    if (!databaseService) return;
    const seq = ++requestSeq.current;
    setError(null);
    try {
      const next = await loadSchemaSelection(databaseService, dbId, schema);
      if (seq !== requestSeq.current) return;

      setActiveSchema(schema);
      setTables(next.tables);
      setActiveTable(next.activeTable);
      setPage(1);
      setMetadata(next.metadata);
      setTableData(next.tableData);
      replaceUrl(buildExplorerUrl(dbId, schema, next.activeTable, activeTab));
    } catch {
      if (seq === requestSeq.current) {
        setError("Gagal memuat daftar tabel untuk schema ini.");
      }
    }
  }

  async function handleSelectTable(table: string, targetTab: ExplorerTab) {
    if (!databaseService) return;
    const seq = ++requestSeq.current;
    setError(null);
    try {
      const next = await loadTableSelection(
        databaseService,
        dbId,
        activeSchema,
        table
      );
      if (seq !== requestSeq.current) return;

      setActiveTable(table);
      setActiveTab(targetTab);
      setPage(1);
      setMetadata(next.metadata);
      setTableData(next.tableData);
      replaceUrl(buildExplorerUrl(dbId, activeSchema, table, targetTab));
    } catch {
      if (seq === requestSeq.current) {
        setError("Gagal memuat detail tabel.");
      }
    }
  }

  async function handleTabChange(nextTab: ExplorerTab) {
    const seq = ++requestSeq.current;
    setError(null);
    setActiveTab(nextTab);
    setPage(1);
    replaceUrl(buildExplorerUrl(dbId, activeSchema, activeTable, nextTab));

    if (!activeSchema || !activeTable || !databaseService) {
      return;
    }

    try {
      if (nextTab === "data") {
        const nextData = await databaseService.getTableData(
          dbId,
          activeSchema,
          activeTable,
          1,
          10
        );
        if (seq !== requestSeq.current) return;
        setTableData(nextData);
      } else {
        const nextMetadata = await databaseService.getTableMetadata(
          dbId,
          activeSchema,
          activeTable
        );
        if (seq !== requestSeq.current) return;
        setMetadata(nextMetadata);
      }
    } catch {
      if (seq === requestSeq.current) {
        setError("Gagal memuat tampilan tabel.");
      }
    }
  }

  async function handlePageChange(nextPage: number) {
    if (!activeSchema || !activeTable || !databaseService) {
      return;
    }
    const seq = ++requestSeq.current;
    setError(null);
    try {
      const nextData = await databaseService.getTableData(
        dbId,
        activeSchema,
        activeTable,
        nextPage,
        10
      );
      if (seq !== requestSeq.current) return;
      setPage(nextData.page);
      setTableData(nextData);
    } catch {
      if (seq === requestSeq.current) {
        setError("Gagal memuat halaman data tabel.");
      }
    }
  }

  const dataColumns: DataTableColumn<TableRow>[] = tableData.columns.map(
    (columnName) => ({
      key: columnName,
      header: columnName,
      render: (row) => {
        const val = row[columnName];
        return val === null || val === undefined ? "" : String(val);
      },
    })
  );

  return (
    <>
      <AlertMessage variant="error" message={error} />
      <div className="card-grid">
        <Card title="Schemas">
          <ul className="item-list">
            {initialData.schemas.length > 0 ? (
              initialData.schemas.map((schema) => (
                <li
                  key={schema}
                  className={schema === activeSchema ? "item-active" : undefined}
                >
                  <button
                    type="button"
                    className="selectable-link"
                    aria-current={schema === activeSchema ? "true" : undefined}
                    onClick={() => void handleSelectSchema(schema)}
                  >
                    {schema}
                  </button>
                </li>
              ))
            ) : (
              <li className="empty-state">
                Belum ada schema yang dapat ditampilkan.
              </li>
            )}
          </ul>
        </Card>

        <Card title={activeSchema ? `Tables · ${activeSchema}` : "Tables"}>
          <ul className="item-list">
            {tables.length > 0 ? (
              tables.map((table) => (
                <li
                  key={table}
                  className={table === activeTable ? "item-active" : undefined}
                >
                  <button
                    type="button"
                    className="selectable-link"
                    aria-current={table === activeTable ? "true" : undefined}
                    onClick={() => void handleSelectTable(table, "structure")}
                  >
                    {table}
                  </button>{" "}
                  (
                  <button
                    type="button"
                    className="selectable-link"
                    aria-label={`Data ${table}`}
                    onClick={() => void handleSelectTable(table, "data")}
                  >
                    Data
                  </button>
                  )
                </li>
              ))
            ) : (
              <li className="empty-state">Belum ada tabel dalam schema ini.</li>
            )}
          </ul>
        </Card>
      </div>

      {activeSchema && activeTable ? (
        <Card title={`${activeSchema}.${activeTable}`}>
          <Tabs
            tabs={EXPLORER_TABS}
            activeTab={activeTab}
            onChange={(tab) => void handleTabChange(tab)}
            ariaLabel="Tampilan tabel"
          />

          {activeTab === "structure" ? (
            <div>
              <h3>Columns</h3>
              <ul className="item-list">
                {metadata && metadata.columns.length > 0 ? (
                  metadata.columns.map((column) => (
                    <li key={column.name}>
                      {column.name} ({column.dataType}) Nullable:{" "}
                      {String(column.isNullable)}
                    </li>
                  ))
                ) : (
                  <li className="empty-state">
                    Tidak ada kolom yang dapat ditampilkan.
                  </li>
                )}
              </ul>

              <h3>Constraints</h3>
              <ul className="item-list">
                {metadata && metadata.constraints.length > 0 ? (
                  metadata.constraints.map((constraint) => (
                    <li key={constraint.name}>
                      {constraint.name} ({constraint.type})
                    </li>
                  ))
                ) : (
                  <li className="empty-state">Tidak ada constraint.</li>
                )}
              </ul>

              <h3>Indexes</h3>
              <ul className="item-list">
                {metadata && metadata.indexes.length > 0 ? (
                  metadata.indexes.map((index) => (
                    <li key={index.name}>
                      {index.name} Unique: {String(index.isUnique)}
                    </li>
                  ))
                ) : (
                  <li className="empty-state">Tidak ada index.</li>
                )}
              </ul>
            </div>
          ) : (
            <DataTable
              columns={dataColumns}
              rows={tableData.rows}
              getRowKey={(_, idx) =>
                `${activeSchema}.${activeTable}.${page}.${idx}`
              }
              emptyMessage="Belum ada data pada tabel ini."
              pagination={{
                page: tableData.page,
                totalPages: tableData.totalPages,
                totalRows: tableData.totalRows,
                onPageChange: (nextPage) => void handlePageChange(nextPage),
              }}
            />
          )}
        </Card>
      ) : null}
    </>
  );
}
