"use client";

import type { ReactNode } from "react";
import { ServicesContext } from "@/lib/api/context";
import type { AppServices } from "@/lib/api/contract";

export function Providers({
  services = {},
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
