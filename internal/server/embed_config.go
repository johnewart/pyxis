package server

import (
	"strings"

	knowledgestore "github.com/johnewart/pyxis/internal/knowledge/store"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// resolveEmbedConfig reads EmbeddingConfig from unknown proto fields (pre-regen clients)
// or falls back to legacy embedding_model_id.
func resolveEmbedConfig(
	msg proto.Message,
	embeddingField protowire.Number,
	legacyModelID string,
) knowledgestore.EmbedConfig {
	if cfg, ok := parseEmbeddingFromUnknown(msg, embeddingField); ok {
		return cfg
	}
	return knowledgestore.EmbedConfigFromLegacyModel(legacyModelID)
}

func parseEmbeddingFromUnknown(msg proto.Message, fieldNum protowire.Number) (knowledgestore.EmbedConfig, bool) {
	pr := msg.ProtoReflect()
	if emb, ok := embeddingFromKnownField(pr, fieldNum); ok {
		return emb, true
	}
	b := pr.GetUnknown()
	if len(b) == 0 {
		return knowledgestore.EmbedConfig{}, false
	}
	embBytes, ok := extractLengthDelimitedField(b, fieldNum)
	if !ok || len(embBytes) == 0 {
		return knowledgestore.EmbedConfig{}, false
	}
	cfg, ok := parseEmbeddingConfigBytes(embBytes)
	return cfg, ok
}

func embeddingFromKnownField(pr protoreflect.Message, fieldNum protowire.Number) (knowledgestore.EmbedConfig, bool) {
	fd := pr.Descriptor().Fields().ByNumber(protoreflect.FieldNumber(fieldNum))
	if fd == nil || fd.Kind() != protoreflect.MessageKind {
		return knowledgestore.EmbedConfig{}, false
	}
	if !pr.Has(fd) {
		return knowledgestore.EmbedConfig{}, false
	}
	v := pr.Get(fd)
	if !v.Message().IsValid() {
		return knowledgestore.EmbedConfig{}, false
	}
	em := v.Message()
	var providerID, model, indexModelID string
	var dimensions int32
	if f := em.Descriptor().Fields().ByName("provider_id"); f != nil {
		providerID = strings.TrimSpace(em.Get(f).String())
	}
	if f := em.Descriptor().Fields().ByName("model"); f != nil {
		model = strings.TrimSpace(em.Get(f).String())
	}
	if f := em.Descriptor().Fields().ByName("dimensions"); f != nil {
		dimensions = int32(em.Get(f).Int())
	}
	if f := em.Descriptor().Fields().ByName("index_model_id"); f != nil {
		indexModelID = strings.TrimSpace(em.Get(f).String())
	}
	if model == "" && indexModelID == "" {
		return knowledgestore.EmbedConfig{}, false
	}
	return knowledgestore.EmbedConfig{
		ProviderID:   providerID,
		Model:        model,
		Dimensions:   dimensions,
		IndexModelID: indexModelID,
	}, true
}

func extractLengthDelimitedField(b []byte, want protowire.Number) ([]byte, bool) {
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return nil, false
		}
		b = b[n:]
		if protowire.Number(num) == want && typ == protowire.BytesType {
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return nil, false
			}
			return v, true
		}
		n = protowire.ConsumeFieldValue(num, typ, b)
		if n < 0 {
			return nil, false
		}
		b = b[n:]
	}
	return nil, false
}

func parseEmbeddingConfigBytes(b []byte) (knowledgestore.EmbedConfig, bool) {
	var providerID, model, indexModelID string
	var dimensions int32
	found := false
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return knowledgestore.EmbedConfig{}, false
		}
		b = b[n:]
		switch protowire.Number(num) {
		case 1:
			if typ != protowire.BytesType {
				return knowledgestore.EmbedConfig{}, false
			}
			v, n := protowire.ConsumeString(b)
			if n < 0 {
				return knowledgestore.EmbedConfig{}, false
			}
			providerID = strings.TrimSpace(v)
			b = b[n:]
			found = true
		case 2:
			if typ != protowire.BytesType {
				return knowledgestore.EmbedConfig{}, false
			}
			v, n := protowire.ConsumeString(b)
			if n < 0 {
				return knowledgestore.EmbedConfig{}, false
			}
			model = strings.TrimSpace(v)
			b = b[n:]
			found = true
		case 3:
			if typ != protowire.VarintType {
				return knowledgestore.EmbedConfig{}, false
			}
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return knowledgestore.EmbedConfig{}, false
			}
			dimensions = int32(v)
			b = b[n:]
			found = true
		case 4:
			if typ != protowire.BytesType {
				return knowledgestore.EmbedConfig{}, false
			}
			v, n := protowire.ConsumeString(b)
			if n < 0 {
				return knowledgestore.EmbedConfig{}, false
			}
			indexModelID = strings.TrimSpace(v)
			b = b[n:]
			found = true
		default:
			n = protowire.ConsumeFieldValue(num, typ, b)
			if n < 0 {
				return knowledgestore.EmbedConfig{}, false
			}
			b = b[n:]
		}
	}
	if !found {
		return knowledgestore.EmbedConfig{}, false
	}
	return knowledgestore.EmbedConfig{
		ProviderID:   providerID,
		Model:        model,
		Dimensions:   dimensions,
		IndexModelID: indexModelID,
	}, true
}
