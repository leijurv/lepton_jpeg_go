package lepton

import (
	"io"
)

const (
	bitsInByte               = 8
	bitsInValue              = 64
	bitsInValueMinusLastByte = bitsInValue - bitsInByte
	valueMask                = (1 << bitsInValueMinusLastByte) - 1
)

// VPXBoolReader implements VP8 CABAC arithmetic decoding
type VPXBoolReader struct {
	value          uint64
	rang           uint64 // 128 << bitsInValueMinusLastByte <= range <= 255 << bitsInValueMinusLastByte
	upstreamReader io.Reader
}

// NewVPXBoolReader creates a new VPXBoolReader
func NewVPXBoolReader(reader io.Reader) (*VPXBoolReader, error) {
	r := &VPXBoolReader{
		upstreamReader: reader,
		value:          1 << (bitsInValue - 1), // guard bit
		rang:           255 << bitsInValueMinusLastByte,
	}

	// Read the marker false bit
	var dummyBranch Branch = NewBranch()
	bit, err := r.GetBit(&dummyBranch)
	if err != nil {
		return nil, err
	}
	if bit {
		return nil, ErrStreamInconsistent
	}

	return r, nil
}

// get performs a single bit read with the given branch
func (r *VPXBoolReader) get(branch *Branch, tmpValue *uint64, tmpRange *uint64) bool {
	probability := uint64(branch.GetProbability())

	split := mulProb(*tmpRange, probability)

	bit := *tmpValue >= split

	branch.RecordAndUpdateBit(bit)

	if bit {
		*tmpRange -= split
		*tmpValue -= split
	} else {
		*tmpRange = split
	}

	shift := leadingZeros64(*tmpRange)
	*tmpValue <<= shift
	*tmpRange <<= shift

	return bit
}

// GetBit reads a single bit using the given branch for probability
func (r *VPXBoolReader) GetBit(branch *Branch) (bool, error) {
	tmpValue := r.value
	tmpRange := r.rang

	// Ensure we have at least 8 stream bits
	if tmpValue&valueMask == 0 {
		var err error
		tmpValue, err = r.vpxReaderFill(tmpValue)
		if err != nil {
			return false, err
		}
	}

	bit := r.get(branch, &tmpValue, &tmpRange)

	r.value = tmpValue
	r.rang = tmpRange

	return bit, nil
}

// GetGrid reads a value encoded using a grid (binary tree) of branches
// A is the size of the grid (must be power of 2)
func (r *VPXBoolReader) GetGrid(branches []Branch) (int, error) {
	if len(branches) == 0 || (len(branches)&(len(branches)-1)) != 0 {
		panic("branches length must be power of 2")
	}

	A := len(branches)
	tmpValue := r.value
	tmpRange := r.rang

	var err error
	tmpValue, err = r.vpxReaderFill(tmpValue)
	if err != nil {
		return 0, err
	}

	decodedSoFar := 1
	numBits := bitLength(A) - 1

	for i := 0; i < numBits; i++ {
		curBit := r.get(&branches[decodedSoFar], &tmpValue, &tmpRange)
		decodedSoFar <<= 1
		if curBit {
			decodedSoFar |= 1
		}
	}

	// Remove set leading bit
	value := decodedSoFar ^ A

	r.value = tmpValue
	r.rang = tmpRange

	return value, nil
}

// GetUnaryEncoded reads a unary encoded value (count of 1s before first 0)
func (r *VPXBoolReader) GetUnaryEncoded(branches []Branch) (int, error) {
	A := len(branches)
	tmpValue := r.value
	tmpRange := r.rang

	for value := 0; value < A; value++ {
		// Refill every 7 iterations
		if value == 0 || value == 7 {
			var err error
			tmpValue, err = r.vpxReaderFill(tmpValue)
			if err != nil {
				return 0, err
			}
		}

		split := mulProb(tmpRange, uint64(branches[value].GetProbability()))

		if tmpValue >= split {
			branches[value].RecordAndUpdateBit(true)

			tmpRange -= split
			tmpValue -= split

			shift := leadingZeros64(tmpRange)
			tmpValue <<= shift
			tmpRange <<= shift
		} else {
			branches[value].RecordAndUpdateBit(false)

			tmpRange = split

			shift := leadingZeros64(tmpRange)
			tmpValue <<= shift
			tmpRange <<= shift

			r.value = tmpValue
			r.rang = tmpRange

			return value, nil
		}
	}

	r.value = tmpValue
	r.rang = tmpRange

	return A, nil
}

