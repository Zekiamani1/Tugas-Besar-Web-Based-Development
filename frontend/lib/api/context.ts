"use client";

import { createContext } from "react";
import type { AppServices } from "./contract";

export const ServicesContext = createContext<Partial<AppServices>>({});
