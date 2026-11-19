package orchestration

import "strconv"

// GitHubRepository is a personal connection. Credentials never enter its JSON.
type GitHubRepository struct {
	ID                   string `json:"id"`
	Revision             int64  `json:"revision"`
	Repository           string `json:"repository"`
	RepositoryID         int64  `json:"repositoryId"`
	DefaultBranch        string `json:"defaultBranch"`
	Private              bool   `json:"private"`
	ActorID              int64  `json:"actorId"`
	CIWorkflow           string `json:"ciWorkflow,omitempty"`
	CIWorkflowID         int64  `json:"ciWorkflowId,omitempty"`
	DeploymentWorkflow   string `json:"deploymentWorkflow,omitempty"`
	DeploymentWorkflowID int64  `json:"deploymentWorkflowId,omitempty"`
	Enabled              bool   `json:"enabled"`
}

type GitHubRepositoryInput struct {
	RequestID          string `json:"requestId"`
	ID                 string `json:"id,omitempty"`
	Revision           int64  `json:"revision,omitempty"`
	Repository         string `json:"repository"`
	Token              string `json:"token,omitempty"`
	CIWorkflow         string `json:"ciWorkflow,omitempty"`
	DeploymentWorkflow string `json:"deploymentWorkflow,omitempty"`
	Enabled            bool   `json:"enabled"`
}

// GitHubDeliverySpec fixes the destination and visible effects at Work creation.
type GitHubDeliverySpec struct {
	ConnectionID       string `json:"connectionId"`
	ConnectionRevision int64  `json:"connectionRevision"`
	BaseBranch         string `json:"baseBranch"`
	Mode               string `json:"mode"`
	Tag                string `json:"tag,omitempty"`
	Prerelease         bool   `json:"prerelease,omitempty"`
	WaitForDeployment  bool   `json:"waitForDeployment,omitempty"`
	MaxRepairs         int    `json:"maxRepairs,omitempty"`
	VersionLine        string `json:"versionLine,omitempty"`
}

type GitHubRepairCause string

const (
	GitHubRepairSourceCI       GitHubRepairCause = "source_ci"
	GitHubRepairApprovedReview GitHubRepairCause = "approved_review"
)

// GitHubDelivery is bounded current state; full originals stay in immutable
// resource versions and append-only delivery receipts.
type GitHubDelivery struct {
	SourceSelection      string             `json:"sourceSelection"`
	Private              bool               `json:"private"`
	ID                   string             `json:"id"`
	WorkItemID           string             `json:"workItemId"`
	RoomID               string             `json:"roomId"`
	Repository           string             `json:"repository"`
	RepositoryID         int64              `json:"repositoryId"`
	Spec                 GitHubDeliverySpec `json:"spec"`
	Stage                string             `json:"stage"`
	Status               string             `json:"status"`
	Revision             int64              `json:"revision"`
	Reason               string             `json:"reason,omitempty"`
	ResourceID           string             `json:"resourceId"`
	BaselineCommit       string             `json:"baselineCommit"`
	ResultCommit         string             `json:"resultCommit,omitempty"`
	BaseSHA              string             `json:"baseSha,omitempty"`
	HeadSHA              string             `json:"headSha,omitempty"`
	ActorID              int64              `json:"actorId"`
	CIWorkflowID         int64              `json:"ciWorkflowId,omitempty"`
	DeploymentWorkflowID int64              `json:"deploymentWorkflowId,omitempty"`
	Branch               string             `json:"branch"`
	PullRequest          int64              `json:"pullRequest,omitempty"`
	PullRequestURL       string             `json:"pullRequestUrl,omitempty"`
	MergeSHA             string             `json:"mergeSha,omitempty"`
	ReleaseURL           string             `json:"releaseUrl,omitempty"`
	DeploymentURL        string             `json:"deploymentUrl,omitempty"`
	Title                string             `json:"title"`
	IssueNumber          int64              `json:"issueNumber,omitempty"`
	CreatedAt            string             `json:"createdAt"`
	Deadline             string             `json:"deadline,omitempty"`
	NextCheckAt          string             `json:"nextCheckAt,omitempty"`
	Checks               int                `json:"checks"`
	Reconcile            bool               `json:"reconcile,omitempty"`
	FailureCode          string             `json:"failureCode,omitempty"`
	CheckURL             string             `json:"checkUrl,omitempty"`
	RepairCause          GitHubRepairCause  `json:"repairCause,omitempty"`
	Diagnostics          string             `json:"diagnostics,omitempty"`
	ReadFailures         int                `json:"readFailures,omitempty"`
	Repairs              int                `json:"repairs"`
	RepairTaskID         string             `json:"repairTaskId,omitempty"`
	InitialTaskID        string             `json:"initialTaskId,omitempty"`
	ParticipantAgentIDs  []string           `json:"participantAgentIds,omitempty"`
	FirstAgentID         string             `json:"firstAgentId,omitempty"`
	InitialResultCommit  string             `json:"initialResultCommit,omitempty"`
	PullRequestEnvelope  string             `json:"pullRequestEnvelope,omitempty"`
	PreviousHeadSHA      string             `json:"previousHeadSha,omitempty"`
	ProjectRevision      int64              `json:"projectRevision,omitempty"`
	PolicyRevision       int64              `json:"policyRevision,omitempty"`
	ReviewerIDs          []int64            `json:"reviewerIds,omitempty"`
}

type GitHubDeliveryResult struct {
	Delivery GitHubDelivery
	// Effect identifies a dispatched write. Receipt stays immutable even when
	// cancellation or a newer Work decision prevents further progression.
	Effect  string
	Outcome string
}

// HasRepairEvidence also accepts previously recorded approved reviews. Older
// generic CI failures need a fresh observation under the current classifier.
func (d GitHubDelivery) HasRepairEvidence() bool {
	return d.Diagnostics != "" && (d.RepairCause == GitHubRepairSourceCI || d.RepairCause == GitHubRepairApprovedReview || d.FailureCode == "review_changes")
}

// VisibleWriteStage identifies steps whose interrupted claims need a receipt
// check before any further write. Preparation creates only immutable objects.
func (d GitHubDelivery) VisibleWriteStage() bool {
	switch d.Stage {
	case "branch", "update_branch", "pull_request", "merge", "release":
		return true
	default:
		return false
	}
}

func (d GitHubDelivery) WorkflowObservationStage() bool {
	switch d.Stage {
	case "checks", "review", "deployment":
		return true
	default:
		return false
	}
}

// PullRequestBody is the frozen protocol envelope shared by outbound delivery
// and authenticated ingress echo detection, never an execution authorization.
func (d GitHubDelivery) PullRequestBody() string {
	if d.PullRequestEnvelope != "" {
		return d.PullRequestEnvelope
	}
	body := "Produced by Camp from an accepted, published Work result."
	if d.IssueNumber > 0 {
		body += "\n\nRefs #" + strconv.FormatInt(d.IssueNumber, 10)
	}
	label, version := "Source version", d.ResultCommit
	if d.InitialResultCommit != "" {
		label, version = "Initial source version", d.InitialResultCommit
	}
	return body + "\n\nWork: `" + d.WorkItemID + "`\n" + label + ": `" + version + "`\n\n<!-- camp-delivery:" + d.ID + " -->"
}

// CommitTitle uses a generated subject for issue deliveries because GitHub
// interprets closing keywords in commit messages.
func (d GitHubDelivery) CommitTitle() string {
	if d.IssueNumber > 0 {
		return "Deliver work for issue #" + strconv.FormatInt(d.IssueNumber, 10)
	}
	return d.Title
}
