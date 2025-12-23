package lepton

// ProbabilityTables tracks which neighbors are present for probability context
type ProbabilityTables struct {
	leftPresent  bool
	abovePresent bool
	allPresent   bool
}

// Pre-defined probability tables for different neighbor configurations
var (
	NoNeighbors   = &ProbabilityTables{leftPresent: false, abovePresent: false, allPresent: false}
	TopOnly       = &ProbabilityTables{leftPresent: false, abovePresent: true, allPresent: false}
	LeftOnly      = &ProbabilityTables{leftPresent: true, abovePresent: false, allPresent: false}
	AllNeighbors  = &ProbabilityTables{leftPresent: true, abovePresent: true, allPresent: true}
)

// IsAllPresent returns true if both neighbors are present
func (pt *ProbabilityTables) IsAllPresent() bool {
	return pt.allPresent
}

// IsLeftPresent returns true if the left neighbor is present
func (pt *ProbabilityTables) IsLeftPresent() bool {
	return pt.leftPresent
}

// IsAbovePresent returns true if the above neighbor is present
func (pt *ProbabilityTables) IsAbovePresent() bool {
	return pt.abovePresent
}

// CalcNumNonZeros7x7ContextBin calculates the context bin for 7x7 non-zero count
func (pt *ProbabilityTables) CalcNumNonZeros7x7ContextBin(neighbors *NeighborData) uint8 {
	var numNonZerosAbove, numNonZerosLeft uint8 = 0, 0

	if pt.allPresent || pt.abovePresent {
		numNonZerosAbove = neighbors.NeighborContextAbove.NumNonZeros
	}
	if pt.allPresent || pt.leftPresent {
		numNonZerosLeft = neighbors.NeighborContextLeft.NumNonZeros
	}

	var numNonZerosContext int
	if !pt.allPresent && pt.abovePresent && !pt.leftPresent {
		numNonZerosContext = (int(numNonZerosAbove) + 1) / 2
	} else if !pt.allPresent && pt.leftPresent && !pt.abovePresent {
		numNonZerosContext = (int(numNonZerosLeft) + 1) / 2
	} else if pt.allPresent || (pt.leftPresent && pt.abovePresent) {
		numNonZerosContext = (int(numNonZerosAbove) + int(numNonZerosLeft) + 2) / 4
	} else {
		numNonZerosContext = 0
	}

	if numNonZerosContext >= len(NonZeroToBin) {
		numNonZerosContext = len(NonZeroToBin) - 1
	}
	return NonZeroToBin[numNonZerosContext]
}

// CalcCoefficientContext7x7AavgBlock calculates prior values from neighbors for 7x7 coefficients
// The result is stored in transposed/column-major order (col*8 + row) to match Rust
func (pt *ProbabilityTables) CalcCoefficientContext7x7AavgBlock(neighbors *NeighborData) [64]uint16 {
	var bestPrior [64]uint16

	if pt.allPresent {
		// Approximate average of 3 neighbors with weighted formula
		// Iterate in column-major order to match Rust's SIMD layout
		for col := 1; col < 8; col++ {
			for row := 0; row < 8; row++ {
				idx := col*8 + row // transposed order
				leftVal := abs16(neighbors.Left.RawData[idx])
				aboveVal := abs16(neighbors.Above.RawData[idx])
				aboveLeftVal := abs16(neighbors.AboveLeft.RawData[idx])
				// (left + above) * 13 + above_left * 6) >> 5
				bestPrior[idx] = uint16(((uint32(leftVal)+uint32(aboveVal))*13 + uint32(aboveLeftVal)*6) >> 5)
			}
		}
	} else if pt.leftPresent {
		for col := 1; col < 8; col++ {
			for row := 0; row < 8; row++ {
				idx := col*8 + row // transposed order
				bestPrior[idx] = abs16(neighbors.Left.RawData[idx])
			}
		}
	} else if pt.abovePresent {
		for col := 1; col < 8; col++ {
			for row := 0; row < 8; row++ {
				idx := col*8 + row // transposed order
				bestPrior[idx] = abs16(neighbors.Above.RawData[idx])
			}
		}
	}
	// else leave all zeros

	return bestPrior
}

