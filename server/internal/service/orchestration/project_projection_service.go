package orchestration

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

const MaxProjectCommandCenterMissions = 100

// GetProjectCommandCenterProjection is a bounded read composition over the
// canonical per-Mission projections. It owns no cross-Mission state or
// scheduling decisions; a failed child read fails the whole snapshot closed.
func (s *Service) GetProjectCommandCenterProjection(
	ctx context.Context,
	workspaceID pgtype.UUID,
	projectID pgtype.UUID,
) (ProjectCommandCenterProjection, error) {
	if s == nil || s.queries == nil || s.repository == nil || !validUUID(workspaceID) || !validUUID(projectID) {
		return ProjectCommandCenterProjection{}, fmt.Errorf("get project command center projection: invalid scope")
	}
	project, err := s.queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{ID: projectID, WorkspaceID: workspaceID})
	if err != nil {
		return ProjectCommandCenterProjection{}, err
	}
	missionIDs, err := s.queries.ListProjectMissionIDs(ctx, db.ListProjectMissionIDsParams{
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		PageSize:    MaxProjectCommandCenterMissions + 1,
	})
	if err != nil {
		return ProjectCommandCenterProjection{}, fmt.Errorf("get project command center projection: list missions: %w", err)
	}
	truncated := len(missionIDs) > MaxProjectCommandCenterMissions
	if truncated {
		missionIDs = missionIDs[:MaxProjectCommandCenterMissions]
	}
	projections := make([]MissionProjection, 0, len(missionIDs))
	for _, missionID := range missionIDs {
		projection, projectionErr := s.GetMissionProjection(ctx, workspaceID, missionID)
		if projectionErr != nil {
			return ProjectCommandCenterProjection{}, fmt.Errorf("get project command center projection: load mission %s: %w", uuidText(missionID), projectionErr)
		}
		projections = append(projections, projection)
	}
	return BuildProjectCommandCenterProjection(project, projections, time.Now().UTC(), truncated), nil
}
