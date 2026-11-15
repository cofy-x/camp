package orchestration

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
}

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
	CreatedAt            string             `json:"createdAt"`
	Deadline             string             `json:"deadline,omitempty"`
	NextCheckAt          string             `json:"nextCheckAt,omitempty"`
	Checks               int                `json:"checks"`
	Reconcile            bool               `json:"reconcile,omitempty"`
}

type GitHubDeliveryResult struct {
	Delivery GitHubDelivery
	// Effect identifies a dispatched write. Receipt stays immutable even when
	// cancellation or a newer Work decision prevents further progression.
	Effect  string
	Outcome string
}

// PullRequestBody is the frozen protocol envelope shared by outbound delivery
// and authenticated ingress echo detection, never an execution authorization.
func (d GitHubDelivery) PullRequestBody() string {
	return "Produced by Camp from an accepted, published Work result.\n\nWork: `" + d.WorkItemID + "`\nSource version: `" + d.ResultCommit + "`\n\n<!-- camp-delivery:" + d.ID + " -->"
}
