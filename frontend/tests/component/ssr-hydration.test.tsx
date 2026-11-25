import { act, fireEvent, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { hydrateRoot, type Root } from "react-dom/client";
import { renderToString } from "react-dom/server";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PageLayout } from "@/components/ui/PageLayout";
import { AdminPanel } from "@/features/admin/AdminPanel";
import { AccountNav } from "@/features/auth/AccountNav";
import { LoginForm } from "@/features/auth/LoginForm";
import { DatabaseList } from "@/features/dashboard/DatabaseList";
import { DatabaseExplorer } from "@/features/databases/DatabaseExplorer";
import { MigrationManager } from "@/features/migrations/MigrationManager";
import { Providers } from "@/app/providers";
import type { AppServices, DatabaseService } from "@/lib/api/contract";
import type {
  DatabaseExplorerInitialData,
  GroupDetail,
  MigrationRecord,
  TargetDBInfo,
  UserSummary,
} from "@/lib/types";

const ADMIN_USER: UserSummary = {
  id: 1,
  username: "admin",
  isAdmin: true,
};

const SAMPLE_USERS: UserSummary[] = [
  ADMIN_USER,
  { id: 2, username: "budi_developer", isAdmin: false },
];

const SAMPLE_DATABASES: TargetDBInfo[] = [
  {
    id: 1,
    name: "Toko Online Production",
    dbType: "postgres",
    host: "db-prod.internal",
    port: 5432,
    user: "postgres",
    dbName: "db_toko_online",
  },
];

const SAMPLE_GROUPS: GroupDetail[] = [
  {
    id: 1,
    name: "tim_pengembang",
    members: [
      { id: 1, username: "admin" },
      { id: 2, username: "budi_developer" },
    ],
    databaseAccess: [
      {
        targetDbId: 1,
        databaseName: "Toko Online Production",
        accessLevel: "VIEW_EDIT",
      },
    ],
  },
];

const SAMPLE_EXPLORER_DATA: DatabaseExplorerInitialData = {
  schemas: ["public", "analytics"],
  activeSchema: "public",
  tables: ["produk", "pesanan"],
  activeTable: "produk",
  activeTab: "structure",
  metadata: {
    schema: "public",
    tableName: "produk",
    columns: [
      { name: "id", dataType: "integer", isNullable: false },
      { name: "nama", dataType: "text", isNullable: false },
    ],
    constraints: [{ name: "produk_pkey", type: "PRIMARY KEY" }],
    indexes: [{ name: "produk_pkey", isUnique: true }],
  },
  tableData: {
    columns: ["id", "nama"],
    rows: [{ id: 1, nama: "Laptop ThinkPad X1" }],
    totalRows: 1,
    page: 1,
    pageSize: 10,
    totalPages: 1,
  },
};

const SAMPLE_HISTORY: MigrationRecord[] = [
  {
    id: 1,
    targetDbId: 1,
    version: 1,
    sqlQuery: "CREATE TABLE public.produk (id INT PRIMARY KEY);",
    executedBy: "budi_developer",
    status: "APPLIED",
    executedAt: "2026-11-24 10:00:00 UTC",
  },
];

let activeRoot: Root | null = null;
let container: HTMLDivElement | null = null;

function mountSsrAndHydrate(node: ReactNode, services?: Partial<AppServices>) {
  const tree = <Providers services={services}>{node}</Providers>;
  const ssrHtml = renderToString(tree);
  container = document.createElement("div");
  container.innerHTML = ssrHtml;
  document.body.appendChild(container);

  const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
  act(() => {
    activeRoot = hydrateRoot(container!, tree);
  });

  const hydrationErrors = errorSpy.mock.calls.map((call) => call.join(" "));
  errorSpy.mockRestore();

  return { ssrHtml, hydrationErrors, container };
}

afterEach(() => {
  if (activeRoot) {
    act(() => {
      activeRoot?.unmount();
    });
    activeRoot = null;
  }
  if (container && container.parentNode) {
    container.parentNode.removeChild(container);
  }
  container = null;
});

describe("SSR + React hydration fidelity", () => {
  it("hydrates LoginForm without mismatch warnings", () => {
    const { ssrHtml, hydrationErrors } = mountSsrAndHydrate(
      <LoginForm reason="session" />
    );

    expect(ssrHtml).toContain("Masuk");
    expect(ssrHtml).toContain("Sesi berakhir atau Anda belum login");
    expect(hydrationErrors).toEqual([]);
    expect(screen.getByRole("button", { name: "Masuk" })).toBeInTheDocument();
  });

  it("hydrates PageLayout + AccountNav + DatabaseList without mismatch warnings", () => {
    const { ssrHtml, hydrationErrors } = mountSsrAndHydrate(
      <PageLayout
        subtitle="Database Manager"
        title="Dashboard"
        nav={<AccountNav user={ADMIN_USER} showAdminLink />}
      >
        <DatabaseList databases={SAMPLE_DATABASES} />
      </PageLayout>
    );

    expect(ssrHtml).toContain("Toko Online Production");
    expect(ssrHtml).toContain("Admin Panel");
    expect(hydrationErrors).toEqual([]);
    expect(screen.getByRole("link", { name: "Admin Panel" })).toHaveAttribute(
      "href",
      "/admin"
    );
  });

  it("hydrates DatabaseExplorer and supports async tab switching after hydration", async () => {
    const databaseService: DatabaseService = {
      getTables: vi.fn(async () => []),
      getTableMetadata: vi.fn(async () => null),
      getTableData: vi.fn(async () => SAMPLE_EXPLORER_DATA.tableData),
    };

    const { ssrHtml, hydrationErrors } = mountSsrAndHydrate(
      <DatabaseExplorer dbId={1} initialData={SAMPLE_EXPLORER_DATA} />,
      { databases: databaseService }
    );

    expect(ssrHtml).toContain("Schemas");
    expect(ssrHtml).toContain("public.produk");
    expect(hydrationErrors).toEqual([]);

    await act(async () => {
      fireEvent.click(screen.getByRole("tab", { name: "Table Data" }));
    });
    expect(await screen.findByText("Laptop ThinkPad X1")).toBeInTheDocument();
    expect(databaseService.getTableData).toHaveBeenCalledWith(
      1,
      "public",
      "produk",
      1,
      10
    );
  });

  it("hydrates MigrationManager without mismatch warnings", () => {
    const { ssrHtml, hydrationErrors } = mountSsrAndHydrate(
      <MigrationManager
        dbId={1}
        canEdit
        initialDrafts={[]}
        initialHistory={SAMPLE_HISTORY}
      />
    );

    expect(ssrHtml).toContain("Create Draft");
    expect(ssrHtml).toContain("Migration #1");
    expect(hydrationErrors).toEqual([]);
  });

  it("hydrates AdminPanel and switches tabs cleanly after hydration", () => {
    const { ssrHtml, hydrationErrors } = mountSsrAndHydrate(
      <AdminPanel
        initialUsers={SAMPLE_USERS}
        initialGroups={SAMPLE_GROUPS}
        initialDatabases={SAMPLE_DATABASES}
      />
    );

    expect(ssrHtml).toContain("Register User");
    expect(hydrationErrors).toEqual([]);

    act(() => {
      fireEvent.click(screen.getByRole("tab", { name: /Users/ }));
    });
    expect(screen.getByText("budi_developer")).toBeInTheDocument();
  });
});
