import { useEffect, useSyncExternalStore } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { motion } from "motion/react";
import { cn } from "@liexiu/ui/lib/utils";
import {
  useNavigationInputBindings,
  useTabHistory,
} from "@/hooks/use-tab-history";
import {
  SidebarProvider,
  SidebarTrigger,
  useSidebar,
} from "@liexiu/ui/components/ui/sidebar";
import { ModalRegistry } from "@liexiu/views/modals/registry";
import { AppSidebar, GlobalShortcuts } from "@liexiu/views/layout";
import { SearchCommand, SearchTrigger } from "@liexiu/views/search";
import { WorkspaceSlugProvider } from "@liexiu/core/paths";
import { type LinkClickIntent } from "@liexiu/views/navigation";
import { getCurrentSlug, subscribeToCurrentSlug } from "@liexiu/core/platform";
import {
  DesktopNavigationProvider,
  routeContentLinkPath,
} from "@/platform/navigation";
import { TabBar } from "./tab-bar";
import { TabContent } from "./tab-content";

const TOP_BAR_HEIGHT_CLASS = "h-12";
const WINDOW_TOOLBAR_CLEARANCE = 184;
const toolbarMotion = {
  type: "spring",
  stiffness: 420,
  damping: 38,
  mass: 0.8,
} as const;

function WindowToolbar() {
  const { canGoBack, canGoForward, goBack, goForward } = useTabHistory();
  const navButtonClassName =
    "flex size-7 items-center justify-center rounded-md text-faint-foreground transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground disabled:pointer-events-none disabled:opacity-30";

  return (
    <div
      className={cn(
        "fixed left-0 top-0 z-30 flex w-[184px] shrink-0 items-center px-3",
        TOP_BAR_HEIGHT_CLASS,
      )}
      style={{ WebkitAppRegion: "drag" } as React.CSSProperties}
    >
      <div
        className="flex items-center gap-1 pl-[70px]"
        style={{ WebkitAppRegion: "no-drag" } as React.CSSProperties}
      >
        <SidebarTrigger
          className="size-7 text-faint-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
          style={{ WebkitAppRegion: "no-drag" } as React.CSSProperties}
        />
        <div className="flex items-center gap-1">
          <button
            type="button"
            onClick={goBack}
            disabled={!canGoBack}
            aria-label="Go back"
            title="Go back"
            className={navButtonClassName}
            style={{ WebkitAppRegion: "no-drag" } as React.CSSProperties}
          >
            <ChevronLeft className="size-4" />
          </button>
          <button
            type="button"
            onClick={goForward}
            disabled={!canGoForward}
            aria-label="Go forward"
            title="Go forward"
            className={navButtonClassName}
            style={{ WebkitAppRegion: "no-drag" } as React.CSSProperties}
          >
            <ChevronRight className="size-4" />
          </button>
        </div>
      </div>
    </div>
  );
}

function SidebarTopSpacer() {
  return <div className={cn("shrink-0", TOP_BAR_HEIGHT_CLASS)} />;
}

function useNativeNavigationGestures() {
  const { goBack, goForward } = useTabHistory();

  useEffect(() => {
    return window.desktopAPI.onNavigationGesture((gesture) => {
      if (gesture === "back") {
        goBack();
      } else {
        goForward();
      }
    });
  }, [goBack, goForward]);
}


// The main area's top bar doubles as a window drag region. When the sidebar
// is not occupying main-flow width, leave room for the fixed window toolbar
// so tabs do not land beneath the traffic lights / navigation controls.
function MainTopBar() {
  const { state, isCompact } = useSidebar();
  const sidebarHidden = state === "collapsed" || isCompact;

  return (
    <motion.header
      animate={{ paddingLeft: sidebarHidden ? WINDOW_TOOLBAR_CLEARANCE : 0 }}
      className={cn("relative shrink-0 flex items-center gap-2", TOP_BAR_HEIGHT_CLASS)}
      initial={false}
      transition={toolbarMotion}
    >
      <motion.div
        aria-hidden
        animate={{ left: sidebarHidden ? WINDOW_TOOLBAR_CLEARANCE : 0 }}
        className="absolute inset-y-0 right-0"
        initial={false}
        transition={toolbarMotion}
        style={{ WebkitAppRegion: "drag" } as React.CSSProperties}
      />
      <div className="relative z-10 flex h-full min-w-0 max-w-full items-center">
        <TabBar />
      </div>
    </motion.header>
  );
}

