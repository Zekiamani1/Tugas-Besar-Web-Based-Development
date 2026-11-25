"use client";

import { useContext, useState } from "react";
import { AlertMessage } from "@/components/ui/AlertMessage";
import { Card } from "@/components/ui/Card";
import { DataTable, type DataTableColumn } from "@/components/ui/DataTable";
import { formatAccessLevel } from "@/components/ui/StatusBadge";
import { Tabs, type TabItem } from "@/components/ui/Tabs";
import { ServicesContext } from "@/lib/api/context";
import type {
  AccessLevel,
  AdminTab,
  GroupDetail,
  TargetDBInfo,
  UserSummary,
} from "@/lib/types";
import {
  submitAssignMember,
  submitCreateGroup,
  submitRegisterDatabase,
  submitRegisterUser,
  submitSetGroupAccess,
} from "./logic";

export type { AdminTab };

type AdminFormKey =
  | "users"
  | "groups"
  | "members"
  | "permissions"
  | "databases";

const USER_COLUMNS: ReadonlyArray<DataTableColumn<UserSummary>> = [
  { key: "id", header: "ID", render: (user) => user.id },
  { key: "username", header: "Username", render: (user) => user.username },
  {
    key: "role",
    header: "Platform role",
    render: (user) => (user.isAdmin ? "Admin" : "User"),
  },
];

