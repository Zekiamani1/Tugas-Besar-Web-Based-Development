import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { AccountNav } from "@/features/auth/AccountNav";
import { LoginForm } from "@/features/auth/LoginForm";
import { Providers } from "@/app/providers";
import type { AuthService } from "@/lib/api/contract";
import { mockRouter } from "../../vitest.setup";

function createFakeAuthService(overrides?: Partial<AuthService>): AuthService {
  return {
    login: vi.fn(),
    logout: vi.fn(async () => {}),
    ...overrides,
  };
}

describe("LoginForm", () => {
  it("shows session expired banner when reason=session", () => {
    render(
      <Providers>
        <LoginForm reason="session" />
      </Providers>
    );

    expect(screen.getByRole("alert")).toHaveTextContent(
      "Sesi berakhir atau Anda belum login. Silakan masuk kembali."
    );
  });

  it("shows error message on invalid credentials while preserving username", async () => {
    const user = userEvent.setup();
    const login = vi.fn<AuthService["login"]>().mockResolvedValueOnce({
      ok: false,
      error: "Username atau password salah.",
    });

    render(
      <Providers services={{ auth: createFakeAuthService({ login }) }}>
        <LoginForm />
      </Providers>
    );

    await user.type(screen.getByLabelText("Username"), "admin");
    await user.type(screen.getByLabelText("Password"), "salah");
    await user.click(screen.getByRole("button", { name: "Masuk" }));

    expect(login).toHaveBeenCalledWith("admin", "salah");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Username atau password salah."
    );
    expect(screen.getByLabelText("Username")).toHaveValue("admin");
    expect(mockRouter.push).not.toHaveBeenCalled();
  });

  it("calls context authService.login and navigates to /dashboard on valid credentials", async () => {
    const user = userEvent.setup();
    const login = vi.fn<AuthService["login"]>().mockResolvedValueOnce({
      ok: true,
      data: { id: 2, username: "budi_developer", isAdmin: false },
    });

    render(
      <Providers services={{ auth: createFakeAuthService({ login }) }}>
        <LoginForm />
      </Providers>
    );

    await user.type(screen.getByLabelText("Username"), "budi_developer");
    await user.type(screen.getByLabelText("Password"), "budi123");
    await user.click(screen.getByRole("button", { name: "Masuk" }));

    await waitFor(() => {
      expect(mockRouter.push).toHaveBeenCalledWith("/dashboard");
    });
    expect(login).toHaveBeenCalledWith("budi_developer", "budi123");
    expect(mockRouter.refresh).toHaveBeenCalledTimes(1);
  });
});

describe("AccountNav", () => {
  it("renders nothing when unauthenticated", () => {
    const { container } = render(
      <Providers>
        <AccountNav user={null} showAdminLink />
      </Providers>
    );

    expect(container).toBeEmptyDOMElement();
  });

  it("renders conditional Admin Panel link for admins and triggers context logout", async () => {
    const user = userEvent.setup();
    const logout = vi.fn(async () => {});

    render(
      <Providers services={{ auth: createFakeAuthService({ logout }) }}>
        <AccountNav
          user={{ id: 1, username: "admin", isAdmin: true }}
          showAdminLink
        />
      </Providers>
    );

    expect(screen.getByText("Masuk sebagai admin")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Admin Panel" })).toHaveAttribute(
      "href",
      "/admin"
    );

    await user.click(screen.getByRole("button", { name: "Logout" }));
    await waitFor(() => {
      expect(mockRouter.push).toHaveBeenCalledWith("/login");
    });
    expect(logout).toHaveBeenCalledTimes(1);
  });

  it("hides Admin Panel link for non-admin users", () => {
    render(
      <Providers>
        <AccountNav
          user={{ id: 2, username: "budi_developer", isAdmin: false }}
          showAdminLink
        />
      </Providers>
    );

    expect(
      screen.getByText("Masuk sebagai budi_developer")
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("link", { name: "Admin Panel" })
    ).not.toBeInTheDocument();
  });
});
