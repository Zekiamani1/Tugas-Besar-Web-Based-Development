import type { ReactNode } from "react";

export function PageLayout({
  subtitle,
  title,
  nav,
  children,
}: {
  subtitle: string;
  title: string;
  nav?: ReactNode;
  children: ReactNode;
}) {
  return (
    <main className="page">
      <header className="page-header">
        <div>
          <p className="eyebrow">{subtitle}</p>
          <h1>{title}</h1>
        </div>
        {nav ? <nav aria-label="Navigasi utama">{nav}</nav> : null}
      </header>
      {children}
    </main>
  );
}