const GROUP_COLUMNS: ReadonlyArray<DataTableColumn<GroupDetail>> = [
  { key: "id", header: "ID", render: (group) => group.id },
  { key: "name", header: "Name", render: (group) => group.name },
  {
    key: "members",
    header: "Anggota",
    render: (group) => (
      <ul className="item-list">
        {group.members.length > 0 ? (
          group.members.map((member) => (
            <li key={member.id}>
              {member.username}{" "}
              <span className="muted">(User #{member.id})</span>
            </li>
          ))
        ) : (
          <li className="empty-state">Belum ada anggota.</li>
        )}
      </ul>
    ),
  },
  {
    key: "access",
    header: "Akses database",
    render: (group) => (
      <ul className="item-list">
        {group.databaseAccess.length > 0 ? (
          group.databaseAccess.map((access) => (
            <li key={access.targetDbId}>
              {access.databaseName}{" "}
              <span className="muted">(Database #{access.targetDbId})</span> —{" "}
              {formatAccessLevel(access.accessLevel)}
            </li>
          ))
        ) : (
          <li className="empty-state">Belum ada akses database.</li>
        )}
      </ul>
    ),
  },
];

const DATABASE_COLUMNS: ReadonlyArray<DataTableColumn<TargetDBInfo>> = [
  { key: "id", header: "ID", render: (db) => db.id },
  { key: "name", header: "Name", render: (db) => db.name },
  { key: "type", header: "Type", render: (db) => db.dbType },
  { key: "host", header: "Host", render: (db) => db.host || "-" },
  {
    key: "port",
    header: "Port",
    render: (db) => (db.port > 0 ? db.port : "-"),
  },
  { key: "dbName", header: "Database name", render: (db) => db.dbName },
];

export function AdminPanel({
  initialTab = "manage",
  initialUsers,
  initialGroups,
  initialDatabases,
}: {
  initialTab?: AdminTab;
  initialUsers: UserSummary[];
  initialGroups: GroupDetail[];
  initialDatabases: TargetDBInfo[];
}) {
  const { admin: adminService } = useContext(ServicesContext);
  const [users, setUsers] = useState<UserSummary[]>(initialUsers);
  const [groups, setGroups] = useState<GroupDetail[]>(initialGroups);
  const [databases, setDatabases] = useState<TargetDBInfo[]>(initialDatabases);

  const [activeTab, setActiveTab] = useState<AdminTab>(initialTab);
  const [successMessage, setSuccessMessage] = useState<string | null>(null);
  const [errorState, setErrorState] = useState<{
    form: AdminFormKey;
    message: string;
  } | null>(null);

  const [newUsername, setNewUsername] = useState("");
  const [newUserPassword, setNewUserPassword] = useState("");
  const [newGroupName, setNewGroupName] = useState("");
  const [memberUserId, setMemberUserId] = useState("");
  const [memberGroupId, setMemberGroupId] = useState("");
  const [accessGroupId, setAccessGroupId] = useState("");
  const [accessDbId, setAccessDbId] = useState("");
  const [accessLevel, setAccessLevel] = useState<AccessLevel>("VIEW_ONLY");

  const [dbNameLabel, setDbNameLabel] = useState("");
  const [dbType, setDbType] = useState("postgres");
  const [dbHost, setDbHost] = useState("");
  const [dbPort, setDbPort] = useState("");
  const [dbUser, setDbUser] = useState("");
  const [dbPassword, setDbPassword] = useState("");
  const [dbDatabaseName, setDbDatabaseName] = useState("");

  function handleTabChange(nextTab: AdminTab) {
    setActiveTab(nextTab);
    if (typeof window !== "undefined") {
      window.history.replaceState(null, "", `/admin?tab=${nextTab}`);
    }
  }

  function getFormError(form: AdminFormKey): string | null {
    return errorState?.form === form ? errorState.message : null;
  }

  async function handleRegisterUser() {
    if (!adminService) return;
    const res = await submitRegisterUser(
      adminService,
      newUsername,
      newUserPassword
    );
    if (!res.ok) {
      setSuccessMessage(null);
      setErrorState({ form: "users", message: res.error });
      return;
    }
    setErrorState(null);
    setSuccessMessage(res.message);
    setNewUsername("");
    setNewUserPassword("");
    setUsers(res.users);
  }

  async function handleCreateGroup() {
    if (!adminService) return;
    const res = await submitCreateGroup(adminService, newGroupName);
    if (!res.ok) {
      setSuccessMessage(null);
      setErrorState({ form: "groups", message: res.error });
      return;
    }
    setErrorState(null);
    setSuccessMessage(res.message);
    setNewGroupName("");
    setGroups(res.groups);
  }

  async function handleAssignMember() {
    if (!adminService) return;
    const res = await submitAssignMember(
      adminService,
      memberUserId,
      memberGroupId
    );
    if (!res.ok) {
      setSuccessMessage(null);
      setErrorState({ form: "members", message: res.error });
      return;
    }
    setErrorState(null);
    setSuccessMessage(res.message);
    setMemberUserId("");
    setMemberGroupId("");
    setGroups(res.groups);
  }

  async function handleSetGroupAccess() {
    if (!adminService) return;
    const res = await submitSetGroupAccess(
      adminService,
      accessGroupId,
      accessDbId,
      accessLevel
    );
    if (!res.ok) {
      setSuccessMessage(null);
      setErrorState({ form: "permissions", message: res.error });
      return;
    }
    setErrorState(null);
    setSuccessMessage(res.message);
    setGroups(res.groups);
  }

  async function handleRegisterDatabase() {
    if (!adminService) return;
    const res = await submitRegisterDatabase(adminService, {
      name: dbNameLabel,
      dbType,
      host: dbHost,
      port: dbPort,
      user: dbUser,
      password: dbPassword,
      dbName: dbDatabaseName,
    });
    if (!res.ok) {
      setSuccessMessage(null);
      setErrorState({ form: "databases", message: res.error });
      return;
    }
    setErrorState(null);
    setSuccessMessage(res.message);
    setDbNameLabel("");
    setDbHost("");
    setDbPort("");
    setDbUser("");
    setDbPassword("");
    setDbDatabaseName("");
    setDatabases(res.databases);
  }

  const tabs: ReadonlyArray<TabItem<AdminTab>> = [
    { id: "manage", label: "Kelola" },
    { id: "users", label: "Users", count: users.length },
    { id: "groups", label: "Groups", count: groups.length },
    { id: "databases", label: "Databases", count: databases.length },
  ];

  const isSqlite = dbType === "sqlite";

  return (
    <>
      <Tabs
        tabs={tabs}
        activeTab={activeTab}
        onChange={handleTabChange}
        ariaLabel="Bagian Admin Panel"
      />

      {activeTab === "manage" ? (
        <>
          <AlertMessage variant="success" message={successMessage} />
          <div className="card-grid">
            <Card title="Register User">
              <form
                onSubmit={(event) => {
                  event.preventDefault();
                  void handleRegisterUser();
                }}
                noValidate
              >
                <label htmlFor="user-name">Username</label>
                <input
                  id="user-name"
                  name="username"
                  value={newUsername}
                  onChange={(event) => setNewUsername(event.target.value)}
                  autoComplete="off"
                  required
                />
                <label htmlFor="user-password">Password</label>
                <input
                  id="user-password"
                  type="password"
                  name="password"
                  value={newUserPassword}
                  onChange={(event) => setNewUserPassword(event.target.value)}
                  autoComplete="new-password"
                  required
                />
                <AlertMessage variant="error" message={getFormError("users")} />
                <button type="submit">Register User</button>
              </form>
            </Card>

            <Card title="Create Group">
              <form
                onSubmit={(event) => {
                  event.preventDefault();
                  void handleCreateGroup();
                }}
                noValidate
              >
                <label htmlFor="group-name">Group Name</label>
                <input
                  id="group-name"
                  name="group_name"
                  value={newGroupName}
                  onChange={(event) => setNewGroupName(event.target.value)}
                  required
                />
                <AlertMessage
                  variant="error"
                  message={getFormError("groups")}
                />
                <button type="submit">Create Group</button>
              </form>
            </Card>

            <Card title="Assign User to Group">
              <form
                onSubmit={(event) => {
                  event.preventDefault();
                  void handleAssignMember();
                }}
                noValidate
              >
                <label htmlFor="member-user">User</label>
                <select
                  id="member-user"
                  name="user_id"
                  value={memberUserId}
                  onChange={(event) => setMemberUserId(event.target.value)}
                  required
                >
                  <option value="" disabled>
                    Pilih user
                  </option>
                  {users.length > 0 ? (
                    users.map((user) => (
                      <option key={user.id} value={String(user.id)}>
                        {user.username} (User #{user.id})
                      </option>
                    ))
                  ) : (
                    <option value="" disabled>
                      Tidak ada user
                    </option>
                  )}
                </select>
                <label htmlFor="member-group">Group</label>
                <select
                  id="member-group"
                  name="group_id"
                  value={memberGroupId}
                  onChange={(event) => setMemberGroupId(event.target.value)}
                  required
                >
                  <option value="" disabled>
                    Pilih group
                  </option>
                  {groups.length > 0 ? (
                    groups.map((group) => (
                      <option key={group.id} value={String(group.id)}>
                        {group.name} (Group #{group.id})
                      </option>
                    ))
                  ) : (
                    <option value="" disabled>
                      Tidak ada group
                    </option>
                  )}
                </select>
                <AlertMessage
                  variant="error"
                  message={getFormError("members")}
                />
                <button type="submit">Assign</button>
              </form>
            </Card>

            <Card title="Set Group DB Access">
              <form
                onSubmit={(event) => {
                  event.preventDefault();
                  void handleSetGroupAccess();
                }}
                noValidate
              >
                <label htmlFor="access-group">Group</label>
                <select
                  id="access-group"
                  name="group_id"
                  value={accessGroupId}
                  onChange={(event) => setAccessGroupId(event.target.value)}
                  required
                >
                  <option value="" disabled>
                    Pilih group
                  </option>
                  {groups.length > 0 ? (
                    groups.map((group) => (
                      <option key={group.id} value={String(group.id)}>
                        {group.name} (Group #{group.id})
                      </option>
                    ))
                  ) : (
                    <option value="" disabled>
                      Tidak ada group
                    </option>
                  )}
                </select>
                <label htmlFor="access-db">Database</label>
                <select
                  id="access-db"
                  name="target_db_id"
                  value={accessDbId}
                  onChange={(event) => setAccessDbId(event.target.value)}
                  required
                >
                  <option value="" disabled>
                    Pilih database
                  </option>
                  {databases.length > 0 ? (
                    databases.map((db) => (
                      <option key={db.id} value={String(db.id)}>
                        {db.name} (Database #{db.id})
                      </option>
                    ))
                  ) : (
                    <option value="" disabled>
                      Tidak ada database
                    </option>
                  )}
                </select>
                <label htmlFor="access-level">Access Level</label>
                <select
                  id="access-level"
                  name="access_level"
                  value={accessLevel}
                  onChange={(event) =>
                    setAccessLevel(event.target.value as AccessLevel)
                  }
                  required
                >
                  <option value="VIEW_ONLY">View Only</option>
                  <option value="VIEW_EDIT">View + Edit</option>
                </select>
                <AlertMessage
                  variant="error"
                  message={getFormError("permissions")}
                />
                <button type="submit">Set Access</button>
              </form>
            </Card>

            <Card title="Register Target Database" wide>
              <form
                onSubmit={(event) => {
                  event.preventDefault();
                  void handleRegisterDatabase();
                }}
                noValidate
              >
                {isSqlite ? (
                  <p className="muted">
                    Untuk SQLite, cukup isi Name, DB Type, dan DB Name (path
                    file).
                  </p>
                ) : null}
                <div className="form-grid">
                  <div>
                    <label htmlFor="db-name">Name</label>
                    <input
                      id="db-name"
                      name="name"
                      value={dbNameLabel}
                      onChange={(event) => setDbNameLabel(event.target.value)}
                      required
                    />
                  </div>
                  <div>
                    <label htmlFor="db-type">DB Type</label>
                    <select
                      id="db-type"
                      name="db_type"
                      value={dbType}
                      onChange={(event) => setDbType(event.target.value)}
                      required
                    >
                      <option value="postgres">postgres</option>
                      <option value="mysql">mysql</option>
                      <option value="sqlite">sqlite</option>
                    </select>
                  </div>
                  <div>
                    <label htmlFor="db-host">Host</label>
                    <input
                      id="db-host"
                      name="host"
                      value={dbHost}
                      onChange={(event) => setDbHost(event.target.value)}
                      required={!isSqlite}
                    />
                  </div>
                  <div>
                    <label htmlFor="db-port">Port</label>
                    <input
                      id="db-port"
                      type="number"
                      name="port"
                      value={dbPort}
                      onChange={(event) => setDbPort(event.target.value)}
                      min={1}
                      max={65535}
                      required={!isSqlite}
                    />
                  </div>
                  <div>
                    <label htmlFor="db-user">User</label>
                    <input
                      id="db-user"
                      name="user"
                      value={dbUser}
                      onChange={(event) => setDbUser(event.target.value)}
                      required={!isSqlite}
                    />
                  </div>
                  <div>
                    <label htmlFor="db-password">Password</label>
                    <input
                      id="db-password"
                      type="password"
                      name="password"
                      value={dbPassword}
                      onChange={(event) => setDbPassword(event.target.value)}
                      autoComplete="new-password"
                      required={!isSqlite}
                    />
                  </div>
                  <div>
                    <label htmlFor="db-database">DB Name</label>
                    <input
                      id="db-database"
                      name="db_name"
                      value={dbDatabaseName}
                      onChange={(event) =>
                        setDbDatabaseName(event.target.value)
                      }
                      required
                    />
                  </div>
                </div>
                <AlertMessage
                  variant="error"
                  message={getFormError("databases")}
                />
                <button type="submit">Register Database</button>
              </form>
            </Card>
          </div>
        </>
      ) : null}

      {activeTab === "users" ? (
        <Card title="User list">
          <DataTable
            columns={USER_COLUMNS}
            rows={users}
            getRowKey={(user) => user.id}
            emptyMessage="Belum ada user."
          />
        </Card>
      ) : null}

      {activeTab === "groups" ? (
        <Card title="Group list">
          <DataTable
            columns={GROUP_COLUMNS}
            rows={groups}
            getRowKey={(group) => group.id}
            emptyMessage="Belum ada group."
          />
        </Card>
      ) : null}

      {activeTab === "databases" ? (
        <Card title="Database list">
          <DataTable
            columns={DATABASE_COLUMNS}
            rows={databases}
            getRowKey={(db) => db.id}
            emptyMessage="Belum ada database terdaftar."
          />
        </Card>
      ) : null}
    </>
  );
}
