package featureflags

import (
	"context"

	"github.com/kailonyang/liexiu/server/pkg/featureflag"
)

const (
	// agentSkillTogglesCompat is no longer a release flag. Keep publishing the
	// key as enabled so installed v0.4.0 desktop clients, which still gate the
	// switch on this config decision, receive the permanently enabled behavior.
	agentSkillTogglesCompat = "agents_skill_toggles"
	// resourceLabelsCompat is no longer a release flag. Keep publishing the key
	// as enabled for installed desktop clients from v0.4.0 through at least
	// v0.4.15, every release shipped before this change. Unlike the skill-toggle
	// gate above, which was removed client-side in v0.4.1, the resource-label
	// gate remained in every such client and fails closed (default false) if
	// the key stops being published.
	resourceLabelsCompat = "settings_resource_labels"
)

func EvaluateFrontendPublicFlags(ctx context.Context, flags *featureflag.Service) map[string]bool {
	out := make(map[string]bool, 2)
	out[agentSkillTogglesCompat] = true
	out[resourceLabelsCompat] = true
	return out
}
