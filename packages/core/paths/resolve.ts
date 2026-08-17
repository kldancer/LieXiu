import type { Workspace } from "../types";
import { paths } from "./paths";

/**
 * Resolve the post-auth destination without a user-created workspace branch.
 * A canonical workspace is selected by the server bootstrap/session contract;
 * an empty result returns to login so the local bootstrap flow can establish
 * the owner and canonical workspace.
 *
 * The second argument is accepted for source compatibility with applications
 * that have not yet removed the former onboarding flag.
 */
export function resolvePostAuthDestination(
  workspaces: Workspace[],
  _legacyOnboarded?: boolean,
): string {
  const first = workspaces[0];
  return first ? paths.workspace(first.slug).issues() : paths.login();
}
