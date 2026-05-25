// Copyright IBM Corp. 2026

package hostsession

import "github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"

// OperationMessage is the provider-side operation envelope used by the generic
// engine adapter. The wire protocol itself is handled via JSON-RPC.
type OperationMessage struct {
	ModuleName   string                    `json:"module_name,omitempty"` // preferred runtime module or capability pack name
	ResourceType string                    `json:"resource_type"`         // e.g. "hosts_entry"
	Action       string                    `json:"action"`                // "validate", "read", "create", "update", "delete", "import", "data_read"
	ImportID     string                    `json:"import_id,omitempty"`   // external identifier for import
	Plan         map[string]interface{}    `json:"plan,omitempty"`        // desired state (create/update)
	State        map[string]interface{}    `json:"state,omitempty"`       // current state (update/delete/read)
	Config       map[string]interface{}    `json:"config,omitempty"`      // raw HCL config (validate)
	Execution    *hostrpc.ExecutionContext `json:"execution,omitempty"`
}

// ResultMessage is the provider-side result returned from a JSON-RPC method.
type ResultMessage struct {
	State map[string]interface{} `json:"state,omitempty"`
}
