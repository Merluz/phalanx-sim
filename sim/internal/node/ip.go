package node

import "fmt"

const maxSequentialIPv4 = 256 * 256 * 254

// SequentialIPv4ByOrdinal maps an ordinal to a deterministic private IPv4
// address in the 10.0.0.0/8 range, skipping host .0 and .255.
func SequentialIPv4ByOrdinal(ordinal uint64) string {
	if ordinal == 0 {
		ordinal = 1
	}
	if ordinal > maxSequentialIPv4 {
		ordinal = maxSequentialIPv4
	}

	slot := ordinal - 1
	oct2 := (slot / (256 * 254)) % 256
	oct3 := (slot / 254) % 256
	oct4 := (slot % 254) + 1
	return fmt.Sprintf("10.%d.%d.%d", oct2, oct3, oct4)
}