// PredictCurrentEdges calculates edge predictors for the current block
func (pt *ProbabilityTables) PredictCurrentEdges(neighbors *NeighborData, raster *[8][8]int32) ([8]int32, [8]int32) {
	// Load initial predictors from neighborhood blocks
	horizPred := neighbors.NeighborContextAbove.EdgeCoefsH
	vertPred := neighbors.NeighborContextLeft.EdgeCoefsV

	// Subtract contributions from current block coefficients
	for col := 1; col < 8; col++ {
		icos := IcosBased8192Scaled[col]
		// Update vertical prediction
		for row := 0; row < 8; row++ {
			vertPred[row] -= raster[col][row] * icos
		}
		// Update horizontal prediction (sum across column)
		var horizSum int32 = 0
		for row := 0; row < 8; row++ {
			horizSum += raster[col][row] * IcosBased8192Scaled[row]
		}
		horizPred[col] -= horizSum
	}

	return horizPred, vertPred
}

// PredictNextEdges calculates edge predictors for neighborhood blocks
func (pt *ProbabilityTables) PredictNextEdges(raster *[8][8]int32) ([8]int32, [8]int32) {
	var horizPred [8]int32
	var vertPred [8]int32

	// Initialize vertical prediction from column 0
	for row := 0; row < 8; row++ {
		vertPred[row] = IcosBased8192ScaledPM[0] * raster[0][row]
	}

	for col := 1; col < 8; col++ {
		icosPM := IcosBased8192ScaledPM[col]
		// Horizontal prediction: sum across column with alternating signs
		var horizSum int32 = 0
		for row := 0; row < 8; row++ {
			horizSum += IcosBased8192ScaledPM[row] * raster[col][row]
		}
		horizPred[col] = horizSum

		// Vertical prediction: accumulate
		for row := 0; row < 8; row++ {
			vertPred[row] += icosPM * raster[col][row]
		}
	}

	return horizPred, vertPred
}

// CalcCoefficientContext8Lak calculates the LAK context for edge coefficients
func (pt *ProbabilityTables) CalcCoefficientContext8Lak(qt *QuantizationTables, coefficientTR int, pred []int32, horizontal bool) (int32, error) {
	if !pt.allPresent && ((horizontal && !pt.abovePresent) || (!horizontal && !pt.leftPresent)) {
		return 0, nil
	}

	var idx int
	if horizontal {
		idx = coefficientTR >> 3
	} else {
		idx = coefficientTR
	}

	bestPrior := pred[idx]
	div := int32(qt.GetQTransposed(coefficientTR)) << 13

	if div == 0 {
		return 0, NewLeptonError(ExitCodeUnsupportedJpegWithZeroIdct0, "division by zero in coefficient context calculation")
	}

	return bestPrior / div, nil
}

// PredictDCResult contains the results of DC prediction
type PredictDCResult struct {
	PredictedDC     int32
	Uncertainty     int16
	Uncertainty2    int16
	NextEdgePixelsH [8]int16
	NextEdgePixelsV [8]int16
}

