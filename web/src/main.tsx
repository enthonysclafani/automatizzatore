import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { isApiError, setUnauthenticatedHandler } from "./api/client";
import "./index.css";
import { safeRedirect } from "./lib/nav";
import { initTheme } from "./lib/theme";
import { makeRouter } from "./router";

initTheme();

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5_000,
      refetchOnWindowFocus: true,
      // Do not retry answers that will not change: missing, forbidden, signed out, bad input.
      retry: (count, err) => {
        if (isApiError(err) && err.status >= 400 && err.status < 500) return false;
        return count < 2;
      },
    },
  },
});

const router = makeRouter(queryClient);

// A 401 from any call means the session ended: go to sign-in and come back afterwards.
let redirecting = false;
setUnauthenticatedHandler((returnTo) => {
  if (redirecting) return;
  redirecting = true;
  queryClient.clear();
  void router.navigate({ to: "/login", search: { redirect: safeRedirect(returnTo) } }).finally(() => {
    redirecting = false;
  });
});

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("Missing #root element in index.html");

createRoot(rootEl).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
