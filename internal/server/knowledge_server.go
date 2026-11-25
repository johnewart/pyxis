package server

import (
	"context"
	"errors"
	"strings"

	knowledgev1 "github.com/johnewart/pyxis/gen/knowledge/v1"
	"github.com/johnewart/pyxis/internal/knowledge/chunk"
	knowledgestore "github.com/johnewart/pyxis/internal/knowledge/store"
	"github.com/johnewart/pyxis/internal/serverutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// KnowledgeServer implements knowledge.v1.KnowledgeService.
type KnowledgeServer struct {
	knowledgev1.UnimplementedKnowledgeServiceServer
	Store *knowledgestore.Store
}

var _ knowledgev1.KnowledgeServiceServer = (*KnowledgeServer)(nil)

func (s *KnowledgeServer) UpsertDocuments(ctx context.Context, req *knowledgev1.UpsertDocumentsRequest) (*knowledgev1.UpsertDocumentsResponse, error) {
	sc, err := serverutil.ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	cs := int(req.GetChunkSizeChars())
	co := int(req.GetChunkOverlapChars())
	cp, err := chunk.Resolve(cs, co)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	var docs []knowledgestore.DocumentIn
	for _, d := range req.GetDocuments() {
		docs = append(docs, knowledgestore.DocumentIn{
			LogicalDocumentID: d.GetLogicalDocumentId(),
			SourceID:          d.GetSourceId(),
			Name:              d.GetName(),
			Content:           d.GetContent(),
			Metadata:          d.GetMetadata(),
		})
	}
	embedCfg := resolveEmbedConfig(req, 8, req.GetEmbeddingModelId())
	n, err := s.Store.UpsertDocuments(ctx, sc, embedCfg, cp, docs, knowledgestore.UpsertOptions{
		DefaultSourceID: req.GetDefaultSourceId(),
		ChunkStrategy:   req.GetChunkStrategy(),
	})
	if err != nil {
		return nil, knowledgeStatusErr(err)
	}
	return &knowledgev1.UpsertDocumentsResponse{ChunksWritten: int32(n)}, nil
}

func (s *KnowledgeServer) DeleteDocuments(ctx context.Context, req *knowledgev1.DeleteDocumentsRequest) (*knowledgev1.DeleteDocumentsResponse, error) {
	sc, err := serverutil.ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	var bulk *string
	if req.DeleteAllForSourceId != nil {
		v := req.GetDeleteAllForSourceId()
		bulk = &v
	}
	var refs []struct{ SourceID, LogicalID string }
	for _, r := range req.GetDocuments() {
		refs = append(refs, struct{ SourceID, LogicalID string }{SourceID: r.GetSourceId(), LogicalID: r.GetLogicalDocumentId()})
	}
	n, err := s.Store.DeleteDocuments(ctx, sc, bulk, req.GetDeleteForFilePath(), req.GetDeleteForFileSourceId(), refs)
	if err != nil {
		return nil, knowledgeStatusErr(err)
	}
	return &knowledgev1.DeleteDocumentsResponse{ChunksDeleted: int32(n)}, nil
}

func (s *KnowledgeServer) Query(ctx context.Context, req *knowledgev1.QueryRequest) (*knowledgev1.QueryResponse, error) {
	sc, err := serverutil.ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	q := strings.TrimSpace(req.GetQuery())
	if q == "" {
		return nil, status.Error(codes.InvalidArgument, "query is required")
	}
	embedCfg := resolveEmbedConfig(req, 8, req.GetEmbeddingModelId())
	hits, err := s.Store.Query(ctx, sc, q, embedCfg, int(req.GetLimit()), req.GetEnableRerank(), knowledgestore.QueryFilter{
		SourceID:    req.GetSourceId(),
		SourceIDs:   req.GetSourceIds(),
		PartitionID: strings.TrimSpace(req.GetPartitionId()),
	})
	if err != nil {
		return nil, knowledgeStatusErr(err)
	}
	var out []*knowledgev1.QueryHit
	for _, h := range hits {
		out = append(out, &knowledgev1.QueryHit{
			ChunkId:           h.ChunkID,
			LogicalDocumentId: h.LogicalDocumentID,
			SourceId:          h.SourceID,
			Name:              h.Name,
			Content:           h.Content,
			Score:             h.Score,
			Metadata:          h.Metadata,
			PartitionId:       h.PartitionID,
		})
	}
	return &knowledgev1.QueryResponse{Hits: out}, nil
}

func (s *KnowledgeServer) CountDocuments(ctx context.Context, req *knowledgev1.CountDocumentsRequest) (*knowledgev1.CountDocumentsResponse, error) {
	sc, err := serverutil.ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	files, chunks, err := s.Store.CountDocuments(ctx, sc, req.GetSourceId())
	if err != nil {
		return nil, knowledgeStatusErr(err)
	}
	return &knowledgev1.CountDocumentsResponse{FileCount: files, ChunkCount: chunks}, nil
}

func knowledgeStatusErr(err error) error {
	var mm *knowledgestore.ModelMismatchError
	if errors.As(err, &mm) {
		st := status.New(codes.FailedPrecondition, mm.Error())
		rem := knowledgev1.KnowledgeRemediation_KNOWLEDGE_REMEDIATION_UNSPECIFIED
		switch mm.Remediation {
		case knowledgestore.RemediationReindex:
			rem = knowledgev1.KnowledgeRemediation_REINDEX_REQUIRED
		case knowledgestore.RemediationNewWorkspace:
			rem = knowledgev1.KnowledgeRemediation_NEW_SCOPE_REQUIRED
		}
		st, derr := st.WithDetails(&knowledgev1.KnowledgeError{
			StoredModelId:      mm.StoredModelID,
			RequestedModelId:   mm.RequestedModelID,
			StoredDimension:    mm.StoredDimension,
			RequestedDimension: mm.RequestedDimension,
			Remediation:        rem,
		})
		if derr != nil {
			return status.Errorf(codes.Internal, "attach knowledge error details: %v", derr)
		}
		return st.Err()
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	return status.Errorf(codes.Internal, "%v", err)
}
