import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import { Providers } from "@/app/providers";
import { AdminPanel } from "@/features/admin/AdminPanel";
import { AccountNav } from "@/features/auth/AccountNav";
import { LoginForm } from "@/features/auth/LoginForm";
import { DatabaseExplorer } from "@/features/databases/DatabaseExplorer";
import { EMPTY_TABLE_DATA } from "@/features/databases/logic";
import { MigrationManager } from "@/features/migrations/MigrationManager";
import { authService } from "@/lib/api/client/auth";
import { loadAdminData } from "@/lib/api/server/admin";
import { loadMigrationsData } from "@/lib/api/server/migrations";
import { SESSION_COOKIE } from "@/lib/session";
import type { DatabaseExplorerInitialData } from "@/lib/types";
import { mockRouter } from "../../vitest.setup";
import { createMswBackend, TEST_BACKEND_URL } from "./msw-backend";

const backend = createMswBackend();
let originalPublicApiUrl: string | undefined;
let originalInternalApiUrl: string | undefined;

beforeAll(() => {
  originalPublicApiUrl = process.env.NEXT_PUBLIC_API_URL;
  originalInternalApiUrl = process.env.INTERNAL_API_URL;
  process.env.NEXT_PUBLIC_API_URL = TEST_BACKEND_URL;
  process.env.INTERNAL_API_URL = TEST_BACKEND_URL;
  backend.server.listen({ onUnhandledRequest: "error" });
});

afterEach(() => {
  backend.server.resetHandlers();
  backend.reset();
});

afterAll(() => {
  backend.server.close();
  if (originalPublicApiUrl === undefined) {
    delete process.env.NEXT_PUBLIC_API_URL;
  } else {
    process.env.NEXT_PUBLIC_API_URL = originalPublicApiUrl;
  }
  if (originalInternalApiUrl === undefined) {
    delete process.env.INTERNAL_API_URL;
  } else {
    process.env.INTERNAL_API_URL = originalInternalApiUrl;
  }
});

