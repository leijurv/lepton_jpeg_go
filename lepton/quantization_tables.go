package lepton

// QuantizationTables holds quantization tables and derived values for a component
type QuantizationTables struct {
	QTable             [64]uint16
	MinNoiseThreshold  [14]uint8
	Divisors           [64]int32
	BiasedDivisors     [64]int32
}

// NewQuantizationTables creates new quantization tables from raw table data
func NewQuantizationTables(table [64]uint16) *QuantizationTables {
	qt := &QuantizationTables{}

	// Copy the table in zigzag to transposed order
	for i := 0; i < 64; i++ {
		qt.QTable[ZigzagToTransposed[i]] = table[i]
	}

	// Calculate min noise thresholds for edge coefficients
	// Rust uses raster-order quantization_table, we use transposed QTable
	// Raster coord (row*8+col) → Transposed coord (col*8+row)
	for i := 0; i < 14; i++ {
		// Rust: coord = if i < 7 { i + 1 } else { (i - 6) * 8 }
		// For i < 7: raster position row=0, col=i+1 → transposed = (i+1)*8
		// For i >= 7: raster position row=i-6, col=0 → transposed = i-6
		var coordTR int
		if i < 7 {
			coordTR = (i + 1) * 8 // col=i+1, row=0 in transposed order
		} else {
			coordTR = i - 6 // col=0, row=i-6 in transposed order
		}

		q := qt.QTable[coordTR]
		if q < 9 {
			// freq_max = (FREQ_MAX[i] + q - 1) / q  (rounded up division)
			// Match Rust behavior for q == 0 (leave as FREQ_MAX[i] - 1).
			var freqMax uint16
			if q != 0 {
				freqMax = (FreqMax[i] + q - 1) / q
			} else {
				freqMax = FreqMax[i] - 1
			}

			maxLen := u16BitLength(freqMax)
			if maxLen > uint8(ResidualNoiseFloor) {
				qt.MinNoiseThreshold[i] = maxLen - uint8(ResidualNoiseFloor)
			} else {
				qt.MinNoiseThreshold[i] = 0
			}
		} else {
			qt.MinNoiseThreshold[i] = 0
		}
	}

	// Calculate divisors for IDCT
	for i := 0; i < 64; i++ {
		if qt.QTable[i] != 0 {
			qt.Divisors[i] = int32(qt.QTable[i])
			qt.BiasedDivisors[i] = int32(qt.QTable[i]) / 2
		}
	}

	return qt
}

// GetMinNoiseThreshold returns the minimum noise threshold for an edge coefficient position
func (qt *QuantizationTables) GetMinNoiseThreshold(zig15offset int) uint8 {
	if zig15offset < 0 || zig15offset >= 14 {
		return 0
	}
	return qt.MinNoiseThreshold[zig15offset]
}

// GetQ returns the quantization value at the given transposed position
func (qt *QuantizationTables) GetQ(pos int) uint16 {
	return qt.QTable[pos]
}

// GetDivisor returns the divisor at the given transposed position
func (qt *QuantizationTables) GetDivisor(pos int) int32 {
	return qt.Divisors[pos]
}

// GetBiasedDivisor returns half the quantization value (for rounding)
func (qt *QuantizationTables) GetBiasedDivisor(pos int) int32 {
	return qt.BiasedDivisors[pos]
}

// GetQTransposed returns the quantization value at the given transposed position
func (qt *QuantizationTables) GetQTransposed(pos int) uint16 {
	return qt.QTable[pos]
}
