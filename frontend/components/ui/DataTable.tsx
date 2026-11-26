import type { ReactNode } from "react";

export interface DataTableColumn<T> {
  key: string;
  header: string;
  render: (row: T) => ReactNode;
}

export interface DataTablePagination {
  page: number;
  totalPages: number;
  totalRows: number;
  onPageChange: (nextPage: number) => void;
}

export function DataTable<T>({
  columns,
  rows,
  getRowKey,
  emptyMessage,
  pagination,
}: {
  columns: ReadonlyArray<DataTableColumn<T>>;
  rows: ReadonlyArray<T>;
  getRowKey: (row: T, index: number) => string | number;
  emptyMessage: string;
  pagination?: DataTablePagination;
}) {
  if (rows.length === 0) {
    return <p className="empty-state">{emptyMessage}</p>;
  }

  return (
    <div>
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              {columns.map((column) => (
                <th key={column.key} scope="col">
                  {column.header}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((row, index) => (
              <tr key={getRowKey(row, index)}>
                {columns.map((column) => (
                  <td key={column.key}>{column.render(row)}</td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {pagination && pagination.totalPages > 1 ? (
        <div className="pagination">
          <span className="muted">
            Halaman {pagination.page} dari {pagination.totalPages} ({pagination.totalRows} baris)
          </span>
          <div className="link-row">
            <button
              type="button"
              className="button-subtle"
              disabled={pagination.page <= 1}
              onClick={() => pagination.onPageChange(pagination.page - 1)}
            >
              Sebelumnya
            </button>
            <button
              type="button"
              className="button-subtle"
              disabled={pagination.page >= pagination.totalPages}
              onClick={() => pagination.onPageChange(pagination.page + 1)}
            >
              Berikutnya
            </button>
          </div>
        </div>
      ) : null}
    </div>
  );
}
