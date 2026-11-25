"use client";

import type { ReactNode } from "react";
import { adminService } from "@/lib/api/client/admin";
import { authService } from "@/lib/api/client/auth";
import { ServicesContext } from "@/lib/api/context";
import type { AppServices } from "@/lib/api/contract";
import { databaseService } from "@/lib/api/client/databases";
import { migrationService } from "@/lib/api/client/migrations";

const defaultServices: AppServices = {
  auth: authService,
  databases: databaseService,
  migrations: migrationService,
  admin: adminService,
};

export function Providers({
  services = defaultServices,
  children,
}: {
  services?: Partial<AppServices>;
  children: ReactNode;
}) {
  return (
    <ServicesContext.Provider value={services}>
      {children}
    </ServicesContext.Provider>
  );
}