// AdvPredictDCPix performs advanced DC prediction using IDCT-based pixel prediction
func (pt *ProbabilityTables) AdvPredictDCPix(
	raster *[8][8]int32,
	q0 int32,
	neighbors *NeighborData,
	use16bitAdvPredict bool,
	use16bitDCEstimate bool,
) PredictDCResult {
	// Run IDCT with DC=0 to get pixels sans DC
	pixelsSansDC := runIDCTForPrediction(raster)

	// Calculate prediction values
	vPred := calcPred(pixelsSansDC[0][:], pixelsSansDC[1][:], use16bitAdvPredict)
	hPred := calcPredColumn(pixelsSansDC, 0, 1, use16bitAdvPredict)

	// Calculate next edge pixels
	nextEdgePixelsV := calcPred(pixelsSansDC[7][:], pixelsSansDC[6][:], use16bitDCEstimate)
	nextEdgePixelsH := calcPredColumn(pixelsSansDC, 7, 6, use16bitDCEstimate)

	var minDC, maxDC int16
	var avgHorizontal, avgVertical int32

	if pt.allPresent {
		var horizDiff, vertDiff [8]int16
		for i := 0; i < 8; i++ {
			horizDiff[i] = neighbors.NeighborContextLeft.EdgePixelsH[i] - hPred[i]
			vertDiff[i] = neighbors.NeighborContextAbove.EdgePixelsV[i] - vPred[i]
		}

		minDC = minSlice(horizDiff[:])
		if m := minSlice(vertDiff[:]); m < minDC {
			minDC = m
		}
		maxDC = maxSlice(horizDiff[:])
		if m := maxSlice(vertDiff[:]); m > maxDC {
			maxDC = m
		}

		avgHorizontal = sumSlice(horizDiff[:])
		avgVertical = sumSlice(vertDiff[:])
	} else if pt.leftPresent {
		var horizDiff [8]int16
		for i := 0; i < 8; i++ {
			horizDiff[i] = neighbors.NeighborContextLeft.EdgePixelsH[i] - hPred[i]
		}
		minDC = minSlice(horizDiff[:])
		maxDC = maxSlice(horizDiff[:])
		avgHorizontal = sumSlice(horizDiff[:])
		avgVertical = avgHorizontal
	} else if pt.abovePresent {
		var vertDiff [8]int16
		for i := 0; i < 8; i++ {
			vertDiff[i] = neighbors.NeighborContextAbove.EdgePixelsV[i] - vPred[i]
		}
		minDC = minSlice(vertDiff[:])
		maxDC = maxSlice(vertDiff[:])
		avgVertical = sumSlice(vertDiff[:])
		avgHorizontal = avgVertical
	} else {
		return PredictDCResult{
			PredictedDC:     0,
			Uncertainty:     0,
			Uncertainty2:    0,
			NextEdgePixelsH: nextEdgePixelsH,
			NextEdgePixelsV: nextEdgePixelsV,
		}
	}

	avgmed := (avgVertical + avgHorizontal) >> 1
	uncertaintyVal := int16((int32(maxDC) - int32(minDC)) >> 3)
	avgHorizontal -= avgmed
	avgVertical -= avgmed

	farAfieldValue := avgVertical
	if abs32(avgHorizontal) < abs32(avgVertical) {
		farAfieldValue = avgHorizontal
	}

	uncertainty2Val := int16(farAfieldValue >> 3)

	var predictedDC int32 = 0
	if q0 != 0 {
		predictedDC = (avgmed/q0 + 4) >> 3
	}

	return PredictDCResult{
		PredictedDC:     predictedDC,
		Uncertainty:     uncertaintyVal,
		Uncertainty2:    uncertainty2Val,
		NextEdgePixelsH: nextEdgePixelsH,
		NextEdgePixelsV: nextEdgePixelsV,
	}
}

// runIDCTForPrediction runs IDCT on the raster for DC prediction
func runIDCTForPrediction(raster *[8][8]int32) [8][8]int16 {
	var result [8][8]int16

	// Run the IDCT (raster is in column-major, transposed form)
	// For DC prediction we need the spatial domain pixels
	runIDCTInternal(raster, &result)

	return result
}

