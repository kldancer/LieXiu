package orchestration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

var nodeKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

var allowedArtifactKinds = map[ArtifactKind]struct{}{
	ArtifactKindBranch:        {},
	ArtifactKindCommit:        {},
	ArtifactKindDiff:          {},
	ArtifactKindFile:          {},
	ArtifactKindTestReceipt:   {},
	ArtifactKindFinalDelivery: {},
}

func DecodePlan(data []byte) (Plan, error) {
	var plan Plan
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return Plan{}, fmt.Errorf("decode plan: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return Plan{}, fmt.Errorf("decode plan: multiple JSON values")
		}
		return Plan{}, fmt.Errorf("decode plan: %w", err)
	}
	return plan, nil
}

func ValidatePlan(plan Plan, expectedMissionID string, hardLimits PlanLimits) []ValidationError {
	var errs []ValidationError
	add := func(path, code, message string) {
		errs = append(errs, ValidationError{Path: path, Code: code, Message: message})
	}

	if plan.SchemaVersion != PlanSchemaVersion {
		add("schema_version", "unsupported_schema_version", fmt.Sprintf("schema_version must be %d", PlanSchemaVersion))
	}
	if strings.TrimSpace(plan.MissionID) == "" || plan.MissionID != expectedMissionID {
		add("mission_id", "mission_mismatch", "mission_id must match the target mission")
	}
	if strings.TrimSpace(plan.PlanKey) == "" {
		add("plan_key", "missing_plan_key", "plan_key is required")
	} else if len(plan.PlanKey) > 255 {
		add("plan_key", "plan_key_too_long", "plan_key may not exceed 255 bytes")
	}
	errs = append(errs, validatePlanLimits(plan.Limits, hardLimits)...)

	if len(plan.Nodes) == 0 {
		add("nodes", "missing_nodes", "at least one node is required")
	}
	if len(plan.Nodes) > MaxPlanNodes {
		add("nodes", "node_limit_exceeded", fmt.Sprintf("a plan may contain at most %d nodes", MaxPlanNodes))
	}

	nodesByKey := make(map[string]PlanNode, len(plan.Nodes))
	dependents := make(map[string][]string, len(plan.Nodes))
	hasRoot := false
	graphValid := true
	for index, node := range plan.Nodes {
		path := fmt.Sprintf("nodes[%d]", index)
		key := node.Key
		if key == "" {
			add(path+".key", "missing_node_key", "node key is required")
			graphValid = false
		} else if !nodeKeyPattern.MatchString(key) {
			add(path+".key", "invalid_node_key", "node key must use 1-64 ASCII letters, digits, underscores, or hyphens")
			graphValid = false
		} else if _, exists := nodesByKey[key]; exists {
			add(path+".key", "duplicate_node_key", fmt.Sprintf("node key %q is duplicated", key))
			graphValid = false
		} else {
			nodesByKey[key] = node
		}
		if strings.TrimSpace(node.Title) == "" {
			add(path+".title", "missing_title", "node title is required")
		}
		if strings.TrimSpace(node.Description) == "" {
			add(path+".description", "missing_description", "node description is required")
		}
		if node.Role != RoleExecutor && node.Role != RoleIntegrator {
			add(path+".role", "invalid_node_role", "node role must be executor or integrator")
		}
		errs = append(errs, validateNodeBudgetEstimate(node, index, plan.Limits.Budget)...)
		if len(node.AcceptanceCriteria) == 0 {
			add(path+".acceptance_criteria", "missing_acceptance_criteria", "at least one acceptance criterion is required")
		}
		for criterionIndex, criterion := range node.AcceptanceCriteria {
			if strings.TrimSpace(criterion) == "" {
				add(fmt.Sprintf("%s.acceptance_criteria[%d]", path, criterionIndex), "empty_acceptance_criterion", "acceptance criteria cannot be empty")
			}
		}
		if len(node.ArtifactKinds) == 0 {
			add(path+".artifact_kinds", "missing_artifact_kind", "at least one artifact kind is required")
		}
		artifactKinds := make(map[ArtifactKind]struct{}, len(node.ArtifactKinds))
		hasFinalDelivery := false
		for artifactIndex, kind := range node.ArtifactKinds {
			if _, ok := allowedArtifactKinds[kind]; !ok {
				add(fmt.Sprintf("%s.artifact_kinds[%d]", path, artifactIndex), "invalid_artifact_kind", fmt.Sprintf("artifact kind %q is not supported", kind))
			}
			if _, exists := artifactKinds[kind]; exists {
				add(fmt.Sprintf("%s.artifact_kinds[%d]", path, artifactIndex), "duplicate_artifact_kind", fmt.Sprintf("artifact kind %q is duplicated", kind))
			}
			artifactKinds[kind] = struct{}{}
			hasFinalDelivery = hasFinalDelivery || kind == ArtifactKindFinalDelivery
		}
		if node.Role == RoleIntegrator && !hasFinalDelivery {
			add(path+".artifact_kinds", "missing_final_delivery", "integrator nodes must produce a final_delivery artifact")
		}
		if len(node.DependsOn) == 0 {
			hasRoot = true
		}
		dependencies := make(map[string]struct{}, len(node.DependsOn))
		for dependencyIndex, dependency := range node.DependsOn {
			dependencyPath := fmt.Sprintf("%s.depends_on[%d]", path, dependencyIndex)
			if dependency == node.Key {
				add(dependencyPath, "self_dependency", "a node cannot depend on itself")
				graphValid = false
			}
			if _, exists := dependencies[dependency]; exists {
				add(dependencyPath, "duplicate_dependency", fmt.Sprintf("dependency %q is duplicated", dependency))
				graphValid = false
			}
			dependencies[dependency] = struct{}{}
		}
	}

	for index, node := range plan.Nodes {
		for dependencyIndex, dependency := range node.DependsOn {
			if _, ok := nodesByKey[dependency]; !ok {
				add(
					fmt.Sprintf("nodes[%d].depends_on[%d]", index, dependencyIndex),
					"unknown_dependency",
					fmt.Sprintf("dependency %q does not reference a plan node", dependency),
				)
				graphValid = false
				continue
			}
			dependents[dependency] = append(dependents[dependency], node.Key)
		}
	}
	if !hasRoot {
		add("nodes", "missing_root_node", "the plan must contain at least one root node")
	}

	hasIntegratorLeaf := false
	for index, node := range plan.Nodes {
		if node.Role != RoleIntegrator {
			continue
		}
		if len(dependents[node.Key]) == 0 {
			hasIntegratorLeaf = true
		}
		hasNonIntegratorDependency := false
		for _, dependency := range node.DependsOn {
			if predecessor, ok := nodesByKey[dependency]; ok && predecessor.Role != RoleIntegrator {
				hasNonIntegratorDependency = true
				break
			}
		}
		if !hasNonIntegratorDependency {
			add(fmt.Sprintf("nodes[%d].depends_on", index), "integrator_missing_input", "integrator nodes must depend on a non-integrator node")
		}
	}
	if !hasIntegratorLeaf {
		add("nodes", "missing_integrator_leaf", "the plan must contain an integrator leaf node")
	}

	if graphValid && len(nodesByKey) == len(plan.Nodes) {
		validatePlanTopology(nodesByKey, dependents, &errs)
	}
	return errs
}