// GetNBits reads n bits using the given branches
func (r *VPXBoolReader) GetNBits(n int, branches []Branch) (int, error) {
	if n > len(branches) {
		panic("n exceeds branches length")
	}

	tmpValue := r.value
	tmpRange := r.rang

	coef := 0
	for i := n - 1; i >= 0; i-- {
		if tmpValue&valueMask == 0 {
			var err error
			tmpValue, err = r.vpxReaderFill(tmpValue)
			if err != nil {
				return 0, err
			}
		}

		bit := r.get(&branches[i], &tmpValue, &tmpRange)
		if bit {
			coef |= 1 << i
		}
	}

	r.value = tmpValue
	r.rang = tmpRange

	return coef, nil
}

// vpxReaderFill fills the value register with more bits from the stream
func (r *VPXBoolReader) vpxReaderFill(tmpValue uint64) (uint64, error) {
	if tmpValue&0xFF == 0 {
		shift := int32(trailingZeros64(tmpValue))
		// Unset the last guard bit and set a new one
		tmpValue &= tmpValue - 1
		tmpValue |= 1 << (shift & 7)

		var v [1]byte
		shift -= 7

		for shift > 0 {
			n, err := r.upstreamReader.Read(v[:])
			if err != nil && err != io.EOF {
				return 0, err
			}
			if n == 0 {
				break
			}

			tmpValue |= uint64(v[0]) << shift
			shift -= 8
		}
	}

	return tmpValue, nil
}

// mulProb calculates the split point for arithmetic coding
func mulProb(tmpRange, probability uint64) uint64 {
	return ((((tmpRange - (1 << bitsInValueMinusLastByte)) >> 8) * probability) &
		(0xFF << bitsInValueMinusLastByte)) + (1 << bitsInValueMinusLastByte)
}

// leadingZeros64 returns the number of leading zero bits in x
func leadingZeros64(x uint64) uint32 {
	if x == 0 {
		return 64
	}
	n := uint32(0)
	if x&0xFFFFFFFF00000000 == 0 {
		n += 32
		x <<= 32
	}
	if x&0xFFFF000000000000 == 0 {
		n += 16
		x <<= 16
	}
	if x&0xFF00000000000000 == 0 {
		n += 8
		x <<= 8
	}
	if x&0xF000000000000000 == 0 {
		n += 4
		x <<= 4
	}
	if x&0xC000000000000000 == 0 {
		n += 2
		x <<= 2
	}
	if x&0x8000000000000000 == 0 {
		n += 1
	}
	return n
}

// trailingZeros64 returns the number of trailing zero bits in x
func trailingZeros64(x uint64) int {
	if x == 0 {
		return 64
	}
	n := 0
	if x&0xFFFFFFFF == 0 {
		n += 32
		x >>= 32
	}
	if x&0xFFFF == 0 {
		n += 16
		x >>= 16
	}
	if x&0xFF == 0 {
		n += 8
		x >>= 8
	}
	if x&0xF == 0 {
		n += 4
		x >>= 4
	}
	if x&0x3 == 0 {
		n += 2
		x >>= 2
	}
	if x&0x1 == 0 {
		n += 1
	}
	return n
}

// bitLength returns the number of bits needed to represent n
func bitLength(n int) int {
	if n == 0 {
		return 0
	}
	bits := 0
	for n > 0 {
		bits++
		n >>= 1
	}
	return bits
}
