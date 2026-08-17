import type { User, Workspace } from "./workspace";

export interface BootstrapStatus {
  enabled: boolean;
  initialized: boolean;
  requires_selection: boolean;
}

export interface BootstrapRequest {
  secret: string;
  owner_name: string;
  owner_email: string;
  workspace_name: string;
  workspace_slug: string;
  workspace_id: string;
}

export interface BootstrapResponse {
  token: string;
  user: User;
  workspace: Workspace;
  provisioned: boolean;
}
