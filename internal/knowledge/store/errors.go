package knowledgestore

import "fmt"

// Remediation hints how clients should recover from a model mismatch.
type Remediation int

const (
	RemediationUnspecified Remediation = iota
	RemediationReindex
	RemediationNewWorkspace
)

// ModelMismatchError is returned when the client requests a different embedding model or dimension
// than the one recorded in scope_knowledge_meta for this (tenant, scope, source) partition.
type ModelMismatchError struct {
	StoredModelID      string
	RequestedModelID   string
	StoredDimension    int32
	RequestedDimension int32
	Remediation        Remediation
}

func (e *ModelMismatchError) Error() string {
	return fmt.Sprintf(
		"embedding model mismatch: stored %s (dim %d), requested %s (dim %d); reindex or use a new knowledge database",
		e.StoredModelID, e.StoredDimension, e.RequestedModelID, e.RequestedDimension,
	)
}
