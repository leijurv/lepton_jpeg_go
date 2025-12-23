package lepton

import (
	"fmt"
	"io"
)

// BitReader reads JPEG Huffman-encoded bitstream, handling 0xFF escape codes
type BitReader struct {
	inner       io.Reader
	bits        uint64
	bitsLeft    uint32
	cpos        uint32 // reset counter position
	eof         bool
	truncatedFF bool
	buffer      []byte
	bufferPos   int
	bufferLen   int
	totalRead   int64
}

// NewBitReader creates a new BitReader
func NewBitReader(reader io.Reader) *BitReader {
	return &BitReader{
		inner:  reader,
		buffer: make([]byte, 4096),
	}
}

// Read reads the specified number of bits from the stream
func (r *BitReader) Read(bitsToRead uint32) (uint16, error) {
	if bitsToRead == 0 {
		return 0, nil
	}

	if r.bitsLeft < bitsToRead {
		if err := r.fillRegister(bitsToRead); err != nil {
			return 0, err
		}
	}

	retval := uint16((r.bits >> (r.bitsLeft - bitsToRead)) & ((1 << bitsToRead) - 1))
	r.bitsLeft -= bitsToRead

	return retval, nil
}

// Peek returns the top byte of available bits and how many bits are available
func (r *BitReader) Peek() (uint8, uint32) {
	if r.bitsLeft >= 8 {
		return uint8(r.bits >> (r.bitsLeft - 8)), r.bitsLeft
	}
	if r.bitsLeft > 0 {
		return uint8(r.bits << (8 - r.bitsLeft)), r.bitsLeft
	}
	return 0, 0
}

// Advance consumes the specified number of bits without returning them
func (r *BitReader) Advance(bits uint32) {
	r.bitsLeft -= bits
}

// fillRegister reads more bytes into the bit register
func (r *BitReader) fillRegister(bitsToRead uint32) error {
	for r.bitsLeft < bitsToRead {
		b, err := r.readByte()
		if err != nil {
			if err == io.EOF {
				// In case of a truncated file, treat the rest as zeros
				r.eof = true
				r.bitsLeft += 8
				r.bits <<= 8
				continue
			}
			return err
		}

		// 0xff is an escape code
		if b == 0xff {
			next, err := r.readByte()
			if err != nil {
				if err == io.EOF {
					// Handle truncation in the middle of an escape
					// Assume this was an escaped 0xff
					r.bits = (r.bits << 8) | 0xff
					r.bitsLeft += 8
					r.truncatedFF = true
					continue
				}
				return err
			}

			if next == 0 {
				// This was an escaped 0xFF
				r.bits = (r.bits << 8) | 0xff
				r.bitsLeft += 8
			} else {
				// This was not an escaped 0xff
				return NewLeptonError(ExitCodeInvalidResetCode,
					fmt.Sprintf("invalid reset %02x %02x code found in stream", 0xff, next))
			}
		} else {
			r.bits = (r.bits << 8) | uint64(b)
			r.bitsLeft += 8
		}
	}
	return nil
}

// readByte reads a single byte from the buffer
func (r *BitReader) readByte() (byte, error) {
	if r.bufferPos >= r.bufferLen {
		n, err := r.inner.Read(r.buffer)
		if n == 0 {
			if err == nil {
				err = io.EOF
			}
			return 0, err
		}
		r.bufferLen = n
		r.bufferPos = 0
	}
	b := r.buffer[r.bufferPos]
	r.bufferPos++
	r.totalRead++
	return b, nil
}

// IsEOF returns true if end of file has been reached
func (r *BitReader) IsEOF() bool {
	return r.eof
}

// RemainingBuffer returns any unconsumed bytes in the internal buffer
// This should be called after scan completion to get data that was read ahead
func (r *BitReader) RemainingBuffer() []byte {
	if r.bufferPos >= r.bufferLen {
		return nil
	}
	return r.buffer[r.bufferPos:r.bufferLen]
}

// Overhang returns the number of bits already read from the current byte
// and the byte value with unread bits cleared
func (r *BitReader) Overhang() (uint8, uint8) {
	bitsAlreadyRead := uint8((64 - r.bitsLeft) & 7)
	mask := uint8(((1 << bitsAlreadyRead) - 1) << (8 - bitsAlreadyRead))
	return bitsAlreadyRead, uint8(r.bits) & mask
}

// ReadAndVerifyFillBits verifies padding bits at the end of an MCU
func (r *BitReader) ReadAndVerifyFillBits(padBit *uint8, padBitSet *bool) error {
	if r.bitsLeft > 0 && !r.eof {
		numBitsToRead := r.bitsLeft
		actual, err := r.Read(numBitsToRead)
		if err != nil {
			return err
		}

		allOne := uint16((1 << numBitsToRead) - 1)

		if !*padBitSet {
			if actual == 0 {
				*padBit = 0
				*padBitSet = true
			} else if actual == allOne {
				*padBit = 0xff
				*padBitSet = true
			} else {
				return NewLeptonError(ExitCodeInvalidPadding,
					fmt.Sprintf("inconsistent pad bits num_bits=%d pattern=%b", numBitsToRead, actual))
			}
		} else {
			expected := uint16(*padBit) & allOne
			if actual != expected {
				return NewLeptonError(ExitCodeInvalidPadding,
					fmt.Sprintf("padding of %d bits should be set actual=%b expected=%b",
						numBitsToRead, actual, expected))
			}
		}
	}
	return nil
}

// VerifyResetCode verifies and consumes a JPEG reset marker
func (r *BitReader) VerifyResetCode() error {
	// Read the two-byte reset marker
	h0, err := r.readByte()
	if err != nil {
		return err
	}
	h1, err := r.readByte()
	if err != nil {
		return err
	}

	expectedRst := MarkerRST0 + (byte(r.cpos) & 7)
	if h0 != 0xff || h1 != expectedRst {
		return NewLeptonError(ExitCodeInvalidResetCode,
			fmt.Sprintf("invalid reset code %02x %02x found in stream (expected ff %02x)",
				h0, h1, expectedRst))
	}

	// Start from scratch after RST
	r.cpos++
	r.bits = 0
	r.bitsLeft = 0

	return nil
}

// StreamPosition returns the approximate position in the stream
func (r *BitReader) StreamPosition() int64 {
	pos := r.totalRead - int64(r.bufferLen-r.bufferPos)
	if r.bitsLeft > 0 && !r.eof {
		// Account for bits we've read but not consumed
		bytesInBits := int64((r.bitsLeft + 7) / 8)
		pos -= bytesInBits
	}
	return pos
}
