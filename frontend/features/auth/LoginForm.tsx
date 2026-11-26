"use client";

import { useRouter } from "next/navigation";
import { useContext, useState } from "react";
import { AlertMessage } from "@/components/ui/AlertMessage";
import { ServicesContext } from "@/lib/api/context";

export function LoginForm({ reason }: { reason?: string }) {
  const router = useRouter();
  const { auth: authService } = useContext(ServicesContext);

  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [dismissedReason, setDismissedReason] = useState(false);
  const [isSubmitting, setIsSubmitting] = useState(false);

  const displayedError =
    error ??
    (!dismissedReason && reason === "session"
      ? "Sesi berakhir atau Anda belum login. Silakan masuk kembali."
      : null);

  async function handleSubmit() {
    if (!authService) return;
    setDismissedReason(true);
    setIsSubmitting(true);
    try {
      const result = await authService.login(username, password);
      if (!result.ok) {
        setError(result.error);
        return;
      }
      setError(null);
      router.push("/dashboard");
      router.refresh();
    } finally {
      setIsSubmitting(false);
    }
  }

  return (
    <>
      <AlertMessage variant="error" message={displayedError} />
      <form
        onSubmit={(event) => {
          event.preventDefault();
          void handleSubmit();
        }}
        noValidate
      >
        <label htmlFor="username">Username</label>
        <input
          id="username"
          type="text"
          name="username"
          value={username}
          onChange={(event) => setUsername(event.target.value)}
          autoComplete="username"
          required
        />
        <label htmlFor="password">Password</label>
        <input
          id="password"
          type="password"
          name="password"
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          autoComplete="current-password"
          required
        />
        <button type="submit" disabled={isSubmitting}>
          Masuk
        </button>
      </form>
    </>
  );
}