func validatePlanLimits(limits, hardLimits PlanLimits) []ValidationError {
	var errs []ValidationError
	check := func(path string, value, hardLimit int) {
		switch {
		case value < 1:
			errs = append(errs, ValidationError{Path: path, Code: "invalid_limit", Message: path + " must be at least 1"})
		case hardLimit < 1 || value > hardLimit:
			errs = append(errs, ValidationError{Path: path, Code: "limit_exceeded", Message: path + " exceeds the system hard limit"})
		}
	}
	check("limits.max_parallel_runs", limits.MaxParallelRuns, hardLimits.MaxParallelRuns)
	check("limits.max_task_attempts", limits.MaxTaskAttempts, hardLimits.MaxTaskAttempts)
	check("limits.max_rework_cycles", limits.MaxReworkCycles, hardLimits.MaxReworkCycles)
	errs = append(errs, validateBudgetPolicy(limits.Budget)...)
	return errs
}

func validatePlanTopology(nodesByKey map[string]PlanNode, dependents map[string][]string, errs *[]ValidationError) {
	indegree := make(map[string]int, len(nodesByKey))
	depth := make(map[string]int, len(nodesByKey))
	var queue []string
	for key, node := range nodesByKey {
		indegree[key] = len(node.DependsOn)
		if len(node.DependsOn) == 0 {
			queue = append(queue, key)
		}
	}
	sort.Strings(queue)

	processed := 0
	maxDepth := 0
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		processed++
		for _, dependent := range dependents[key] {
			if depth[dependent] < depth[key]+1 {
				depth[dependent] = depth[key] + 1
			}
			if depth[dependent] > maxDepth {
				maxDepth = depth[dependent]
			}
			indegree[dependent]--
			if indegree[dependent] == 0 {
				queue = append(queue, dependent)
				sort.Strings(queue)
			}
		}
	}
	if processed != len(nodesByKey) {
		*errs = append(*errs, ValidationError{
			Path:    "nodes",
			Code:    "dependency_cycle",
			Message: "task dependencies must form an acyclic graph",
		})
		return
	}
	if maxDepth > MaxPlanDependencyDepth {
		*errs = append(*errs, ValidationError{
			Path:    "nodes",
			Code:    "dependency_depth_exceeded",
			Message: fmt.Sprintf("dependency depth may not exceed %d", MaxPlanDependencyDepth),
		})
	}
}

