"use client";

import { useEffect, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { getApi } from "../api";
import { useAuthStore } from "../auth";
import { configStore } from "../config";
import { workspaceKeys } from "../workspace/queries";
import { createLogger } from "../logger";
import { defaultStorage } from "./storage";
import { setCurrentWorkspace } from "./workspace-storage";
import type { ClientIdentity } from "./types";
import type { StorageAdapter } from "../types/storage";
import type { User } from "../types";

const logger = createLogger("auth");

export function AuthInitializer({
  children,
  onLogin,
  onLogout,
  storage = defaultStorage,
  cookieAuth,
}: {
  children: ReactNode;
  onLogin?: () => void;
  onLogout?: () => void;
  storage?: StorageAdapter;
  cookieAuth?: boolean;
  identity?: ClientIdentity;
}) {
  const qc = useQueryClient();

  useEffect(() => {
    const api = getApi();

    // Fetch app config (CDN domain and runtime settings) in the background — non-blocking.
    api
      .getConfig()
      .then((cfg) => {
        if (cfg.cdn_domain) {
          configStore.getState().setCdnConfig({
            cdnDomain: cfg.cdn_domain,
            // Old servers omit this — false keeps the previous behavior.
            cdnSigned: cfg.cdn_signed === true,
          });
        }
        configStore.getState().setAuthConfig({
          // Absent/false on the managed cloud and older servers → section hidden.
          vcsIntegrationAvailable: cfg.vcs_integration_available === true,
        });
        configStore.getState().setDaemonConfig({
          daemonServerUrl: cfg.daemon_server_url,
          daemonAppUrl: cfg.daemon_app_url,
        });
        configStore.getState().setFeatureFlags(cfg.feature_flags);
        configStore.getState().setServerVersion(cfg.server_version);
      })
      .catch(() => {
        /* config is optional — legacy file card matching degrades gracefully */
      });

    const onAuthSuccess = (user: User) => {
      onLogin?.();
      useAuthStore.setState({ user, isLoading: false });
    };

    const onAuthFailure = () => {
      onLogout?.();
      useAuthStore.setState({ user: null, isLoading: false });
    };

    if (cookieAuth) {
      // Cookie mode: the HttpOnly cookie is sent automatically by the browser.
      // Call the API to check if the session is still valid.
      //
      // Seed the canonical workspace as the one-element internal cache shape
      // so the URL-driven layout can resolve the slug without a second fetch.
      // The active workspace itself is derived from the URL by
      // [workspaceSlug]/layout.tsx — no imperative selection here.
      Promise.all([api.getMe(), api.listWorkspaces()])
        .then(([user, wsList]) => {
          onAuthSuccess(user);
          qc.setQueryData(workspaceKeys.list(), wsList);
        })
        .catch((err) => {
          logger.error("cookie auth init failed", err);
          onAuthFailure();
        });
      return;
    }

    // Token mode: read from localStorage (Electron / legacy).
    const token = storage.getItem("liexiu_token");
    if (!token) {
      onLogout?.();
      useAuthStore.setState({ isLoading: false });
      return;
    }

    api.setToken(token);

    Promise.all([api.getMe(), api.listWorkspaces()])
      .then(([user, wsList]) => {
        onAuthSuccess(user);
        // Seed the canonical workspace cache so the URL-driven layout can
        // resolve the slug without a second fetch.
        qc.setQueryData(workspaceKeys.list(), wsList);
      })
      .catch((err) => {
        logger.error("auth init failed", err);
        api.setToken(null);
        setCurrentWorkspace(null, null);
        storage.removeItem("liexiu_token");
        onAuthFailure();
      });
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return <>{children}</>;
}
