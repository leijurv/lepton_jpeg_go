package lepton

import "math"

const (
	blockTypes       = 2
	numericLengthMax = 12
	coefBits         = MaxExponent - 1
	nonZero7x7CountBits = 6 // ilog2(49) + 1
	nonZeroEdgeCountBits = 3 // ilog2(7) + 1
	numNonZero7x7Bins = 9
	numNonZeroEdgeBins = 7
	numNonZero7x7ContextBins = 9 // 1 + NonZeroToBin[25] where NonZeroToBin[25] = 8

	residualThresholdCountsD1 = 1 << (1 + ResidualNoiseFloor)
	residualThresholdCountsD2 = 1 + ResidualNoiseFloor - 2
	residualThresholdCountsD3 = 1 << ResidualNoiseFloor
)

// Model contains all probability branches for arithmetic coding
type Model struct {
	PerColor [blockTypes]ModelPerColor
	CountsDC [numericLengthMax]CountsDC
}

// NewModel creates a new Model with default branches
func NewModel() *Model {
	m := &Model{}

	// Initialize all branches to default
	for i := range m.PerColor {
		m.PerColor[i] = newModelPerColor()
	}
	for i := range m.CountsDC {
		m.CountsDC[i] = newCountsDC()
	}

	return m
}

// GetPerColor returns the ModelPerColor for the given color index
func (m *Model) GetPerColor(colorIndex int) *ModelPerColor {
	return &m.PerColor[colorIndex]
}

// ReadDC reads a DC coefficient
func (m *Model) ReadDC(boolReader *VPXBoolReader, colorIndex int, uncertainty, uncertainty2 int16) (int16, error) {
	exp, sign, bits := m.getDCBranches(uncertainty, uncertainty2, colorIndex)
	return readLengthSignCoef(boolReader, exp, sign, bits)
}

// WriteDC writes a DC coefficient
func (m *Model) WriteDC(boolWriter *VPXBoolWriter, colorIndex int, coef int16, uncertainty, uncertainty2 int16) error {
	exp, sign, bits := m.getDCBranches(uncertainty, uncertainty2, colorIndex)
	return writeLengthSignCoef(boolWriter, coef, exp, sign, bits)
}

func (m *Model) getDCBranches(uncertainty, uncertainty2 int16, colorIndex int) ([]Branch, *Branch, []Branch) {
	lenAbsMxm := u16BitLength(abs16(uncertainty))
	lenAbsOffsetToClosestEdge := u16BitLength(abs16(uncertainty2))
	lenAbsMxmClamp := min(int(lenAbsMxm), len(m.CountsDC)-1)

	exp := m.CountsDC[lenAbsMxmClamp].ExponentCounts[lenAbsOffsetToClosestEdge][:]
	sign := &m.PerColor[colorIndex].SignCounts[0][calcSignIndex(uncertainty2)+1] // +1 to separate from signCounts[0][0]
	bits := m.CountsDC[lenAbsMxmClamp].ResidualNoiseCounts[:]

	return exp, sign, bits
}

// ModelPerColor contains probability branches for a single color component
type ModelPerColor struct {
	NumNonZerosCounts7x7 [numNonZero7x7ContextBins][1 << nonZero7x7CountBits]Branch
	Counts               [numNonZero7x7Bins][49]Counts7x7
	NumNonZerosCounts1x8 [8][8][1 << nonZeroEdgeCountBits]Branch
	NumNonZerosCounts8x1 [8][8][1 << nonZeroEdgeCountBits]Branch
	CountsX              [numNonZeroEdgeBins][14]CountsEdge
	ResidualThresholdCounts [residualThresholdCountsD1][residualThresholdCountsD2][residualThresholdCountsD3]Branch
	SignCounts           [3][numericLengthMax]Branch
}

func newModelPerColor() ModelPerColor {
	m := ModelPerColor{}

	// Initialize all branches
	for i := range m.NumNonZerosCounts7x7 {
		for j := range m.NumNonZerosCounts7x7[i] {
			m.NumNonZerosCounts7x7[i][j] = NewBranch()
		}
	}

	for i := range m.Counts {
		for j := range m.Counts[i] {
			m.Counts[i][j] = newCounts7x7()
		}
	}

	for i := range m.NumNonZerosCounts1x8 {
		for j := range m.NumNonZerosCounts1x8[i] {
			for k := range m.NumNonZerosCounts1x8[i][j] {
				m.NumNonZerosCounts1x8[i][j][k] = NewBranch()
			}
		}
	}

	for i := range m.NumNonZerosCounts8x1 {
		for j := range m.NumNonZerosCounts8x1[i] {
			for k := range m.NumNonZerosCounts8x1[i][j] {
				m.NumNonZerosCounts8x1[i][j][k] = NewBranch()
			}
		}
	}

	for i := range m.CountsX {
		for j := range m.CountsX[i] {
			m.CountsX[i][j] = newCountsEdge()
		}
	}

	for i := range m.ResidualThresholdCounts {
		for j := range m.ResidualThresholdCounts[i] {
			for k := range m.ResidualThresholdCounts[i][j] {
				m.ResidualThresholdCounts[i][j][k] = NewBranch()
			}
		}
	}

	for i := range m.SignCounts {
		for j := range m.SignCounts[i] {
			m.SignCounts[i][j] = NewBranch()
		}
	}

	return m
}

