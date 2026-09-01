package providers

type Type string

const TypeInferenceSnap Type = "inference-snap"
const StatusNotInstalled = "not installed"

type Definition struct {
	Name string
	Type Type
}

type Provider struct {
	Name   string `json:"provider"`
	Type   Type   `json:"type"`
	Status string `json:"status"`
}

func (p Provider) Installed() bool {
	return p.Status != StatusNotInstalled
}
