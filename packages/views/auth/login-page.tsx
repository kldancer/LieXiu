"use client";

import { useEffect, useRef, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@liexiu/ui/components/ui/card";
import { Button } from "@liexiu/ui/components/ui/button";
import { Input } from "@liexiu/ui/components/ui/input";
import { Label } from "@liexiu/ui/components/ui/label";
import { api } from "@liexiu/core/api";
import { useAuthStore } from "@liexiu/core/auth";
import { workspaceKeys } from "@liexiu/core/workspace/queries";
import type {
  BootstrapRequest,
  BootstrapStatus,
  User,
  Workspace,
} from "@liexiu/core/types";
import { useT } from "../i18n";

interface CliCallbackConfig {
  url: string;
  state: string;
}

interface LoginPageProps {
  logo?: ReactNode;
  onSuccess: (workspace?: Workspace) => void | Promise<void>;
  cliCallback?: CliCallbackConfig;
  onTokenObtained?: () => void;
  extra?: ReactNode;
  localBootstrap?: boolean;
  productName?: string;
}

const DEFAULT_PRODUCT_NAME = "LieXiu";

function configuredProductName(): string {
  const configured = process.env.NEXT_PUBLIC_PRODUCT_NAME?.trim();
  return configured || DEFAULT_PRODUCT_NAME;
}

export function redirectToCliCallback(url: string, token: string, state: string) {
  const separator = url.includes("?") ? "&" : "?";
  window.location.href = `${url}${separator}token=${encodeURIComponent(token)}&state=${encodeURIComponent(state)}`;
}

export function validateCliCallback(cliCallback: string): boolean {
  try {
    const cbUrl = new URL(cliCallback);
    if (cbUrl.protocol !== "http:") return false;
    const host = cbUrl.hostname;
    if (host === "localhost" || host === "127.0.0.1") return true;
    if (/^10\./.test(host)) return true;
    if (/^172\.(1[6-9]|2\d|3[01])\./.test(host)) return true;
    return /^192\.168\./.test(host);
  } catch {
    return false;
  }
}

export function LoginPage({
  logo,
  onSuccess,
  cliCallback,
  onTokenObtained,
  extra,
  localBootstrap = true,
  productName = configuredProductName(),
}: LoginPageProps) {
  const { t } = useT("auth");
  const queryClient = useQueryClient();
  const [existingUser, setExistingUser] = useState<User | null>(null);
  const [bootstrapStatus, setBootstrapStatus] = useState<BootstrapStatus | null>(null);
  const [statusError, setStatusError] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const [bootstrapForm, setBootstrapForm] = useState({
    secret: "",
    ownerName: "",
    ownerEmail: "",
    workspaceName: "",
    workspaceSlug: "",
    workspaceId: "",
  });
  const authSourceRef = useRef<"cookie" | "localStorage">("cookie");

  useEffect(() => {
    if (!localBootstrap) {
      setStatusError(true);
      return;
    }
    let active = true;
    api
      .getBootstrapStatus()
      .then((status) => {
        if (active) setBootstrapStatus(status);
      })
      .catch(() => {
        if (active) setStatusError(true);
      });
    return () => {
      active = false;
    };
  }, [localBootstrap]);

  useEffect(() => {
    if (!cliCallback) return;

    api.setToken(null);
    api
      .getMe()
      .then((user) => {
        authSourceRef.current = "cookie";
        setExistingUser(user);
      })
      .catch(() => {
        const token = localStorage.getItem("liexiu_token");
        if (!token) return;
        api.setToken(token);
        api
          .getMe()
          .then((user) => {
            authSourceRef.current = "localStorage";
            setExistingUser(user);
          })
          .catch(() => {
            api.setToken(null);
            localStorage.removeItem("liexiu_token");
          });
      });
  }, [cliCallback]);

  const handleCliAuthorize = async () => {
    if (!cliCallback) return;
    setSubmitting(true);
    setError("");
    try {
      const token =
        authSourceRef.current === "localStorage"
          ? localStorage.getItem("liexiu_token")
          : (await api.issueCliToken()).token;
      if (!token) throw new Error("token missing");
      onTokenObtained?.();
      redirectToCliCallback(cliCallback.url, token, cliCallback.state);
    } catch {
      setError(t(($) => $.errors.cli_auth_failed));
      setSubmitting(false);
    }
  };

  const handleBootstrap = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!bootstrapStatus?.enabled) return;

    const needsOwnerFields = !bootstrapStatus.initialized;
    const needsOwnerEmail = needsOwnerFields || bootstrapStatus.requires_selection;
    const needsWorkspaceId = bootstrapStatus.requires_selection;
    const requiredValues = [
      bootstrapForm.secret,
      ...(needsOwnerFields
        ? [bootstrapForm.ownerName, bootstrapForm.workspaceName, bootstrapForm.workspaceSlug]
        : []),
      ...(needsOwnerEmail ? [bootstrapForm.ownerEmail] : []),
      ...(needsWorkspaceId ? [bootstrapForm.workspaceId] : []),
    ];
    if (requiredValues.some((value) => !value.trim())) {
      setError(t(($) => $.bootstrap.required));
      return;
    }

    const request: BootstrapRequest = {
      secret: bootstrapForm.secret,
      owner_name: needsOwnerFields ? bootstrapForm.ownerName.trim() : "",
      owner_email: needsOwnerEmail ? bootstrapForm.ownerEmail.trim() : "",
      workspace_name: needsOwnerFields ? bootstrapForm.workspaceName.trim() : "",
      workspace_slug: needsOwnerFields ? bootstrapForm.workspaceSlug.trim() : "",
      workspace_id: needsWorkspaceId ? bootstrapForm.workspaceId.trim() : "",
    };

    setSubmitting(true);
    setError("");
    try {
      const response = await useAuthStore.getState().bootstrap(request);
      queryClient.setQueryData(workspaceKeys.list(), [response.workspace]);
      onTokenObtained?.();
      if (cliCallback) {
        redirectToCliCallback(cliCallback.url, response.token, cliCallback.state);
        return;
      }
      await onSuccess(response.workspace);
    } catch (err) {
      setError(err instanceof Error ? err.message : t(($) => $.bootstrap.failed));
    } finally {
      setSubmitting(false);
      setBootstrapForm((current) => ({ ...current, secret: "" }));
    }
  };

  if (cliCallback && existingUser) {
    return (
      <div className="flex min-h-svh items-center justify-center">
        <Card className="w-full max-w-sm">
          <CardHeader className="text-center">
            {logo && <div className="mx-auto mb-4">{logo}</div>}
            <CardTitle className="text-display-sm">{t(($) => $.cli.title, { productName })}</CardTitle>
            <CardDescription>
              {t(($) => $.cli.description, { email: existingUser.email, productName })}
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <Button onClick={handleCliAuthorize} disabled={submitting} size="lg">
              {submitting ? t(($) => $.cli.authorizing) : t(($) => $.cli.authorize)}
            </Button>
            <Button
              variant="ghost"
              onClick={() => {
                setExistingUser(null);
                setError("");
              }}
            >
              {t(($) => $.cli.different_account)}
            </Button>
            {error && <p className="text-body text-destructive">{error}</p>}
          </CardContent>
        </Card>
      </div>
    );
  }

  if (!bootstrapStatus && !statusError) {
    return (
      <div className="flex min-h-svh items-center justify-center" role="status">
        <Card className="w-full max-w-sm">
          <CardHeader className="text-center">
            {logo && <div className="mx-auto mb-4">{logo}</div>}
            <CardTitle className="text-display-sm">{t(($) => $.bootstrap.title, { productName })}</CardTitle>
            <CardDescription>{t(($) => $.bootstrap.description)}</CardDescription>
          </CardHeader>
        </Card>
      </div>
    );
  }

  if (statusError || !bootstrapStatus?.enabled) {
    return (
      <div className="flex min-h-svh items-center justify-center">
        <Card className="w-full max-w-sm">
          <CardHeader className="text-center">
            {logo && <div className="mx-auto mb-4">{logo}</div>}
            <CardTitle className="text-display-sm">{t(($) => $.bootstrap.title, { productName })}</CardTitle>
            <CardDescription>{t(($) => $.bootstrap.failed)}</CardDescription>
          </CardHeader>
        </Card>
      </div>
    );
  }

  const needsOwnerFields = !bootstrapStatus.initialized;
  const needsOwnerEmail = needsOwnerFields || bootstrapStatus.requires_selection;
  const needsWorkspaceId = bootstrapStatus.requires_selection;

  return (
    <div className="flex min-h-svh items-center justify-center">
      <Card className="w-full max-w-sm">
        <CardHeader className="text-center">
          {logo && <div className="mx-auto mb-4">{logo}</div>}
          <CardTitle className="text-display-sm">{t(($) => $.bootstrap.title, { productName })}</CardTitle>
          <CardDescription>{t(($) => $.bootstrap.description)}</CardDescription>
        </CardHeader>
        <CardContent>
          <form id="bootstrap-form" onSubmit={handleBootstrap} className="space-y-4">
            <BootstrapField
              id="bootstrap-secret"
              label={t(($) => $.bootstrap.secret)}
              type="password"
              value={bootstrapForm.secret}
              onChange={(secret) => setBootstrapForm((current) => ({ ...current, secret }))}
              autoFocus
            />
            {needsOwnerFields && (
              <>
                <BootstrapField
                  id="bootstrap-owner-name"
                  label={t(($) => $.bootstrap.owner_name)}
                  value={bootstrapForm.ownerName}
                  onChange={(ownerName) => setBootstrapForm((current) => ({ ...current, ownerName }))}
                />
                <BootstrapField
                  id="bootstrap-owner-email"
                  label={t(($) => $.bootstrap.owner_email)}
                  type="email"
                  value={bootstrapForm.ownerEmail}
                  onChange={(ownerEmail) => setBootstrapForm((current) => ({ ...current, ownerEmail }))}
                />
                <BootstrapField
                  id="bootstrap-workspace-name"
                  label={t(($) => $.bootstrap.workspace_name)}
                  value={bootstrapForm.workspaceName}
                  onChange={(workspaceName) => setBootstrapForm((current) => ({ ...current, workspaceName }))}
                />
                <BootstrapField
                  id="bootstrap-workspace-slug"
                  label={t(($) => $.bootstrap.workspace_slug)}
                  value={bootstrapForm.workspaceSlug}
                  onChange={(workspaceSlug) => setBootstrapForm((current) => ({ ...current, workspaceSlug }))}
                />
              </>
            )}
            {needsOwnerEmail && !needsOwnerFields && (
              <BootstrapField
                id="bootstrap-owner-email"
                label={t(($) => $.bootstrap.owner_email)}
                type="email"
                value={bootstrapForm.ownerEmail}
                onChange={(ownerEmail) => setBootstrapForm((current) => ({ ...current, ownerEmail }))}
              />
            )}
            {needsWorkspaceId && (
              <BootstrapField
                id="bootstrap-workspace-id"
                label={t(($) => $.bootstrap.workspace_id)}
                value={bootstrapForm.workspaceId}
                onChange={(workspaceId) => setBootstrapForm((current) => ({ ...current, workspaceId }))}
              />
            )}
            {error && <p className="text-body text-destructive">{error}</p>}
          </form>
        </CardContent>
        <CardFooter className="flex flex-col gap-3">
          <Button type="submit" form="bootstrap-form" className="w-full" size="lg" disabled={submitting}>
            {submitting ? t(($) => $.bootstrap.submitting) : t(($) => $.bootstrap.submit, { productName })}
          </Button>
          {extra && <div className="w-full pt-1 text-center">{extra}</div>}
        </CardFooter>
      </Card>
    </div>
  );
}

function BootstrapField({
  id,
  label,
  value,
  onChange,
  type = "text",
  autoFocus = false,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  type?: string;
  autoFocus?: boolean;
}) {
  return (
    <div className="space-y-2">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        type={type}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        autoFocus={autoFocus}
        required
      />
    </div>
  );
}
