package orchestration

import "fmt"

type transitionError struct {
	entity string
	from   string
	to     string
}

func (e transitionError) Error() string {
	return fmt.Sprintf("invalid %s transition %q -> %q", e.entity, e.from, e.to)
}

func validateMissionTransition(from, to MissionStatus) error {
	return validateTransition("mission", string(from), string(to), map[MissionStatus]map[MissionStatus]struct{}{
		MissionStatusDraft: {
			MissionStatusReady:     {},
			MissionStatusCancelled: {},
		},
		MissionStatusReady: {
			MissionStatusRunning:   {},
			MissionStatusCancelled: {},
		},
		MissionStatusRunning: {
			MissionStatusCompleted: {},
			MissionStatusBlocked:   {},
			MissionStatusFailed:    {},
			MissionStatusCancelled: {},
		},
		MissionStatusBlocked: {
			MissionStatusRunning:   {},
			MissionStatusFailed:    {},
			MissionStatusCancelled: {},
		},
		MissionStatusCompleted: {},
		MissionStatusFailed:    {},
		MissionStatusCancelled: {},
	}, from, to)
}

func validateTaskTransition(from, to TaskStatus) error {
	return validateTransition("task node", string(from), string(to), map[TaskStatus]map[TaskStatus]struct{}{
		TaskStatusPending: {
			TaskStatusReady:     {},
			TaskStatusBlocked:   {},
			TaskStatusCancelled: {},
		},
		TaskStatusReady: {
			TaskStatusAssigned:  {},
			TaskStatusBlocked:   {},
			TaskStatusCancelled: {},
		},
		TaskStatusAssigned: {
			TaskStatusRunning:   {},
			TaskStatusReview:    {},
			TaskStatusFailed:    {},
			TaskStatusBlocked:   {},
			TaskStatusCancelled: {},
		},
		TaskStatusRunning: {
			TaskStatusAssigned:  {},
			TaskStatusReview:    {},
			TaskStatusFailed:    {},
			TaskStatusBlocked:   {},
			TaskStatusCancelled: {},
		},
		TaskStatusReview: {
			TaskStatusCompleted: {},
			TaskStatusRework:    {},
			TaskStatusFailed:    {},
			TaskStatusCancelled: {},
		},
		TaskStatusRework: {
			TaskStatusReady:     {},
			TaskStatusBlocked:   {},
			TaskStatusCancelled: {},
		},
		TaskStatusBlocked: {
			TaskStatusPending:   {},
			TaskStatusReady:     {},
			TaskStatusFailed:    {},
			TaskStatusCancelled: {},
		},
		TaskStatusCompleted: {},
		TaskStatusFailed:    {},
		TaskStatusCancelled: {},
	}, from, to)
}

func validateAssignmentTransition(from, to AssignmentStatus) error {
	return validateTransition("assignment", string(from), string(to), map[AssignmentStatus]map[AssignmentStatus]struct{}{
		AssignmentStatusActive: {
			AssignmentStatusFulfilled:  {},
			AssignmentStatusSuperseded: {},
			AssignmentStatusRevoked:    {},
		},
		AssignmentStatusFulfilled:  {},
		AssignmentStatusSuperseded: {},
		AssignmentStatusRevoked:    {},
	}, from, to)
}

func validateRunTransition(from, to RunStatus) error {
	return validateTransition("run", string(from), string(to), map[RunStatus]map[RunStatus]struct{}{
		RunStatusQueued: {
			RunStatusDispatched: {},
			RunStatusRunning:    {},
			RunStatusSucceeded:  {},
			RunStatusFailed:     {},
			RunStatusCancelled:  {},
		},
		RunStatusDispatched: {
			RunStatusRunning:   {},
			RunStatusSucceeded: {},
			RunStatusFailed:    {},
			RunStatusCancelled: {},
		},
		RunStatusRunning: {
			RunStatusSucceeded: {},
			RunStatusFailed:    {},
			RunStatusCancelled: {},
		},
		RunStatusSucceeded: {},
		RunStatusFailed:    {},
		RunStatusCancelled: {},
	}, from, to)
}

func validateTransition[S ~string](entity, fromText, toText string, allowed map[S]map[S]struct{}, from, to S) error {
	targets, fromKnown := allowed[from]
	_, toKnown := allowed[to]
	if !fromKnown || !toKnown {
		return transitionError{entity: entity, from: fromText, to: toText}
	}
	if from == to {
		return nil
	}
	if _, ok := targets[to]; ok {
		return nil
	}
	return transitionError{entity: entity, from: fromText, to: toText}
}
