import { cleanup } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import { afterEach, beforeEach, vi } from "vitest";
import "@testing-library/jest-dom/vitest";

export const mockRouter = {
  push: vi.fn(),
  replace: vi.fn(),
  refresh: vi.fn(),
  back: vi.fn(),
  forward: vi.fn(),
  prefetch: vi.fn(),
};

vi.mock("next/navigation", () => ({
  useRouter: () => mockRouter,
  usePathname: () => "/",
  useSearchParams: () => new URLSearchParams(),
}));

vi.mock("next/link", () => ({
  default: function MockLink(props: Record<string, unknown>) {
    const { href, children, className, id, "aria-label": ariaLabel } = props;
    const resolvedHref =
      typeof href === "string"
        ? href
        : typeof href === "object" && href !== null && "pathname" in href
          ? String((href as { pathname?: string }).pathname ?? "#")
          : "#";

    return createElement(
      "a",
      {
        href: resolvedHref,
        className: className as string | undefined,
        id: id as string | undefined,
        "aria-label": ariaLabel as string | undefined,
      },
      children as ReactNode
    );
  },
}));

beforeEach(() => {
  mockRouter.push.mockReset();
  mockRouter.replace.mockReset();
  mockRouter.refresh.mockReset();
  mockRouter.back.mockReset();
  mockRouter.forward.mockReset();
  mockRouter.prefetch.mockReset();
});

afterEach(() => {
  cleanup();
});