describe("React UI components + real HTTP services (MSW integration)", () => {
  it("handles LoginForm authentication errors, successful login, and AccountNav logout", async () => {
    const user = userEvent.setup();

    const { rerender } = render(
      <Providers>
        <LoginForm />
      </Providers>
    );

    await user.type(screen.getByLabelText("Username"), "admin");
    await user.type(screen.getByLabelText("Password"), "wrong");
    await user.click(screen.getByRole("button", { name: "Masuk" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Username atau password salah."
    );
    expect(mockRouter.push).not.toHaveBeenCalled();

    const passwordInput = screen.getByLabelText("Password");
    await user.clear(passwordInput);
    await user.type(passwordInput, "admin123");
    await user.click(screen.getByRole("button", { name: "Masuk" }));

    await waitFor(() => {
      expect(mockRouter.push).toHaveBeenCalledWith("/dashboard");
    });
    expect(document.cookie).toContain(`${SESSION_COOKIE}=`);

    rerender(
      <Providers>
        <AccountNav
          user={{ id: 1, username: "admin", isAdmin: true }}
          showAdminLink
        />
      </Providers>
    );

    await user.click(screen.getByRole("button", { name: "Logout" }));
    await waitFor(() => {
      expect(mockRouter.push).toHaveBeenCalledWith("/login");
    });
    expect(document.cookie).not.toContain(`${SESSION_COOKIE}=`);
    expect(backend.getActiveSessionCookie()).toBe("");
  });

  it("creates users, groups, and SQLite databases in AdminPanel and refreshes lists from backend", async () => {
    const user = userEvent.setup();
    await authService.login("admin", "admin123");
    const initial = await loadAdminData(
      backend.getActiveSessionCookie(),
      TEST_BACKEND_URL
    );

    render(
      <Providers>
        <AdminPanel
          initialUsers={initial.users}
          initialGroups={initial.groups}
          initialDatabases={initial.databases}
        />
      </Providers>
    );

    await user.type(
      screen.getByLabelText("Username", { selector: "#user-name" }),
      "dewi_devops"
    );
    await user.type(
      screen.getByLabelText("Password", { selector: "#user-password" }),
      "dewi123"
    );
    await user.click(screen.getByRole("button", { name: "Register User" }));
    expect(await screen.findByRole("status")).toHaveTextContent(
      "berhasil menambahkan dewi_devops"
    );

    await user.type(
      screen.getByLabelText("Name", { selector: "#db-name" }),
      "Staging SQLite"
    );
    await user.selectOptions(
      screen.getByLabelText("DB Type", { selector: "#db-type" }),
      "sqlite"
    );
    await user.type(
      screen.getByLabelText("DB Name", { selector: "#db-database" }),
      "/data/staging.db"
    );
    await user.click(
      screen.getByRole("button", { name: "Register Database" })
    );
    expect(await screen.findByRole("status")).toHaveTextContent(
      "DB ditambahkan dengan id 3"
    );

    await user.click(screen.getByRole("tab", { name: /Users/ }));
    expect(screen.getByText("dewi_devops")).toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: /Databases/ }));
    expect(screen.getByText("Staging SQLite")).toBeInTheDocument();
    expect(screen.getByText("/data/staging.db")).toBeInTheDocument();
  });

  it("navigates DatabaseExplorer schemas, metadata, and Table Data pagination against backend", async () => {
    const user = userEvent.setup();
    await authService.login("siti_analyst", "siti123");

    const initialData: DatabaseExplorerInitialData = {
      schemas: ["public", "analytics"],
      activeSchema: "public",
      tables: ["produk", "pesanan"],
      activeTable: "produk",
      activeTab: "structure",
      metadata: {
        schema: "public",
        tableName: "produk",
        columns: [{ name: "id", dataType: "integer", isNullable: false }],
        constraints: [],
        indexes: [],
      },
      tableData: EMPTY_TABLE_DATA,
    };

    render(
      <Providers>
        <DatabaseExplorer dbId={1} initialData={initialData} />
      </Providers>
    );

    await user.click(screen.getByRole("tab", { name: "Table Data" }));
    expect(await screen.findByText("Produk #1")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Berikutnya" }));
    expect(await screen.findByText("Standing Desk Mat")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "analytics" }));
    expect(
      await screen.findByRole("heading", {
        level: 2,
        name: "Tables · analytics",
      })
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "empty_audit" })
    ).toBeInTheDocument();
  });

  it("drives full MigrationManager create, PUT edit, explain, submit for approval, inline review decision, and rollback filter via HTTP services", async () => {
    const user = userEvent.setup();
    await authService.login("budi_developer", "budi123");
    const loaded = await loadMigrationsData(1, {
      cookieHeader: backend.getActiveSessionCookie(),
      baseUrl: TEST_BACKEND_URL,
    });

    render(
      <Providers>
        <MigrationManager
          dbId={1}
          canEdit={loaded.canEdit}
          initialDrafts={loaded.drafts}
          initialHistory={loaded.history}
          initialPosition={loaded.position}
          initialPendingReviews={loaded.pendingReviews}
        />
      </Providers>
    );

    await user.type(
      screen.getByLabelText("SQL Up", { selector: "#create-draft-sql-up" }),
      "CREATE TABLE orders (id INT);"
    );
    await user.type(
      screen.getByLabelText("SQL Down", {
        selector: "#create-draft-sql-down",
      }),
      "DROP TABLE orders;"
    );
    await user.click(screen.getByRole("button", { name: "Create Draft" }));
    expect(
      await screen.findByRole("heading", { level: 2, name: "Migration #2" })
    ).toBeInTheDocument();

    const editUp = screen.getByLabelText("SQL Up", {
      selector: "#edit-draft-sql-up",
    });
    await user.clear(editUp);
    await user.type(editUp, "CREATE TABLE orders (id SERIAL PRIMARY KEY);");
    await user.click(screen.getByRole("button", { name: "Save Draft" }));
    expect(await screen.findByRole("status")).toHaveTextContent(
      "draft updated"
    );

    await user.click(screen.getByRole("button", { name: "Lihat Query Plan" }));
    expect(
      await screen.findByText(/Seq Scan on public\.produk/)
    ).toBeInTheDocument();

    await user.click(
      screen.getByRole("button", { name: "Submit untuk Approval" })
    );
    expect(
      await screen.findByRole("link", { name: "Buka Review #1" })
    ).toHaveAttribute("href", "/databases/1/migrations/2/reviews/1");

    await user.click(screen.getByRole("tab", { name: /Pending Reviews/ }));
    expect(
      await screen.findByRole("link", { name: "Review #1" })
    ).toBeInTheDocument();
    await user.click(screen.getAllByRole("button", { name: "Approve" })[0]);
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Persetujuan group berhasil disimpan."
    );
    expect(
      await screen.findByText(
        "Tidak ada review yang menunggu keputusan Anda."
      )
    ).toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: "Migrations" }));
    await user.click(
      screen.getByLabelText("Sembunyikan pasangan rollback", {
        selector: "#hide-rollback-toggle",
      })
    );
    expect(screen.getByText(/Total eksekusi tampil:/)).toBeInTheDocument();
  });
});
