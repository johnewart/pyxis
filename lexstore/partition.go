package lexstore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cockroachdb/pebble"
)

// OverlayRecord describes a derived partition and the partition it reads
// through.
type OverlayRecord struct {
	PartitionID     string
	BasePartitionID string
	BaseCommit      string
	TargetCommit    string
}

type overlayRec struct {
	OverlayRecord
	LastUsed int64 // unix seconds
}

func overlayKey(space, src, part string) string { return pOverlay + join(space, src, part) }

func (s *Store) readOverlayRec(space, src, part string) (*overlayRec, error) {
	raw, ok, err := s.get(overlayKey(space, src, part))
	if err != nil || !ok {
		return nil, err
	}
	var r overlayRec
	if err := decode(raw, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) writeOverlayRec(space, src, part string, r *overlayRec) error {
	enc, err := encode(r)
	if err != nil {
		return err
	}
	return s.kv.Set([]byte(overlayKey(space, src, part)), enc, pebble.Sync)
}

// DeclareOverlay registers rec.PartitionID as a derived partition of one
// source, reading through rec.BasePartitionID.
func (s *Store) DeclareOverlay(ctx context.Context, space, sourceID string, rec OverlayRecord) error {
	if err := requireSpace(space); err != nil {
		return err
	}
	partitionID := normalizePartitionID(rec.PartitionID)
	if partitionID == "" {
		return fmt.Errorf("partition_id is required (the live partition is not an overlay)")
	}
	baseID := normalizePartitionID(rec.BasePartitionID)
	if baseID == partitionID {
		return fmt.Errorf("overlay %q would read through itself", partitionID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeOverlayRec(space, NormalizeSourceID(sourceID), partitionID, &overlayRec{
		OverlayRecord: OverlayRecord{PartitionID: partitionID, BasePartitionID: baseID,
			BaseCommit: strings.TrimSpace(rec.BaseCommit), TargetCommit: strings.TrimSpace(rec.TargetCommit)},
		LastUsed: time.Now().Unix(),
	})
}

// ReadOverlay returns partitionID's overlay record, or nil when it is live
// or unknown.
func (s *Store) ReadOverlay(ctx context.Context, space, sourceID, partitionID string) (*OverlayRecord, error) {
	partitionID = normalizePartitionID(partitionID)
	if partitionID == "" {
		return nil, nil
	}
	r, err := s.readOverlayRec(space, NormalizeSourceID(sourceID), partitionID)
	if err != nil || r == nil {
		return nil, err
	}
	out := r.OverlayRecord
	out.PartitionID = partitionID
	return &out, nil
}

// RecordOwnedPaths records the files a derived partition decided for itself
// (re-indexed or deleted): they hide the base's copy even with no chunks.
func (s *Store) RecordOwnedPaths(ctx context.Context, space, sourceID, partitionID string, paths []string) error {
	partitionID = normalizePartitionID(partitionID)
	if partitionID == "" {
		return fmt.Errorf("partition_id is required")
	}
	if len(paths) == 0 {
		return nil
	}
	sourceID = NormalizeSourceID(sourceID)
	b := s.kv.NewBatch()
	defer b.Close()
	for _, p := range paths {
		if p = normalizeFilePath(p); p != "" {
			_ = b.Set([]byte(pOwned+join(space, sourceID, partitionID, p)), nil, nil)
		}
	}
	return b.Commit(pebble.Sync)
}

// OwnedPaths returns a derived partition's analysed set.
func (s *Store) OwnedPaths(ctx context.Context, space, sourceID, partitionID string) (map[string]struct{}, error) {
	return s.ownedPaths(space, NormalizeSourceID(sourceID), normalizePartitionID(partitionID))
}

func (s *Store) ownedPaths(space, src, part string) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	if part == "" {
		return out, nil
	}
	err := s.scan(pOwned+join(space, src, part)+"\x00", func(k, _ []byte) error {
		out[string(k)] = struct{}{}
		return nil
	})
	return out, err
}

// TouchPartition stamps a derived partition's last use.
func (s *Store) TouchPartition(ctx context.Context, space, sourceID, partitionID string) error {
	partitionID = normalizePartitionID(partitionID)
	if partitionID == "" {
		return nil
	}
	sourceID = NormalizeSourceID(sourceID)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.readOverlayRec(space, sourceID, partitionID)
	if err != nil || r == nil {
		return err
	}
	r.LastUsed = time.Now().Unix()
	return s.writeOverlayRec(space, sourceID, partitionID, r)
}

// DropPartition deletes a derived partition's chunks, analysed set and
// overlay record. It refuses the live partition.
func (s *Store) DropPartition(ctx context.Context, space, sourceID, partitionID string) (_ int, err error) {
	defer s.observeWrite("drop_partition", time.Now(), &err)
	partitionID = normalizePartitionID(partitionID)
	if partitionID == "" {
		return 0, fmt.Errorf("refusing to drop the live partition")
	}
	sourceID = NormalizeSourceID(sourceID)
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	if err := s.scan(pFile+join(space, partitionID, sourceID)+"\x00", func(k, _ []byte) error {
		rest := string(k)
		ids = append(ids, rest[strings.LastIndex(rest, "\x00")+1:])
		return nil
	}); err != nil {
		return 0, err
	}
	w := s.newBatch()
	for _, id := range ids {
		if err := w.deleteChunk(id); err != nil {
			w.abort()
			return 0, err
		}
	}
	op := pOwned + join(space, sourceID, partitionID) + "\x00"
	_ = w.b.DeleteRange([]byte(op), upper(op), nil)
	_ = w.b.Delete([]byte(overlayKey(space, sourceID, partitionID)), nil)
	return len(ids), w.commit()
}

type overlayCtx struct {
	baseID string
	owned  map[string]struct{}
}

func (s *Store) overlayContexts(space, partitionID string) (map[string]*overlayCtx, error) {
	if partitionID == "" {
		return nil, nil
	}
	out := map[string]*overlayCtx{}
	err := s.scan(pOverlay+space+"\x00", func(k, v []byte) error {
		f := strings.SplitN(string(k), "\x00", 2)
		if len(f) != 2 || f[1] != partitionID {
			return nil
		}
		var r overlayRec
		if decode(v, &r) == nil {
			out[f[0]] = &overlayCtx{baseID: normalizePartitionID(r.BasePartitionID)}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for src, oc := range out {
		if oc.owned, err = s.ownedPaths(space, src, partitionID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// filterByPartition applies overlay precedence: the child's chunks are the
// whole truth for the files it analysed, the base answers for everything
// else, and a live query sees only live chunks.
func (s *Store) filterByPartition(cand []SearchHit, partitionID string, overlays map[string]*overlayCtx) []SearchHit {
	out := cand[:0]
	for _, h := range cand {
		hp := normalizePartitionID(h.PartitionID)
		if partitionID == "" {
			if hp == "" {
				out = append(out, h)
			}
			continue
		}
		if hp == partitionID {
			out = append(out, h)
			continue
		}
		oc := overlays[NormalizeSourceID(h.SourceID)]
		if oc == nil || hp != oc.baseID {
			continue
		}
		if _, owned := oc.owned[s.opts.FileOf(h.LogicalDocumentID)]; owned {
			continue
		}
		out = append(out, h)
	}
	return out
}

// ListOverlayPartitions returns the derived partitions declared for one
// source, sorted.
func (s *Store) ListOverlayPartitions(ctx context.Context, space, sourceID string) ([]string, error) {
	if err := requireSpace(space); err != nil {
		return nil, err
	}
	var out []string
	err := s.scan(pOverlay+join(space, NormalizeSourceID(sourceID))+"\x00", func(k, _ []byte) error {
		if id := string(k); id != "" {
			out = append(out, id)
		}
		return nil
	})
	return out, err
}

// IdlePartition is one reap candidate.
type IdlePartition struct {
	Space       string
	SourceID    string
	PartitionID string
}

// ListIdlePartitions returns derived partitions unused for idleHours.
func (s *Store) ListIdlePartitions(ctx context.Context, idleHours int) ([]IdlePartition, error) {
	if idleHours <= 0 {
		return nil, nil
	}
	cutoff := time.Now().Add(-time.Duration(idleHours) * time.Hour).Unix()
	var out []IdlePartition
	err := s.scan(pOverlay, func(k, v []byte) error {
		f := strings.SplitN(string(k), "\x00", 3)
		if len(f) != 3 {
			return nil
		}
		var r overlayRec
		if decode(v, &r) == nil && r.LastUsed <= cutoff {
			out = append(out, IdlePartition{Space: f[0], SourceID: f[1], PartitionID: f[2]})
		}
		return nil
	})
	return out, err
}
