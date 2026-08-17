"use client";

import React, { useEffect, useRef } from "react";
import { cn } from "@liexiu/ui/lib/utils";
import { useScrollFade } from "@liexiu/ui/hooks/use-scroll-fade";
import { AppLink, useNavigation } from "../navigation";
import { HelpLauncher } from "./help-launcher";
import { JoinDiscordCard } from "./join-discord-card";
import { ChevronDown, LogOut, SquarePen } from "lucide-react";
import { WorkspaceAvatar } from "../workspace/workspace-avatar";
import { ActorAvatar } from "@liexiu/ui/components/common/actor-avatar";
import { useIssueDraftStore } from "@liexiu/core/issues/stores/draft-store";
import { openCreateIssueWithPreference } from "@liexiu/core/issues/stores/create-mode-store";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
  useSidebar,
} from "@liexiu/ui/components/ui/sidebar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@liexiu/ui/components/ui/dropdown-menu";
import { useAuthStore } from "@liexiu/core/auth";
import { useCurrentWorkspace, useWorkspacePaths } from "@liexiu/core/paths";
import { resolvePublicFileUrl } from "@liexiu/core/workspace/avatar-url";
import { useLogout } from "../auth";
import { routeIconForPath } from "./route-icon-components";
import { useT } from "../i18n";
import { useShortcut } from "@liexiu/core/shortcuts";
import { ShortcutKeycaps } from "../common/shortcut-keycaps";

function isNavActive(pathname: string, href: string): boolean {
  return pathname === href || pathname.startsWith(`${href}/`);
}

const DEFAULT_PRODUCT_NAME = "LieXiu";

function configuredProductName(): string {
  const configured = process.env.NEXT_PUBLIC_PRODUCT_NAME?.trim();
  return configured || DEFAULT_PRODUCT_NAME;
}

type NavKey =
  | "myIssues"
  | "issues"
  | "projects"
  | "agents"
  | "usage"
  | "runtimes"
  | "skills"
  | "settings";

type NavLabelKey =
  | "my_issues"
  | "issues"
  | "projects"
  | "agents"
  | "usage"
  | "runtimes"
  | "skills"
  | "settings";

const personalNav: { key: NavKey; labelKey: NavLabelKey }[] = [
  { key: "myIssues", labelKey: "my_issues" },
];

const workspaceNav: { key: NavKey; labelKey: NavLabelKey }[] = [
  { key: "issues", labelKey: "issues" },
  { key: "projects", labelKey: "projects" },
  { key: "agents", labelKey: "agents" },
  { key: "usage", labelKey: "usage" },
];

const configureNav: { key: NavKey; labelKey: NavLabelKey }[] = [
  { key: "runtimes", labelKey: "runtimes" },
  { key: "skills", labelKey: "skills" },
  { key: "settings", labelKey: "settings" },
];

function DraftDot() {
  const hasDraft = useIssueDraftStore((s) => s.hasDraft());
  if (!hasDraft) return null;
  return <span className="absolute right-0 top-0 size-1.5 rounded-full bg-brand" />;
}

interface AppSidebarProps {
  topSlot?: React.ReactNode;
  searchSlot?: React.ReactNode;
  headerClassName?: string;
  headerStyle?: React.CSSProperties;
  productName?: string;
}

