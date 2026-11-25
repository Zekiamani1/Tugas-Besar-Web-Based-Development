import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { DatabaseList } from "@/features/dashboard/DatabaseList";
import type { TargetDBInfo } from "@/lib/types";

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
  {
    id: 3,
    name: "SQLite Local Archive",
    dbType: "sqlite",
    host: "",
    port: 0,
    user: "",
    dbName: "/data/archive.db",
  },
];

describe("DatabaseList", () => {
  it("renders injected databases with endpoints and action links", () => {
    render(<DatabaseList databases={SAMPLE_DATABASES} />);

    expect(
      screen.getByRole("heading", { level: 2, name: "Databases" })
    ).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "Toko Online Production" })
    ).toHaveAttribute("href", "/databases/1");
    expect(
      screen.getByText("postgres · db-prod.internal:5432/db_toko_online")
    ).toBeInTheDocument();
    expect(screen.getByText("sqlite · /data/archive.db")).toBeInTheDocument();
  });

  it("renders empty state when databases prop is empty", () => {
    render(<DatabaseList databases={[]} />);

    expect(
      screen.getByText("Belum ada database yang dapat diakses akun Anda.")
    ).toBeInTheDocument();
  });
});