// ReadCoef reads a coefficient from the 7x7 block
func (m *ModelPerColor) ReadCoef(boolReader *VPXBoolReader, zig49 int, numNonZerosBin int, bestPriorBitLen int) (int16, error) {
	exp, sign, bits := m.getCoefBranches(numNonZerosBin, zig49, bestPriorBitLen)
	return readLengthSignCoef(boolReader, exp, sign, bits)
}

// WriteCoef writes a coefficient to the 7x7 block
func (m *ModelPerColor) WriteCoef(boolWriter *VPXBoolWriter, coef int16, zig49 int, numNonZerosBin int, bestPriorBitLen int) error {
	exp, sign, bits := m.getCoefBranches(numNonZerosBin, zig49, bestPriorBitLen)
	return writeLengthSignCoef(boolWriter, coef, exp, sign, bits)
}

func (m *ModelPerColor) getCoefBranches(numNonZerosBin, zig49, bestPriorBitLen int) ([]Branch, *Branch, []Branch) {
	exp := m.Counts[numNonZerosBin][zig49].ExponentCounts[bestPriorBitLen][:]
	sign := &m.SignCounts[0][0]
	bits := m.Counts[numNonZerosBin][zig49].ResidualNoiseCounts[:]
	return exp, sign, bits
}

// ReadNonZero7x7Count reads the count of non-zero coefficients in the 7x7 block
func (m *ModelPerColor) ReadNonZero7x7Count(boolReader *VPXBoolReader, numNonZeros7x7ContextBin uint8) (uint8, error) {
	prob := m.NumNonZerosCounts7x7[numNonZeros7x7ContextBin][:]
	val, err := boolReader.GetGrid(prob)
	return uint8(val), err
}

// WriteNonZero7x7Count writes the count of non-zero coefficients in the 7x7 block
func (m *ModelPerColor) WriteNonZero7x7Count(boolWriter *VPXBoolWriter, numNonZeros7x7ContextBin uint8, numNonZeros7x7 uint8) error {
	prob := m.NumNonZerosCounts7x7[numNonZeros7x7ContextBin][:]
	return boolWriter.PutGrid(numNonZeros7x7, prob)
}

// ReadNonZeroEdgeCount reads the count of non-zero edge coefficients
func (m *ModelPerColor) ReadNonZeroEdgeCount(boolReader *VPXBoolReader, horizontal bool, estEob, numNonZerosBin uint8) (uint8, error) {
	prob := m.getNonZeroCountsEdge(horizontal, estEob, numNonZerosBin)
	val, err := boolReader.GetGrid(prob)
	return uint8(val), err
}

// WriteNonZeroEdgeCount writes the count of non-zero edge coefficients
func (m *ModelPerColor) WriteNonZeroEdgeCount(boolWriter *VPXBoolWriter, horizontal bool, estEob, numNonZerosBin uint8, numNonZerosEdge uint8) error {
	prob := m.getNonZeroCountsEdge(horizontal, estEob, numNonZerosBin)
	return boolWriter.PutGrid(numNonZerosEdge, prob)
}

func (m *ModelPerColor) getNonZeroCountsEdge(horizontal bool, estEob, numNonZerosBin uint8) []Branch {
	if horizontal {
		return m.NumNonZerosCounts8x1[estEob][numNonZerosBin][:]
	}
	return m.NumNonZerosCounts1x8[estEob][numNonZerosBin][:]
}

