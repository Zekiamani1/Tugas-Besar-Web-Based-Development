export function AlertMessage({
  variant,
  message,
}: {
  variant: "error" | "success";
  message?: string | null;
}) {
  if (!message) {
    return null;
  }

  return (
    <p
      className={`message ${variant}`}
      role={variant === "error" ? "alert" : "status"}
    >
      {message}
    </p>
  );
}
