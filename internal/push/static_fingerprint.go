package push

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"Ithiltir-node/internal/metrics"
)

func staticFingerprint(snap *metrics.Static) ([32]byte, bool, error) {
	var zero [32]byte
	if snap == nil {
		return zero, false, nil
	}

	stable := *snap
	stable.Timestamp = time.Time{}

	body, err := json.Marshal(stable)
	if err != nil {
		return zero, false, fmt.Errorf("marshal static fingerprint: %w", err)
	}
	return sha256.Sum256(body), true, nil
}
