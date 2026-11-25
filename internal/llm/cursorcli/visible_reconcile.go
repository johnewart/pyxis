package cursorclillm

import "strings"

// Cursor CLI emits the same visible assistant message on two redundant sub-streams:
// `type:text` (incremental deltas) and `type:assistant` (cumulative snapshots). Ordering
// between them is not reliable and the snapshot may differ from the concatenated deltas by
// minor formatting drift (e.g. a trailing period the snapshot adds). Treating either source
// as authoritative on its own — or delta-stripping each against its own baseline — lets the
// two reconstructions double-emit overlapping text. The helpers below collapse both into one
// canonical reconstruction so the visible pipeline is fed each raw byte exactly once.

// canonicalVisible merges the running `type:text` reconstruction and the latest
// `type:assistant` cumulative snapshot into the most complete reconstruction of the same
// visible message.
func canonicalVisible(textRaw, asstRaw string) string {
	switch {
	case textRaw == "":
		return asstRaw
	case asstRaw == "":
		return textRaw
	case strings.HasPrefix(asstRaw, textRaw):
		return asstRaw
	case strings.HasPrefix(textRaw, asstRaw):
		return textRaw
	default:
		// Divergence after a shared prefix (formatting drift). Prefer the longer
		// reconstruction; reconcileVisibleSuffix handles the byte-level overlap so no
		// already-fed text is repeated.
		if len(asstRaw) >= len(textRaw) {
			return asstRaw
		}
		return textRaw
	}
}

// maxOverlapLen returns the largest k (k <= min(len(a), len(b))) such that the last k bytes
// of a equal the first k bytes of b.
func maxOverlapLen(a, b string) int {
	max := len(a)
	if len(b) < max {
		max = len(b)
	}
	for k := max; k > 0; k-- {
		if a[len(a)-k:] == b[:k] {
			return k
		}
	}
	return 0
}

// reconcileVisibleSuffix returns the raw text not yet fed to the visible pipeline given the
// already-fed transcript and the new canonical reconstruction, plus the transcript to record
// as fed. It only ever advances (never rewinds), so the pipeline sees a single monotonic
// stream. When canonical extends fed, the suffix is the simple tail; on rare divergence it is
// the portion of canonical beyond its byte-overlap with fed.
func reconcileVisibleSuffix(fed, canonical string) (suffix, newFed string) {
	if len(canonical) <= len(fed) {
		return "", fed
	}
	if strings.HasPrefix(canonical, fed) {
		return canonical[len(fed):], canonical
	}
	k := maxOverlapLen(fed, canonical)
	suffix = canonical[k:]
	return suffix, fed + suffix
}
