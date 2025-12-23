package lepton

import (
	"io"
)

// VPXBoolWriter implements VP8 CABAC arithmetic encoding
type VPXBoolWriter struct {
	lowValue uint64
	rang     uint32
	buffer   []byte
	writer   io.Writer
}

// NewVPXBoolWriter creates a new VPXBoolWriter
func NewVPXBoolWriter(writer io.Writer) (*VPXBoolWriter, error) {
	w := &VPXBoolWriter{
		lowValue: 1 << 9, // marker bit to track stream bits
		rang:     255,
		buffer:   make([]byte, 0, 4096),
		writer:   writer,
	}

	// Write initial false bit to prevent carry overflow
	var dummyBranch Branch = NewBranch()
	if err := w.PutBit(false, &dummyBranch); err != nil {
		return nil, err
	}

	return w, nil
}

// put performs the core arithmetic encoding operation
func (w *VPXBoolWriter) put(bit bool, branch *Branch, tmpValue uint64, tmpRange uint32) (uint64, uint32) {
	probability := uint32(branch.GetProbability())

	split := 1 + (((tmpRange - 1) * probability) >> 8)

	branch.RecordAndUpdateBit(bit)

	if bit {
		tmpValue += uint64(split)
		tmpRange -= split
	} else {
		tmpRange = split
	}

	shift := leadingZeros8(uint8(tmpRange))

	tmpRange <<= shift
	tmpValue <<= shift

	// Check if we need to flush bytes to buffer
	// Mask for top 7 bits: 0xfe00000000000000
	const highBitsMask = uint64(0xfe00000000000000)
	if tmpValue&highBitsMask != 0 {
		// Calculate leftover bits after removing:
		// - 48 bits (6 bytes) flushed to buffer
		// - 8 bits for coding accuracy
		// - 1 bit for marker
		// - 1 bit for overflow
		leftoverBits := leadingZeros64(tmpValue) + 2

		// Rotate so top 6 bytes are ones to write
		vAligned := rotateLeft64(tmpValue, leftoverBits)

		if (vAligned & 1) != 0 {
			w.carry()
		}

		// Append top 6 bytes to buffer in big endian
		w.buffer = append(w.buffer,
			byte(vAligned>>56),
			byte(vAligned>>48),
			byte(vAligned>>40),
			byte(vAligned>>32),
			byte(vAligned>>24),
			byte(vAligned>>16),
		)

		// Mask remaining bits and restore position, adding marker
		tmpValue = ((vAligned & 0xffff) | 0x20000) >> leftoverBits
	}

	return tmpValue, tmpRange
}

// carry handles overflow propagation in the buffer
func (w *VPXBoolWriter) carry() {
	x := len(w.buffer) - 1

	for w.buffer[x] == 0xFF {
		w.buffer[x] = 0
		x--
	}

	w.buffer[x]++
}

// PutBit writes a single bit using the given branch for probability
func (w *VPXBoolWriter) PutBit(value bool, branch *Branch) error {
	tmpValue := w.lowValue
	tmpRange := w.rang

	tmpValue, tmpRange = w.put(value, branch, tmpValue, tmpRange)

	w.lowValue = tmpValue
	w.rang = tmpRange

	return nil
}

// PutGrid writes a value using a grid (binary tree) of branches
func (w *VPXBoolWriter) PutGrid(v uint8, branches []Branch) error {
	A := len(branches)
	// Check if A is power of 2
	if A&(A-1) != 0 {
		panic("branches length must be power of 2")
	}

	tmpValue := w.lowValue
	tmpRange := w.rang

	numBits := bitLength(A) - 1
	serializedSoFar := 1

	for i := numBits - 1; i >= 0; i-- {
		curBit := (v & (1 << i)) != 0
		tmpValue, tmpRange = w.put(curBit, &branches[serializedSoFar], tmpValue, tmpRange)

		serializedSoFar <<= 1
		if curBit {
			serializedSoFar |= 1
		}
	}

	w.lowValue = tmpValue
	w.rang = tmpRange

	return nil
}

// PutNBits writes n bits using the given branches
func (w *VPXBoolWriter) PutNBits(bits int, numBits int, branches []Branch) error {
	tmpValue := w.lowValue
	tmpRange := w.rang

	for i := numBits - 1; i >= 0; i-- {
		bit := (bits & (1 << i)) != 0
		tmpValue, tmpRange = w.put(bit, &branches[i], tmpValue, tmpRange)
	}

	w.lowValue = tmpValue
	w.rang = tmpRange

	return nil
}

// PutUnaryEncoded writes a unary encoded value
func (w *VPXBoolWriter) PutUnaryEncoded(v int, branches []Branch) error {
	A := len(branches)
	if v > A {
		panic("value exceeds branches length")
	}

	tmpValue := w.lowValue
	tmpRange := w.rang

	for i := 0; i < A; i++ {
		curBit := v != i
		tmpValue, tmpRange = w.put(curBit, &branches[i], tmpValue, tmpRange)
		if !curBit {
			break
		}
	}

	w.lowValue = tmpValue
	w.rang = tmpRange

	return nil
}

// Finish writes any remaining data and flushes to the writer
func (w *VPXBoolWriter) Finish() error {
	tmpValue := w.lowValue
	streamBits := 64 - leadingZeros64(tmpValue) - 2

	tmpValue <<= (63 - streamBits)
	if tmpValue&(1<<63) != 0 {
		w.carry()
	}

	shift := uint32(63)
	numBytes := (streamBits + 7) >> 3
	for i := uint32(0); i < numBytes; i++ {
		shift -= 8
		w.buffer = append(w.buffer, byte(tmpValue>>shift))
	}

	_, err := w.writer.Write(w.buffer)
	return err
}

// FlushNonFinalData writes buffered data that is definitely final
func (w *VPXBoolWriter) FlushNonFinalData() error {
	i := len(w.buffer)
	if i > 1 {
		i--
		for w.buffer[i] == 0xFF {
			i--
		}

		if _, err := w.writer.Write(w.buffer[:i]); err != nil {
			return err
		}
		w.buffer = w.buffer[i:]
	}
	return nil
}

// Helper functions

// leadingZeros8 returns leading zero bits in an 8-bit value
func leadingZeros8(x uint8) uint32 {
	if x == 0 {
		return 8
	}
	n := uint32(0)
	if x&0xF0 == 0 {
		n += 4
		x <<= 4
	}
	if x&0xC0 == 0 {
		n += 2
		x <<= 2
	}
	if x&0x80 == 0 {
		n += 1
	}
	return n
}

// rotateLeft64 rotates x left by n bits
func rotateLeft64(x uint64, n uint32) uint64 {
	return (x << n) | (x >> (64 - n))
}
