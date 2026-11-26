import Link from "next/link";
import { Card } from "@/components/ui/Card";
import type { TargetDBInfo } from "@/lib/types";

export function DatabaseList({
  databases,
}: {
  databases: ReadonlyArray<TargetDBInfo>;
}) {
  return (
    <Card title="Databases">
      <ul className="item-list">
        {databases.length > 0 ? (
          databases.map((db) => {
            const endpoint = db.host
              ? `${db.host}:${db.port}/${db.dbName}`
              : db.dbName;
            return (
              <li key={db.id}>
                <strong>
                  <Link href={`/databases/${db.id}`}>{db.name}</Link>
                </strong>
                <br />
                <span className="muted">
                  {db.dbType} · {endpoint}
                </span>
                <div className="link-row">
                  <Link href={`/databases/${db.id}`}>Browse data</Link>
                  <Link href={`/databases/${db.id}/migrations`}>
                    Migrations
                  </Link>
                </div>
              </li>
            );
          })
        ) : (
          <li className="empty-state">
            Belum ada database yang dapat diakses akun Anda.
          </li>
        )}
      </ul>
    </Card>
  );
}