func ReadyNodeKeys(missionStatus MissionStatus, nodes []NodeSnapshot, activeRuns, maxParallelRuns int) ([]string, error) {
	if maxParallelRuns < 1 {
		return nil, fmt.Errorf("max parallel runs must be at least 1")
	}
	if activeRuns < 0 {
		return nil, fmt.Errorf("active runs cannot be negative")
	}

	nodesByKey := make(map[string]NodeSnapshot, len(nodes))
	for _, node := range nodes {
		if strings.TrimSpace(node.Key) == "" {
			return nil, fmt.Errorf("node key is required")
		}
		if _, exists := nodesByKey[node.Key]; exists {
			return nil, fmt.Errorf("duplicate node key %q", node.Key)
		}
		nodesByKey[node.Key] = node
	}
	for _, node := range nodes {
		for _, dependency := range node.DependencyKeys {
			if _, ok := nodesByKey[dependency]; !ok {
				return nil, fmt.Errorf("node %q has unknown dependency %q", node.Key, dependency)
			}
		}
	}

	if missionStatus != MissionStatusRunning || activeRuns >= maxParallelRuns {
		return nil, nil
	}
	candidates := make([]NodeSnapshot, 0, len(nodes))
	for _, node := range nodes {
		if node.Status != TaskStatusPending && node.Status != TaskStatusRework {
			continue
		}
		if node.HasActiveAssignment {
			continue
		}
		ready := true
		for _, dependency := range node.DependencyKeys {
			if nodesByKey[dependency].Status != TaskStatusCompleted {
				ready = false
				break
			}
		}
		if ready {
			candidates = append(candidates, node)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority > candidates[j].Priority
		}
		if candidates[i].CreatedOrder != candidates[j].CreatedOrder {
			return candidates[i].CreatedOrder < candidates[j].CreatedOrder
		}
		return candidates[i].Key < candidates[j].Key
	})

	slots := maxParallelRuns - activeRuns
	if len(candidates) > slots {
		candidates = candidates[:slots]
	}
	keys := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		keys = append(keys, candidate.Key)
	}
	return keys, nil
}
