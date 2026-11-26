import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { AdminPanel } from "@/features/admin/AdminPanel";
import { Providers } from "@/app/providers";
import type { AdminService } from "@/lib/api/contract";
import type { GroupDetail, TargetDBInfo, UserSummary } from "@/lib/types";

const INITIAL_USERS: UserSummary[] = [
  { id: 1, username: "admin", isAdmin: true },
  { id: 2, username: "budi_developer", isAdmin: false },
];

const INITIAL_GROUPS: GroupDetail[] = [
  {
    id: 1,
    name: "tim_pengembang",
    members: [{ id: 1, username: "admin" }],
    databaseAccess: [
      {
        targetDbId: 1,
        databaseName: "Toko Online Production",
        accessLevel: "VIEW_EDIT",
      },
    ],
  },
];

const INITIAL_DATABASES: TargetDBInfo[] = [
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

function createFakeAdminService(
  overrides?: Partial<AdminService>
): AdminService {
  return {
    listUsers: vi.fn(async () => INITIAL_USERS),
    listGroups: vi.fn(async () => INITIAL_GROUPS),
    listDatabases: vi.fn(async () => INITIAL_DATABASES),
    registerUser: vi.fn(),
    createGroup: vi.fn(),
    assignUserToGroup: vi.fn(),
    setGroupDbAccess: vi.fn(),
    registerDatabase: vi.fn(),
    ...overrides,
  };
}

describe("AdminPanel", () => {
  it("submits admin forms via injected adminService, handles validation errors, and switches list tabs", async () => {
    const user = userEvent.setup();
    const newUser: UserSummary = { id: 3, username: "andi_qa", isAdmin: false };
    const newDb: TargetDBInfo = {
      id: 4,
      name: "QA SQLite",
      dbType: "sqlite",
      host: "",
      port: 0,
      user: "",
      dbName: "/data/qa.db",
    };

    const registerUser = vi
      .fn<AdminService["registerUser"]>()
      .mockResolvedValueOnce({
        ok: false,
        error: "Periksa kembali data pengguna.",
      })
      .mockResolvedValueOnce({
        ok: true,
        message: "berhasil menambahkan andi_qa",
        data: newUser,
      });

    const registerDatabase = vi
      .fn<AdminService["registerDatabase"]>()
      .mockResolvedValueOnce({
        ok: true,
        message: "DB ditambahkan dengan id 4",
        data: newDb,
      });

    const adminService = createFakeAdminService({
      registerUser,
      listUsers: vi.fn(async () => [...INITIAL_USERS, newUser]),
      registerDatabase,
      listDatabases: vi.fn(async () => [...INITIAL_DATABASES, newDb]),
    });

    render(
      <Providers services={{ admin: adminService }}>
        <AdminPanel
          initialUsers={INITIAL_USERS}
          initialGroups={INITIAL_GROUPS}
          initialDatabases={INITIAL_DATABASES}
        />
      </Providers>
    );

    await user.click(screen.getByRole("button", { name: "Register User" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Periksa kembali data pengguna."
    );

    await user.type(
      screen.getByLabelText("Username", { selector: "#user-name" }),
      "andi_qa"
    );
    await user.type(
      screen.getByLabelText("Password", { selector: "#user-password" }),
      "andi123"
    );
    await user.click(screen.getByRole("button", { name: "Register User" }));

    expect(registerUser).toHaveBeenLastCalledWith("andi_qa", "andi123");
    expect(await screen.findByRole("status")).toHaveTextContent(
      "berhasil menambahkan andi_qa"
    );

    await user.type(
      screen.getByLabelText("Name", { selector: "#db-name" }),
      "QA SQLite"
    );
    await user.selectOptions(
      screen.getByLabelText("DB Type", { selector: "#db-type" }),
      "sqlite"
    );
    await user.type(
      screen.getByLabelText("DB Name", { selector: "#db-database" }),
      "/data/qa.db"
    );
    await user.click(
      screen.getByRole("button", { name: "Register Database" })
    );

    expect(registerDatabase).toHaveBeenCalledWith({
      name: "QA SQLite",
      dbType: "sqlite",
      host: "",
      port: "",
      user: "",
      password: "",
      dbName: "/data/qa.db",
    });
    expect(await screen.findByRole("status")).toHaveTextContent(
      "DB ditambahkan dengan id 4"
    );

    await user.click(screen.getByRole("tab", { name: /Users/ }));
    expect(screen.getByText("andi_qa")).toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: /Groups/ }));
    expect(screen.getByText("tim_pengembang")).toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: /Databases/ }));
    expect(screen.getByText("QA SQLite")).toBeInTheDocument();
    expect(screen.getByText("/data/qa.db")).toBeInTheDocument();
  });

  it("renders empty states for users, groups, and databases tabs when dataset is empty", async () => {
    const user = userEvent.setup();
    render(
      <Providers>
        <AdminPanel
          initialTab="users"
          initialUsers={[]}
          initialGroups={[]}
          initialDatabases={[]}
        />
      </Providers>
    );
    expect(screen.getByText("Belum ada user.")).toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: /Groups/ }));
    expect(screen.getByText("Belum ada group.")).toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: /Databases/ }));
    expect(
      screen.getByText("Belum ada database terdaftar.")
    ).toBeInTheDocument();
  });
});
