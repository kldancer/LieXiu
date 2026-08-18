// Package agentaccess contains the dependency-free predicates used to decide
// whether an actor may invoke an agent. Query and identity resolution belongs
// to the caller; this package only evaluates already-resolved facts.
package agentaccess

// Principal describes the actor and, for an agent/system trigger, the human
// originator resolved at the top of the chain.
type Principal struct {
	ActorType        string
	ActorID          string
	OriginatorUserID string
}

// EffectiveUserID is the human identity used by the invocation grant. Agent
// and system actors must never be trusted as the effective user themselves.
func (p Principal) EffectiveUserID() string {
	if p.ActorType == "member" {
		return p.ActorID
	}
	return p.OriginatorUserID
}

// GrantFacts are the agent and workspace facts needed by CanInvoke.
type GrantFacts struct {
	OwnerID           string
	PermissionMode    string
	IsWorkspaceMember bool
}

// Target is one invocation grant. Team targets are intentionally represented
// so the predicate can remain explicit and fail closed while team support is
// still reserved.
type Target struct {
	Type string
	ID   string
}

// CanInvoke evaluates the V1 invocation contract:
//   - the owner may always invoke their own agent;
//   - private and unknown permission modes deny everyone else;
//   - public_to workspace targets admit workspace members and workspace
//     internal agent/system principals;
//   - member targets require a resolved effective user;
//   - team and unknown targets are inert.
//
// The caller is responsible for loading targets and determining workspace
// membership. False is returned for incomplete or unknown facts.
func CanInvoke(principal Principal, grant GrantFacts, targets []Target) bool {
	effectiveUser := principal.EffectiveUserID()
	if effectiveUser != "" && grant.OwnerID != "" && grant.OwnerID == effectiveUser {
		return true
	}
	if grant.PermissionMode != "public_to" {
		return false
	}

	workspaceBroad := principal.ActorType == "agent" || principal.ActorType == "system"
	for _, target := range targets {
		switch target.Type {
		case "workspace":
			if grant.IsWorkspaceMember || workspaceBroad {
				return true
			}
		case "member":
			if effectiveUser != "" && target.ID == effectiveUser {
				return true
			}
		case "team":
			// Reserved and inert in V1.
		}
	}
	return false
}
