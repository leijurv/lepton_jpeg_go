package lepton

// Branch tracks the probability of seeing a 1 or 0 bit in the arithmetic coding
// The counts are stored as a 16-bit value where the top byte is the false count
// and the bottom byte is the true count.
type Branch struct {
	counts uint16
}

// NewBranch creates a new Branch with initial counts of 1 for both true and false
func NewBranch() Branch {
	return Branch{counts: 0x0101}
}

// GetProbability returns the probability of the next bit being false (0-255)
// Calculated as (falseCount * 256) / (falseCount + trueCount)
func (b *Branch) GetProbability() uint8 {
	return probLookup[b.counts]
}

// RecordAndUpdateBit updates the counters when we encounter a 1 or 0
// If we hit 255 values, then we normalize both counts (divide by 2),
// except when the other value is 1, to bias probability for long runs
func (b *Branch) RecordAndUpdateBit(bit bool) {
	// Rotation is used to update either the true or false counter
	// This avoids branching which is faster since bit is unpredictable
	var orig uint16
	if bit {
		orig = (b.counts >> 8) | (b.counts << 8) // rotate left 8
	} else {
		orig = b.counts
	}

	sum := orig + 0x100
	overflow := sum < orig // check for overflow from 0xffxx

	if overflow {
		// Normalize, except in special case where we have 0xff or more same bits in a row
		var mask uint16
		if orig == 0xff01 {
			mask = 0xff00
		} else {
			mask = 0x8100
		}
		// Upper byte is 0 since we incremented 0xffxx so we don't have to mask it
		sum = ((1 + (sum & 0xFF)) >> 1) | mask
	}

	if bit {
		b.counts = (sum >> 8) | (sum << 8) // rotate back
	} else {
		b.counts = sum
	}
}

// GetCounts returns the raw counts value (for testing)
func (b *Branch) GetCounts() uint16 {
	return b.counts
}

// SetCounts sets the raw counts value (for testing)
func (b *Branch) SetCounts(counts uint16) {
	b.counts = counts
}

// probLookup is a precalculated probability lookup table
// For counts value i, probability = (i >> 8) * 256 / ((i >> 8) + (i & 0xff))
var probLookup [65536]uint8

func init() {
	for i := 1; i < 65536; i++ {
		a := i >> 8
		b := i & 0xff
		if a+b > 0 {
			probLookup[i] = uint8((a << 8) / (a + b))
		}
	}
}