export function AppSidebar({
  topSlot,
  searchSlot,
  headerClassName,
  headerStyle,
  productName = configuredProductName(),
}: AppSidebarProps = {}) {
  const { t } = useT("layout");
  const { pathname } = useNavigation();
  const user = useAuthStore((s) => s.user);
  const logout = useLogout();
  const workspace = useCurrentWorkspace();
  const paths = useWorkspacePaths();
  const { setOpenMobile } = useSidebar();
  const sidebarScrollRef = useRef<HTMLDivElement>(null);
  const sidebarFadeStyle = useScrollFade(sidebarScrollRef, 24);
  const createIssueShortcut = useShortcut("createIssue");

  useEffect(() => {
    setOpenMobile(false);
  }, [pathname, setOpenMobile]);

  const renderNav = (items: { key: NavKey; labelKey: NavLabelKey }[]) => (
    <SidebarMenu className="gap-0.5">
      {items.map((item) => {
        const href = paths[item.key]();
        const Icon = routeIconForPath(href);
        return (
          <SidebarMenuItem key={item.key}>
            <SidebarMenuButton
              isActive={isNavActive(pathname, href)}
              render={<AppLink href={href} />}
              className="text-muted-foreground hover:not-data-active:bg-sidebar-accent/70 data-active:bg-sidebar-accent data-active:text-sidebar-accent-foreground"
            >
              <Icon />
              <span>{t(($) => $.nav[item.labelKey])}</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        );
      })}
    </SidebarMenu>
  );

  return (
    <Sidebar variant="inset">
      {topSlot}
      <SidebarHeader className={cn("py-3", headerClassName)} style={headerStyle}>
        <SidebarMenu>
          <SidebarMenuItem>
            <DropdownMenu>
              <DropdownMenuTrigger
                render={
                  <SidebarMenuButton>
                    <WorkspaceAvatar name={workspace?.name ?? "M"} avatarUrl={workspace?.avatar_url} size="sm" />
                    <span className="flex-1 truncate font-medium">{workspace?.name ?? productName}</span>
                    <ChevronDown className="size-3 text-muted-foreground" />
                  </SidebarMenuButton>
                }
              />
              <DropdownMenuContent className="w-auto min-w-56" align="start" side="bottom" sideOffset={4}>
                <div className="flex items-center gap-2.5 px-2 py-1.5">
                  <ActorAvatar
                    name={user?.name ?? ""}
                    initials={(user?.name ?? "U").charAt(0).toUpperCase()}
                    avatarUrl={resolvePublicFileUrl(user?.avatar_url)}
                    size="lg"
                  />
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-body font-medium leading-tight">{user?.name}</p>
                    <p className="truncate text-caption text-muted-foreground leading-tight">{user?.email}</p>
                  </div>
                </div>
                <DropdownMenuSeparator />
                <DropdownMenuGroup>
                  <DropdownMenuItem variant="destructive" onClick={logout}>
                    <LogOut className="h-3.5 w-3.5" />
                    {t(($) => $.sidebar.log_out)}
                  </DropdownMenuItem>
                </DropdownMenuGroup>
              </DropdownMenuContent>
            </DropdownMenu>
          </SidebarMenuItem>
        </SidebarMenu>
        <SidebarMenu>
          {searchSlot && <SidebarMenuItem>{searchSlot}</SidebarMenuItem>}
          <SidebarMenuItem>
            <SidebarMenuButton className="text-muted-foreground" onClick={() => openCreateIssueWithPreference()}>
              <span className="relative">
                <SquarePen />
                <DraftDot />
              </span>
              <span>{t(($) => $.sidebar.new_issue)}</span>
              {createIssueShortcut ? <ShortcutKeycaps shortcut={createIssueShortcut} decorative className="pointer-events-none ml-auto" /> : null}
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>
      <SidebarContent ref={sidebarScrollRef} style={sidebarFadeStyle}>
        <SidebarGroup>
          <SidebarGroupContent>{renderNav(personalNav)}</SidebarGroupContent>
        </SidebarGroup>
        <SidebarGroup>
          <SidebarGroupLabel>{t(($) => $.sidebar.workspace_group)}</SidebarGroupLabel>
          <SidebarGroupContent>{renderNav(workspaceNav)}</SidebarGroupContent>
        </SidebarGroup>
        <SidebarGroup>
          <SidebarGroupLabel>{t(($) => $.sidebar.configure_group)}</SidebarGroupLabel>
          <SidebarGroupContent>{renderNav(configureNav)}</SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
      <SidebarFooter className="p-2">
        <div className="flex items-center justify-end gap-1">
          <JoinDiscordCard />
          <HelpLauncher />
        </div>
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  );
}
