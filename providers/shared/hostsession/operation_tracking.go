package hostsession

type OperationMetadata struct {
	ModuleName   string
	ResourceType string
	Action       string
	Name         string
}

type operationStatus string

const (
	operationStatusCompleted operationStatus = "completed"
	operationStatusFailed    operationStatus = "failed"
)

type operationLease struct {
	complete func(lastErr error) error
}

func (l *operationLease) Complete(lastErr error) error {
	if l == nil || l.complete == nil {
		return nil
	}
	return l.complete(lastErr)
}
