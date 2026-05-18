package hostsession

type LockMode string

const (
	LockModeShared    LockMode = "shared"
	LockModeExclusive LockMode = "exclusive"
)

type LockDescriptor struct {
	Key    string   `json:"key"`
	Mode   LockMode `json:"mode"`
	Source string   `json:"source,omitempty"`
}
