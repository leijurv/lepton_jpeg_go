package lepton

// BitWriter writes bits to a byte buffer with JPEG-style FF escaping
type BitWriter struct {
	dataBuffer   []byte
	fillRegister uint64
	currentBit   uint32
}

// NewBitWriter creates a new BitWriter with the given initial buffer
func NewBitWriter(initialCapacity int) *BitWriter {
	return &BitWriter{
		dataBuffer:   make([]byte, 0, initialCapacity),
		fillRegister: 0,
		currentBit:   64,
	}
}

// Write writes the given value using the specified number of bits
func (w *BitWriter) Write(val uint32, numBits uint32) {
	if numBits == 0 {
		return
	}

	// Check if everything fits in the current register
	if numBits <= w.currentBit {
		if w.currentBit-numBits < 64 {
			w.fillRegister |= uint64(val) << (w.currentBit - numBits)
		}
		w.currentBit -= numBits
	} else {
		// Fill up the register to 64 bits and flush
		fill := w.fillRegister
		if numBits > w.currentBit {
			fill |= uint64(val) >> (numBits - w.currentBit)
		}

		leftoverNewBits := numBits - w.currentBit
		leftoverVal := val & ((1 << leftoverNewBits) - 1)

		// Check for 0xFF bytes and flush
		w.writeFFEncoded(fill)

		// Store leftover bits
		if leftoverNewBits < 64 {
			w.fillRegister = uint64(leftoverVal) << (64 - leftoverNewBits)
		} else {
			w.fillRegister = 0
		}
		w.currentBit = 64 - leftoverNewBits
	}
}

// writeFFEncoded writes 8 bytes, escaping any 0xFF bytes
func (w *BitWriter) writeFFEncoded(fill uint64) {
	for i := 0; i < 8; i++ {
		b := byte(fill >> (56 - (i * 8)))
		w.dataBuffer = append(w.dataBuffer, b)
		if b == 0xFF {
			w.dataBuffer = append(w.dataBuffer, 0x00) // Escape FF
		}
	}
}

// WriteByte writes a single unescaped byte (for markers)
func (w *BitWriter) WriteByte(b byte) {
	// Should only be called when byte-aligned
	w.dataBuffer = append(w.dataBuffer, b)
}

// WriteByteUnescaped writes a byte without FF escaping
func (w *BitWriter) WriteByteUnescaped(b byte) {
	w.dataBuffer = append(w.dataBuffer, b)
}

// Pad pads to the next byte boundary with the given fill bit pattern
func (w *BitWriter) Pad(fillBit byte) {
	offset := uint32(1)
	for (w.currentBit & 7) != 0 {
		var bit uint32 = 0
		if (fillBit & byte(offset)) != 0 {
			bit = 1
		}
		w.Write(bit, 1)
		offset <<= 1
	}

	w.flushWholeBytes()
}

// flushWholeBytes flushes complete bytes from the register to the buffer
func (w *BitWriter) flushWholeBytes() {
	for w.currentBit <= 56 {
		b := byte(w.fillRegister >> 56)
		w.dataBuffer = append(w.dataBuffer, b)
		if b == 0xFF {
			w.dataBuffer = append(w.dataBuffer, 0x00) // Escape FF
		}
		w.fillRegister <<= 8
		w.currentBit += 8
	}
}

// DetachBuffer returns the buffer and resets the writer
func (w *BitWriter) DetachBuffer() []byte {
	w.flushWholeBytes()
	result := w.dataBuffer
	w.dataBuffer = nil
	w.fillRegister = 0
	w.currentBit = 64
	return result
}

// GetBuffer returns the current buffer without detaching
func (w *BitWriter) GetBuffer() []byte {
	return w.dataBuffer
}

// HasNoRemainder returns true if there are no bits waiting to be written
func (w *BitWriter) HasNoRemainder() bool {
	return w.currentBit == 64
}

// ResetFromOverhang resets the writer with overhang bits from a previous state
func (w *BitWriter) ResetFromOverhang(overhangByte byte, numBits uint32) {
	w.dataBuffer = w.dataBuffer[:0]
	w.fillRegister = uint64(overhangByte) << 56
	w.currentBit = 64 - numBits
}

// Len returns the current length of the buffer
func (w *BitWriter) Len() int {
	return len(w.dataBuffer)
}