// runIDCTInternal performs the actual IDCT computation
func runIDCTInternal(input *[8][8]int32, output *[8][8]int16) {
	const (
		W1    = 2841 // 2048*sqrt(2)*cos(1*pi/16)
		W2    = 2676 // 2048*sqrt(2)*cos(2*pi/16)
		W3    = 2408 // 2048*sqrt(2)*cos(3*pi/16)
		W5    = 1609 // 2048*sqrt(2)*cos(5*pi/16)
		W6    = 1108 // 2048*sqrt(2)*cos(6*pi/16)
		W7    = 565  // 2048*sqrt(2)*cos(7*pi/16)
		W1PW7 = W1 + W7
		W1MW7 = W1 - W7
		W2PW6 = W2 + W6
		W2MW6 = W2 - W6
		W3PW5 = W3 + W5
		W3MW5 = W3 - W5
		R2    = 181 // 256/sqrt(2)
	)

	var intermed [8][8]int32

	// Horizontal 1-D IDCT
	for y := 0; y < 8; y++ {
		x0 := (input[0][y] << 11) + 128
		x1 := input[4][y] << 11
		x2 := input[6][y]
		x3 := input[2][y]
		x4 := input[1][y]
		x5 := input[7][y]
		x6 := input[5][y]
		x7 := input[3][y]

		// Stage 1
		x8 := W7 * (x4 + x5)
		x4 = x8 + W1MW7*x4
		x5 = x8 - W1PW7*x5
		x8 = W3 * (x6 + x7)
		x6 = x8 - W3MW5*x6
		x7 = x8 - W3PW5*x7

		// Stage 2
		x8 = x0 + x1
		x0 -= x1
		x1 = W6 * (x3 + x2)
		x2 = x1 - W2PW6*x2
		x3 = x1 + W2MW6*x3
		x1 = x4 + x6
		x4 -= x6
		x6 = x5 + x7
		x5 -= x7

		// Stage 3
		x7 = x8 + x3
		x8 -= x3
		x3 = x0 + x2
		x0 -= x2
		x2 = (R2*(x4+x5) + 128) >> 8
		x4 = (R2*(x4-x5) + 128) >> 8

		// Stage 4
		intermed[y][0] = (x7 + x1) >> 8
		intermed[y][1] = (x3 + x2) >> 8
		intermed[y][2] = (x0 + x4) >> 8
		intermed[y][3] = (x8 + x6) >> 8
		intermed[y][4] = (x8 - x6) >> 8
		intermed[y][5] = (x0 - x4) >> 8
		intermed[y][6] = (x3 - x2) >> 8
		intermed[y][7] = (x7 - x1) >> 8
	}

	// Vertical 1-D IDCT
	for x := 0; x < 8; x++ {
		y0 := (intermed[0][x] << 8) + 8192
		y1 := intermed[4][x] << 8
		y2 := intermed[6][x]
		y3 := intermed[2][x]
		y4 := intermed[1][x]
		y5 := intermed[7][x]
		y6 := intermed[5][x]
		y7 := intermed[3][x]

		// Stage 1
		y8 := (W7*(y4+y5) + 4)
		y4 = (y8 + W1MW7*y4) >> 3
		y5 = (y8 - W1PW7*y5) >> 3
		y8 = (W3*(y6+y7) + 4)
		y6 = (y8 - W3MW5*y6) >> 3
		y7 = (y8 - W3PW5*y7) >> 3

		// Stage 2
		y8 = y0 + y1
		y0 -= y1
		y1 = (W6*(y3+y2) + 4)
		y2 = (y1 - W2PW6*y2) >> 3
		y3 = (y1 + W2MW6*y3) >> 3
		y1 = y4 + y6
		y4 -= y6
		y6 = y5 + y7
		y5 -= y7

		// Stage 3
		y7 = y8 + y3
		y8 -= y3
		y3 = y0 + y2
		y0 -= y2
		y2 = (R2*(y4+y5) + 128) >> 8
		y4 = (R2*(y4-y5) + 128) >> 8

		// Stage 4
		output[0][x] = int16((y7 + y1) >> 11)
		output[1][x] = int16((y3 + y2) >> 11)
		output[2][x] = int16((y0 + y4) >> 11)
		output[3][x] = int16((y8 + y6) >> 11)
		output[4][x] = int16((y8 - y6) >> 11)
		output[5][x] = int16((y0 - y4) >> 11)
		output[6][x] = int16((y3 - y2) >> 11)
		output[7][x] = int16((y7 - y1) >> 11)
	}
}

// calcPred calculates prediction from two rows with averaging
func calcPred(a1, a2 []int16, use16bit bool) [8]int16 {
	var result [8]int16
	for i := 0; i < 8; i++ {
		if use16bit {
			pixelDelta := int16(a1[i] - a2[i])
			halfDelta := (pixelDelta - (pixelDelta >> 15)) >> 1
			result[i] = int16(a1[i] + halfDelta)
		} else {
			pixelDelta := int32(a1[i]) - int32(a2[i])
			// Divide by 2 rounding towards 0
			// For negative: subtract the sign bit (-1) to round towards zero
			// This is: (pixelDelta - (pixelDelta >> 31)) >> 1
			halfDelta := (pixelDelta - (pixelDelta >> 31)) >> 1
			result[i] = int16(int32(a1[i]) + halfDelta)
		}
	}
	return result
}

// calcPredColumn calculates prediction from two columns
func calcPredColumn(pixels [8][8]int16, col1, col2 int, use16bit bool) [8]int16 {
	var a1, a2 [8]int16
	for row := 0; row < 8; row++ {
		a1[row] = pixels[row][col1]
		a2[row] = pixels[row][col2]
	}
	return calcPred(a1[:], a2[:], use16bit)
}

// Helper functions
func minSlice(s []int16) int16 {
	if len(s) == 0 {
		return 0
	}
	m := s[0]
	for _, v := range s[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

func maxSlice(s []int16) int16 {
	if len(s) == 0 {
		return 0
	}
	m := s[0]
	for _, v := range s[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

func sumSlice(s []int16) int32 {
	var sum int32 = 0
	for _, v := range s {
		sum += int32(v)
	}
	return sum
}
