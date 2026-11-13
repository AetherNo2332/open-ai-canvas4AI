package hostupdate

type DocsBuild struct {
	Status             string `json:"status,omitempty"`
	Digest             string `json:"digest"`
	BuildID            string `json:"buildId"`
	Commit             string `json:"commit"`
	BuiltAt            string `json:"builtAt"`
	DeploymentContract int    `json:"deploymentContract"`
}

type DocsOperation struct {
	ID       string     `json:"id,omitempty"`
	Phase    Phase      `json:"phase"`
	Error    string     `json:"error,omitempty"`
	Source   *DocsBuild `json:"source,omitempty"`
	Target   *DocsBuild `json:"target,omitempty"`
	Switched bool       `json:"switched"`
}

type DocsStatus struct {
	Supported bool          `json:"supported"`
	Current   *DocsBuild    `json:"current,omitempty"`
	Candidate *DocsBuild    `json:"candidate,omitempty"`
	Previous  *DocsBuild    `json:"previous,omitempty"`
	Operation DocsOperation `json:"operation"`
}

type DocsUpdateRequest struct {
	TargetDigest          string `json:"targetDigest"`
	ExpectedCurrentDigest string `json:"expectedCurrentDigest"`
}

type DocsRollbackRequest struct {
	ExpectedCurrentDigest string `json:"expectedCurrentDigest"`
	Reason                string `json:"reason"`
}
