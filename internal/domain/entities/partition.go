package entities

import (
	"crypto/sha256"
	"encoding/hex"
)

// PartitionID derives the storage partition for a caller from the Forward
// instance id (SHA-256 of the API host, see service.GenerateInstanceID).
//
// The local caller (stdio mode, CLI subcommands) keeps the bare instance id,
// so 3.0.0 on-disk data stays readable. Every other subject gets its own
// partition, `<instance>-<sha256(subject)[:16]>`, which every instance-scoped
// store (semantic cache keys, memory-graph rows, bloom index directories)
// uses as if it were a separate Forward instance. The hash keeps subjects
// (which may be emails or URLs) out of file names and log lines.
//
// Network scoping is a second dimension: the semantic cache already keys on
// network id, memory-graph records carry the network they came from, and the
// caller's NetworkIDs allow-list is enforced at the tool boundary.
func PartitionID(instanceID, subject string) string {
	if subject == "" || subject == LocalSubject {
		return instanceID
	}
	sum := sha256.Sum256([]byte(subject))
	return instanceID + "-" + hex.EncodeToString(sum[:])[:16]
}
