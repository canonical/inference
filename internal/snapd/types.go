package snapd

// snapsEnvelope is the decoded /v2/snaps response envelope. Only the fields
// this client relies on are modeled; snapd's full response contains more.
type snapsEnvelope struct {
	Type   string     `json:"type"`
	Status string     `json:"status"`
	Result []snapInfo `json:"result"`
}

// snapInfo is a single entry in the /v2/snaps result list.
type snapInfo struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}
