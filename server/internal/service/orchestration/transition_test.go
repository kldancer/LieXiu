package orchestration

import "testing"

func TestMissionTransitions(t *testing.T) {
	t.Parallel()

	assertTransitions(t, validateMissionTransition, map[MissionStatus][]MissionStatus{
		MissionStatusDraft:   {MissionStatusReady, MissionStatusCancelled},
		MissionStatusReady:   {MissionStatusRunning, MissionStatusCancelled},
		MissionStatusRunning: {MissionStatusCompleted, MissionStatusBlocked, MissionStatusFailed, MissionStatusCancelled},
		MissionStatusBlocked: {MissionStatusRunning, MissionStatusFailed, MissionStatusCancelled},
	})
	assertRejectedTransition(t, validateMissionTransition, MissionStatusCompleted, MissionStatusRunning)
	assertRejectedTransition(t, validateMissionTransition, MissionStatus(""), MissionStatus(""))
}

func TestTaskTransitions(t *testing.T) {
	t.Parallel()

	assertTransitions(t, validateTaskTransition, map[TaskStatus][]TaskStatus{
		TaskStatusPending:  {TaskStatusReady, TaskStatusBlocked, TaskStatusCancelled},
		TaskStatusReady:    {TaskStatusAssigned, TaskStatusBlocked, TaskStatusCancelled},
		TaskStatusAssigned: {TaskStatusRunning, TaskStatusReview, TaskStatusFailed, TaskStatusBlocked, TaskStatusCancelled},
		TaskStatusRunning:  {TaskStatusAssigned, TaskStatusReview, TaskStatusFailed, TaskStatusBlocked, TaskStatusCancelled},
		TaskStatusReview:   {TaskStatusCompleted, TaskStatusRework, TaskStatusFailed, TaskStatusCancelled},
		TaskStatusRework:   {TaskStatusReady, TaskStatusBlocked, TaskStatusCancelled},
		TaskStatusBlocked:  {TaskStatusPending, TaskStatusReady, TaskStatusFailed, TaskStatusCancelled},
	})
	assertRejectedTransition(t, validateTaskTransition, TaskStatusCompleted, TaskStatusReview)
}

func TestAssignmentTransitions(t *testing.T) {
	t.Parallel()

	assertTransitions(t, validateAssignmentTransition, map[AssignmentStatus][]AssignmentStatus{
		AssignmentStatusActive: {AssignmentStatusFulfilled, AssignmentStatusSuperseded, AssignmentStatusRevoked},
	})
	assertRejectedTransition(t, validateAssignmentTransition, AssignmentStatusFulfilled, AssignmentStatusActive)
}

func TestRunTransitionsAcceptTerminalObservationFromAnyActiveState(t *testing.T) {
	t.Parallel()

	assertTransitions(t, validateRunTransition, map[RunStatus][]RunStatus{
		RunStatusQueued:     {RunStatusDispatched, RunStatusRunning, RunStatusSucceeded, RunStatusFailed, RunStatusCancelled},
		RunStatusDispatched: {RunStatusRunning, RunStatusSucceeded, RunStatusFailed, RunStatusCancelled},
		RunStatusRunning:    {RunStatusSucceeded, RunStatusFailed, RunStatusCancelled},
	})
	assertRejectedTransition(t, validateRunTransition, RunStatusSucceeded, RunStatusRunning)
}

type stateValue interface {
	MissionStatus | TaskStatus | AssignmentStatus | RunStatus
}

func assertTransitions[S stateValue](t *testing.T, validate func(S, S) error, allowed map[S][]S) {
	t.Helper()
	for from, targets := range allowed {
		if err := validate(from, from); err != nil {
			t.Errorf("idempotent %q transition rejected: %v", from, err)
		}
		for _, to := range targets {
			if err := validate(from, to); err != nil {
				t.Errorf("transition %q -> %q rejected: %v", from, to, err)
			}
		}
	}
}

func assertRejectedTransition[S stateValue](t *testing.T, validate func(S, S) error, from, to S) {
	t.Helper()
	if err := validate(from, to); err == nil {
		t.Fatalf("transition %q -> %q accepted, want rejection", from, to)
	}
}