// ReadEdgeCoefficient reads an edge coefficient (row 0 or column 0)
func (m *ModelPerColor) ReadEdgeCoefficient(boolReader *VPXBoolReader, qt *QuantizationTables, zig15offset int, numNonZerosEdge uint8, bestPrior int32) (int16, error) {
	numNonZerosEdgeBin := int(numNonZerosEdge) - 1

	// Cap the bit length since prior prediction can be wonky
	bestPriorAbs := abs32(bestPrior)
	bestPriorBitLen := min(MaxExponent-1, int(u32BitLength(uint32(bestPriorAbs))))

	lengthBranches := m.CountsX[numNonZerosEdgeBin][zig15offset].ExponentCounts[bestPriorBitLen][:]
	length, err := boolReader.GetUnaryEncoded(lengthBranches)
	if err != nil {
		return 0, err
	}

	var coef int16 = 0
	if length != 0 {
		// best_prior in the initial Lepton implementation is stored as i32,
		// but the sign here is taken from its truncated i16 value
		sign := &m.SignCounts[calcSignIndex(int16(bestPrior))][bestPriorBitLen]

		neg, err := boolReader.GetBit(sign)
		if err != nil {
			return 0, err
		}
		neg = !neg

		coef = 1

		if length > 1 {
			minThreshold := int(qt.GetMinNoiseThreshold(zig15offset))
			i := length - 2

			if i >= minThreshold {
				threshProb := m.getResidualThresholdCounts(uint32(bestPriorAbs), minThreshold, length)

				decodedSoFar := 1
				for i >= minThreshold {
					curBit, err := boolReader.GetBit(&threshProb[decodedSoFar])
					if err != nil {
						return 0, err
					}

					coef <<= 1
					if curBit {
						coef |= 1
					}

					// Since we are not strict about rejecting jpegs with out of range coefs
					// we just make those less efficient by reusing the same probability bucket
					decodedSoFar = min(int(coef), len(threshProb)-1)
					i--
				}
			}

			if i >= 0 {
				resProb := m.CountsX[numNonZerosEdgeBin][zig15offset].ResidualNoiseCounts[:]
				bits, err := boolReader.GetNBits(i+1, resProb)
				if err != nil {
					return 0, err
				}
				coef <<= (i + 1)
				coef |= int16(bits)
			}
		}

		if neg {
			coef = -coef
		}
	}

	return coef, nil
}

func (m *ModelPerColor) getResidualThresholdCounts(bestPriorAbs uint32, minThreshold, length int) []Branch {
	// Need to & 0xffff to match C++ behavior
	idx1 := min(int((bestPriorAbs&0xffff)>>minThreshold), len(m.ResidualThresholdCounts)-1)
	idx2 := min(length-minThreshold-2, len(m.ResidualThresholdCounts[0])-1)
	return m.ResidualThresholdCounts[idx1][idx2][:]
}