// The canvas hugs the expanded sidebar with a hairline gap. When the sidebar
// leaves the main flow, the left margin must grow to mirror the fixed mr-2 so
// the floating canvas sits symmetrically inside the window frame.
function MainCanvas({ children }: { children: React.ReactNode }) {
  const { state, isCompact } = useSidebar();
  const sidebarHidden = state === "collapsed" || isCompact;

  return (
    <motion.div
      animate={{ marginLeft: sidebarHidden ? 8 : 2 }}
      className="relative flex flex-1 min-h-0 flex-col overflow-hidden mr-2 mb-2 rounded-xl bg-page-canvas ring-1 ring-surface-border shadow-[var(--surface-shadow)]"
      initial={false}
      transition={toolbarMotion}
    >
      {children}
    </motion.div>
  );
}

function useInternalLinkHandler() {
  useEffect(() => {
    const handler = (e: Event) => {
      const detail = (
        e as CustomEvent<{ path?: string; disposition?: LinkClickIntent }>
      ).detail;
      if (!detail?.path) return;
      routeContentLinkPath(detail.path, detail.disposition);
    };
    window.addEventListener("liexiu:navigate", handler);
    return () => window.removeEventListener("liexiu:navigate", handler);
  }, []);
}

export function DesktopShell() {
  useInternalLinkHandler();
  useNativeNavigationGestures();
  useNavigationInputBindings();

  // Reactive read of current workspace slug from the platform singleton.
  // On first mount, slug is null until WorkspaceRouteLayout (inside the tab
  // router) sets it. Once set, the sidebar and other shell-level components
  // can resolve workspace-scoped paths via useWorkspacePaths().
  const slug = useSyncExternalStore(subscribeToCurrentSlug, getCurrentSlug, () => null);

  return (
    <DesktopNavigationProvider>
      {/* WorkspaceSlugProvider accepts null — components that need slug
          use useWorkspaceSlug() (nullable) or useRequiredWorkspaceSlug()
          (throws). TabContent MUST always render so the tab router can
          mount WorkspaceRouteLayout, which calls setCurrentWorkspace()
          to populate the slug. The sidebar gates on slug being present
          to avoid the useRequiredWorkspaceSlug throw. */}
      <WorkspaceSlugProvider slug={slug}>
        <div className="flex h-screen bg-app-shell">
          {/* bg-app-shell is the wrapper's non-inset fill, so it also owns the
              non-inset half of --sidebar-wrapper-fill. sidebar.tsx supplies the
              inset half of both. Anything that has to paint an opaque layer
              over this wrapper (the tab flares) reads the variable rather than
              re-deriving which of the two is in play. */}
          <SidebarProvider className="flex-1 bg-app-shell [--sidebar-wrapper-fill:var(--app-shell)]">
            {slug && <GlobalShortcuts />}
            {slug && <WindowToolbar />}
            {slug && <AppSidebar topSlot={<SidebarTopSpacer />} searchSlot={<SearchTrigger />} />}
            {/* Right side: header + content container */}
            <div className="flex flex-1 min-w-0 flex-col">
              <MainTopBar />
              <MainCanvas>
                <TabContent />
              </MainCanvas>
            </div>
          </SidebarProvider>
        </div>
        {slug && <ModalRegistry />}
        {slug && <SearchCommand />}
      </WorkspaceSlugProvider>
    </DesktopNavigationProvider>
  );
}
