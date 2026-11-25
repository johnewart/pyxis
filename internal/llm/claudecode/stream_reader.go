package claudecodeclillm

import (
	"bufio"
	"bytes"
	"fmt"
)

// stream-json lines can include large cumulative assistant snapshots; avoid bufio.Scanner's token cap.
const streamJSONLineMaxBytes = 32 << 20 // 32 MiB

func readStreamJSONLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	if len(line) > streamJSONLineMaxBytes {
		return nil, fmt.Errorf("stream-json line exceeds %d bytes", streamJSONLineMaxBytes)
	}
	return bytes.TrimSpace(line), nil
}

func drainStreamJSONLines(r *bufio.Reader) {
	for {
		if _, err := readStreamJSONLine(r); err != nil {
			return
		}
	}
}
