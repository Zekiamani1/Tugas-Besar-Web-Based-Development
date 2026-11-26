import type { ReactNode } from "react";

export function Card({
  title,
  actions,
  wide = false,
  children,
}: {
  title?: string;
  actions?: ReactNode;
  wide?: boolean;
  children: ReactNode;
}) {
  const className = wide ? "card card-wide" : "card";

  return (
    <section className={className}>
      {title ? (
        actions ? (
          <div className="section-header">
            <h2>{title}</h2>
            <div>{actions}</div>
          </div>
        ) : (
          <h2>{title}</h2>
        )
      ) : null}
      {children}
    </section>
  );
}
