import { useEffect, useRef } from "react";
import { bucketDiagnosticPath, setDiagnosticRoute } from "@liexiu/core/diagnostics";
import { useAuthStore } from "@liexiu/core/auth";
import { useActiveTabIdentity, useActiveTabUrl } from "@/stores/tab-store";
import type { RendererRouteContextInput } from "../../../shared/renderer-route-context";

/**
 * Publishes which page this window is showing, for freeze diagnostics.
 *
 * Two consumers, because a freeze can be observed from two places:
 *
 *   - The main process, via IPC. It is the only party still alive during a
 *     true hang, and it cannot ask a blocked renderer anything — so the route
 *     has to already be there when the hang starts.
 *   - The in-renderer freeze watchdog, via the shared diagnostic context.
 *     `location.pathname` is the packaged index.html path here (this window
 *     runs a memory router), so without this the watchdog has no route at all.
 *
 * Desktop reports either the logged-out `/login` state or the active tab.
 *
 * Paths are bucketed to route templates before publishing, so local diagnostic
 * facts carry `/:slug/issues/:id` rather than a workspace slug and issue id.
 */
export function DiagnosticRouteReporter() {
  const user = useAuthStore((s) => s.user);
  // The slug decides whether a workspace is mounted at all; it is never sent.
  const { slug: activeWorkspaceSlug } = useActiveTabIdentity();
  // The tab url carries search/hash too; bucketing drops both, so telemetry
  // never sees a filter value or an anchor.
  const activeTabUrl = useActiveTabUrl();

  // Last payload pushed to main, so a re-render doesn't re-send an identical
  // context.
  const lastSentRef = useRef<string | null>(null);

  useEffect(() => {
    const surface = resolveSurface({
      user,
      activeWorkspaceSlug,
      activeTabUrl,
    });
    setDiagnosticRoute(surface.path);

    // Only the bucketed template travels to the main process. The slug and tab
    // id stay in this process as local workspace/session facts.
    const context: RendererRouteContextInput = {
      surface: surface.kind,
      path: surface.path,
    };
    send(context, lastSentRef);
  }, [user, activeWorkspaceSlug, activeTabUrl]);

  return null;
}

function send(
  context: RendererRouteContextInput,
  lastSentRef: { current: string | null },
) {
  const serialized = JSON.stringify(context);
  if (serialized === lastSentRef.current) return;
  lastSentRef.current = serialized;
  window.desktopAPI.setRendererRouteContext(context);
}

function resolveSurface({
  user,
  activeWorkspaceSlug,
  activeTabUrl,
}: {
  user: unknown;
  activeWorkspaceSlug: string | null;
  activeTabUrl: string | null;
}): { kind: RendererRouteContextInput["surface"]; path: string } {
  if (!user) return { kind: "login", path: "/login" };
  if (activeWorkspaceSlug && activeTabUrl) {
    return { kind: "tab", path: bucketDiagnosticPath(activeTabUrl) };
  }
  return { kind: "tab", path: "/" };
}