// WriteEdgeCoefficient writes an edge coefficient (row 0 or column 0)
func (m *ModelPerColor) WriteEdgeCoefficient(boolWriter *VPXBoolWriter, qt *QuantizationTables, coef int16, zig15offset int, numNonZerosEdge uint8, bestPrior int32) error {
	numNonZerosEdgeBin := int(numNonZerosEdge) - 1

	// Cap the bit length since prior prediction can be wonky
	bestPriorAbs := abs32(bestPrior)
	bestPriorBitLen := min(MaxExponent-1, int(u32BitLength(uint32(bestPriorAbs))))

	absCoef := abs16(coef)
	length := int(u16BitLength(absCoef))

	if length > MaxExponent {
		return NewLeptonError(ExitCodeCoefficientOutOfRange, "coefficient out of range")
	}

	lengthBranches := m.CountsX[numNonZerosEdgeBin][zig15offset].ExponentCounts[bestPriorBitLen][:]
	if err := boolWriter.PutUnaryEncoded(length, lengthBranches); err != nil {
		return err
	}

	if coef != 0 {
		// best_prior in the initial Lepton implementation is stored as i32,
		// but the sign here is taken from its truncated i16 value
		sign := &m.SignCounts[calcSignIndex(int16(bestPrior))][bestPriorBitLen]

		if err := boolWriter.PutBit(coef >= 0, sign); err != nil {
			return err
		}

		if length > 1 {
			minThreshold := int(qt.GetMinNoiseThreshold(zig15offset))
			i := length - 2

			if i >= minThreshold {
				threshProb := m.getResidualThresholdCounts(uint32(bestPriorAbs), minThreshold, length)

				encodedSoFar := 1
				for i >= minThreshold {
					curBit := (absCoef & (1 << i)) != 0
					if err := boolWriter.PutBit(curBit, &threshProb[encodedSoFar]); err != nil {
						return err
					}

					encodedSoFar <<= 1
					if curBit {
						encodedSoFar |= 1
					}

					// Since we are not strict about rejecting jpegs with out of range coefs
					// we just make those less efficient by reusing the same probability bucket
					encodedSoFar = min(encodedSoFar, len(threshProb)-1)
					i--
				}
			}

			if i >= 0 {
				resProb := m.CountsX[numNonZerosEdgeBin][zig15offset].ResidualNoiseCounts[:]
				if err := boolWriter.PutNBits(int(absCoef), i+1, resProb); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// Counts7x7 contains probability branches for 7x7 block coefficients
type Counts7x7 struct {
	ExponentCounts      [numericLengthMax][MaxExponent]Branch
	ResidualNoiseCounts [coefBits]Branch
}

func newCounts7x7() Counts7x7 {
	c := Counts7x7{}
	for i := range c.ExponentCounts {
		for j := range c.ExponentCounts[i] {
			c.ExponentCounts[i][j] = NewBranch()
		}
	}
	for i := range c.ResidualNoiseCounts {
		c.ResidualNoiseCounts[i] = NewBranch()
	}
	return c
}

// CountsEdge contains probability branches for edge coefficients
type CountsEdge struct {
	ExponentCounts      [MaxExponent][MaxExponent]Branch
	ResidualNoiseCounts [3]Branch
}

func newCountsEdge() CountsEdge {
	c := CountsEdge{}
	for i := range c.ExponentCounts {
		for j := range c.ExponentCounts[i] {
			c.ExponentCounts[i][j] = NewBranch()
		}
	}
	for i := range c.ResidualNoiseCounts {
		c.ResidualNoiseCounts[i] = NewBranch()
	}
	return c
}

// CountsDC contains probability branches for DC coefficients
type CountsDC struct {
	ExponentCounts      [17][MaxExponent]Branch
	ResidualNoiseCounts [coefBits]Branch
}

func newCountsDC() CountsDC {
	c := CountsDC{}
	for i := range c.ExponentCounts {
		for j := range c.ExponentCounts[i] {
			c.ExponentCounts[i][j] = NewBranch()
		}
	}
	for i := range c.ResidualNoiseCounts {
		c.ResidualNoiseCounts[i] = NewBranch()
	}
	return c
}

// readLengthSignCoef reads a coefficient using length-sign-bits encoding
func readLengthSignCoef(boolReader *VPXBoolReader, magnitudeBranches []Branch, signBranch *Branch, bitsBranch []Branch) (int16, error) {
	length, err := boolReader.GetUnaryEncoded(magnitudeBranches)
	if err != nil {
		return 0, err
	}

	var coef int16 = 0
	if length != 0 {
		neg, err := boolReader.GetBit(signBranch)
		if err != nil {
			return 0, err
		}
		neg = !neg

		if length > 1 {
			bits, err := boolReader.GetNBits(length-1, bitsBranch)
			if err != nil {
				return 0, err
			}
			coef = int16(bits)
		}

		coef |= int16(1 << (length - 1))

		if neg {
			coef = -coef
		}
	}

	return coef, nil
}

// writeLengthSignCoef writes a coefficient using length-sign-bits encoding
func writeLengthSignCoef(boolWriter *VPXBoolWriter, coef int16, magnitudeBranches []Branch, signBranch *Branch, bitsBranch []Branch) error {
	absCoef := abs16(coef)
	coefBitLen := int(u16BitLength(absCoef))

	if coefBitLen > len(magnitudeBranches) {
		return NewLeptonError(ExitCodeCoefficientOutOfRange, "coefficient > MAX_EXPONENT")
	}

	if err := boolWriter.PutUnaryEncoded(coefBitLen, magnitudeBranches); err != nil {
		return err
	}

	if coef != 0 {
		if err := boolWriter.PutBit(coef > 0, signBranch); err != nil {
			return err
		}
	}

	if coefBitLen > 1 {
		if err := boolWriter.PutNBits(int(absCoef), coefBitLen-1, bitsBranch); err != nil {
			return err
		}
	}

	return nil
}

// Helper functions
func abs16(x int16) uint16 {
	if x < 0 {
		return uint16(-x)
	}
	return uint16(x)
}

func abs32(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}

func u16BitLength(v uint16) uint8 {
	return 16 - uint8(leadingZeros16(v))
}

func u32BitLength(v uint32) uint8 {
	return 32 - uint8(leadingZeros32(v))
}

func leadingZeros16(x uint16) int {
	if x == 0 {
		return 16
	}
	n := 0
	if x&0xFF00 == 0 {
		n += 8
		x <<= 8
	}
	if x&0xF000 == 0 {
		n += 4
		x <<= 4
	}
	if x&0xC000 == 0 {
		n += 2
		x <<= 2
	}
	if x&0x8000 == 0 {
		n += 1
	}
	return n
}

func leadingZeros32(x uint32) int {
	if x == 0 {
		return 32
	}
	n := 0
	if x&0xFFFF0000 == 0 {
		n += 16
		x <<= 16
	}
	if x&0xFF000000 == 0 {
		n += 8
		x <<= 8
	}
	if x&0xF0000000 == 0 {
		n += 4
		x <<= 4
	}
	if x&0xC0000000 == 0 {
		n += 2
		x <<= 2
	}
	if x&0x80000000 == 0 {
		n += 1
	}
	return n
}

func calcSignIndex(val int16) int {
	if val == 0 {
		return 0
	}
	if val > 0 {
		return 1
	}
	return 2
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func absInt(x int) int {
	return int(math.Abs(float64(x)))
}
