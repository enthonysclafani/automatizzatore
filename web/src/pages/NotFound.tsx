import { Link } from "@tanstack/react-router";
import { buttonClass } from "../components/ui/Button";
import { EmptyState } from "../components/ui/States";

export function NotFoundPage() {
  return (
    <div className="flex min-h-[60dvh] items-center justify-center">
      <EmptyState
        title="Page not found"
        hint="The address may be mistyped, or the page has moved."
        action={
          <Link to="/" className={buttonClass("secondary", "sm")}>
            Go to the dashboard
          </Link>
        }
      />
    </div>
  );
}
