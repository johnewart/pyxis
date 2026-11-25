package knowledgestore

import "github.com/johnewart/pyxis/lexstore"

// UpsertOptions carries request-level ingest parameters (proto fields 6–7).
type UpsertOptions struct {
	// DefaultSourceID applies when a document omits source_id.
	DefaultSourceID string
	// ChunkStrategy "files" or "ast" stores each document as one chunk (the
	// caller chunked it); anything else splits by the request's chunk size.
	ChunkStrategy string
	// InsertOnly skips per-document deletes (the caller cleared them).
	InsertOnly bool
	// PartitionID writes into a derived partition ("" = live).
	PartitionID string
}

// QueryFilter scopes a query to sources and a partition.
type QueryFilter lexstore.QueryFilter

// ResolvedSourceIDs returns the effective source_id list for a query.
func (f QueryFilter) ResolvedSourceIDs() []string { return lexstore.QueryFilter(f).ResolvedSourceIDs() }
