package json

const (
	ComponentCapabilitiesSchema = "bdag.component-capabilities.v1"
	CorechainComponent          = "corechain"
	CorechainNativeAPI          = "corechain.native"
	CorechainEVMAPI             = "corechain.evm"
	CorechainNativeAPIVersion   = "1.2.0"
	CorechainEVMAPIVersion      = "1.0.0"
	CorechainContractPath       = "api/openrpc.json"
)

// ComponentAPICapability describes a versioned public API surface supported by
// this binary. SupportedMajors is intentionally explicit so clients can make a
// compatibility decision before depending on an endpoint.
type ComponentAPICapability struct {
	Current         string   `json:"current"`
	SupportedMajors []int    `json:"supported_majors"`
	Transports      []string `json:"transports"`
	Contract        string   `json:"contract"`
}

// ComponentCapabilitiesResult is returned by getCapabilities.
type ComponentCapabilitiesResult struct {
	Schema           string                            `json:"schema"`
	Component        string                            `json:"component"`
	ComponentVersion string                            `json:"component_version"`
	SourceCommit     string                            `json:"source_commit,omitempty"`
	APIs             map[string]ComponentAPICapability `json:"apis"`
}
