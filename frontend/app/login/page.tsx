import type { Metadata } from "next";
import { LoginForm } from "@/features/auth/LoginForm";

export const metadata: Metadata = {
  title: "Login · Database Manager",
};

type SearchParamsPromise = Promise<Record<string, string | string[] | undefined>>;

export default async function LoginPage({
  searchParams,
}: {
  searchParams: SearchParamsPromise;
}) {
  const resolved = await searchParams;
  const rawReason = resolved.reason;
  const reason = Array.isArray(rawReason) ? rawReason[0] : rawReason;

  return (
    <div className="auth-page">
      <main className="auth-card">
        <p className="eyebrow">Database Manager</p>
        <h1>Masuk</h1>
        <p className="muted">Gunakan akun yang diberikan administrator.</p>
        <LoginForm reason={reason} />
      </main>
    </div>
  );
}
