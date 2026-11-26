"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useContext } from "react";
import { ServicesContext } from "@/lib/api/context";
import type { UserSummary } from "@/lib/types";

export function AccountNav({
  user,
  showAdminLink = false,
}: {
  user: UserSummary | null;
  showAdminLink?: boolean;
}) {
  const router = useRouter();
  const { auth: authService } = useContext(ServicesContext);

  if (!user) {
    return null;
  }

  async function handleLogout() {
    if (!authService) return;
    await authService.logout();
    router.push("/login");
    router.refresh();
  }

  return (
    <>
      <span className="muted">Masuk sebagai {user.username}</span>
      {showAdminLink && user.isAdmin ? (
        <Link href="/admin">Admin Panel</Link>
      ) : null}
      <button type="button" onClick={() => void handleLogout()}>
        Logout
      </button>
    </>
  );
}
