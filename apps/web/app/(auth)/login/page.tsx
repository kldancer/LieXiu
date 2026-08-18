"use client";

import { Suspense, useEffect } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useQueryClient } from "@tanstack/react-query";
import { sanitizeNextUrl, useAuthStore } from "@liexiu/core/auth";
import { paths } from "@liexiu/core/paths";
import { workspaceKeys, workspaceListOptions } from "@liexiu/core/workspace/queries";
import type { Workspace } from "@liexiu/core/types";
import { LoginPage, validateCliCallback } from "@liexiu/views/auth";
import { setLoggedInCookie } from "@/features/auth/auth-cookie";

function LoginPageContent() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const queryClient = useQueryClient();
  const user = useAuthStore((state) => state.user);
  const isLoading = useAuthStore((state) => state.isLoading);
  const nextUrl = sanitizeNextUrl(searchParams.get("next"));
  const cliCallbackRaw = searchParams.get("cli_callback");
  const cliState = searchParams.get("cli_state") ?? "";
  const cliCallback =
    cliCallbackRaw && validateCliCallback(cliCallbackRaw)
      ? { url: cliCallbackRaw, state: cliState }
      : undefined;

  useEffect(() => {
    if (isLoading || !user || cliCallback) return;
    if (nextUrl) {
      router.replace(nextUrl);
      return;
    }
    void queryClient
      .ensureQueryData(workspaceListOptions())
      .then(([workspace]) => {
        if (workspace) router.replace(paths.workspace(workspace.slug).issues());
      });
  }, [cliCallback, isLoading, nextUrl, queryClient, router, user]);

  const handleSuccess = async (workspace?: Workspace) => {
    if (nextUrl) {
      router.push(nextUrl);
      return;
    }
    const canonical =
      workspace ??
      queryClient.getQueryData<Workspace[]>(workspaceKeys.list())?.[0] ??
      (await queryClient.ensureQueryData(workspaceListOptions()))[0];
    if (canonical) router.push(paths.workspace(canonical.slug).issues());
  };

  // Personal mode establishes its HttpOnly session during auth
  // initialization. Keep the legacy bootstrap UI out of the first paint so
  // users never see a login form flash before entering the workspace.
  if (isLoading) return null;

  return (
    <LoginPage
      onSuccess={handleSuccess}
      localBootstrap
      cliCallback={cliCallback}
      onTokenObtained={setLoggedInCookie}
    />
  );
}

export default function Page() {
  return (
    <Suspense fallback={null}>
      <LoginPageContent />
    </Suspense>
  );
}
