package model

import "time"

type CrewMemberRole string
const (
    CrewMemberRoleCoordinator CrewMemberRole = "coordinator"
    CrewMemberRoleMember CrewMemberRole = "member"
)

type CrewMemberPermission string
const (
    CrewPermissionReadOnly CrewMemberPermission = "read_only"
    CrewPermissionPropose CrewMemberPermission = "propose"
    CrewPermissionWrite CrewMemberPermission = "write"
)

const (
    CrewEventRunCreated = "crew_run_created"
    CrewEventMemberRunStarted = "member_run_started"
    CrewEventMemberMessage = "member_message"
    CrewEventMemberRunWaiting = "member_run_waiting"
    CrewEventMemberRunCompleted = "member_run_completed"
    CrewEventMemberRunFailed = "member_run_failed"
    CrewEventApprovalRequired = "crew_approval_required"
    CrewEventCrewCompleted = "crew_run_completed"
)

type CrewRunStatus string
const (
    CrewRunQueued CrewRunStatus = "queued"
    CrewRunRunning CrewRunStatus = "running"
    CrewRunWaitingMember CrewRunStatus = "waiting_member"
    CrewRunWaitingApproval CrewRunStatus = "waiting_approval"
    CrewRunCompleted CrewRunStatus = "completed"
    CrewRunFailed CrewRunStatus = "failed"
    CrewRunCancelled CrewRunStatus = "cancelled"
)

type MemberRunStatus string
const (
    MemberRunQueued MemberRunStatus = "queued"
    MemberRunRunning MemberRunStatus = "running"
    MemberRunWaiting MemberRunStatus = "waiting"
    MemberRunCompleted MemberRunStatus = "completed"
    MemberRunFailed MemberRunStatus = "failed"
    MemberRunCancelled MemberRunStatus = "cancelled"
)

type CrewBusinessEvent struct {
    Type string `json:"type"`
    CrewRunID string `json:"crewRunId"`
    MemberRunID string `json:"memberRunId,omitempty"`
    Sequence int64 `json:"sequence"`
    CreatedAt time.Time `json:"createdAt"`
    Payload any `json:"payload,omitempty"`
}

type CrewMemberMessagePayload struct {
    TaskID string `json:"taskId"`
    Summary string `json:"summary"`
    ArtifactIDs []string `json:"artifactIds,omitempty"`
    ErrorCode string `json:"errorCode,omitempty"`
}
