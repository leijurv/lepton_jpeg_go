package lepton

import (
	"io"
)

// LeptonDecoder decodes Lepton-compressed data back to DCT coefficients
type LeptonDecoder struct {
	model      *Model
	boolReader *VPXBoolReader
	qt         []*QuantizationTables
	header     *JpegHeader
}

// NewLeptonDecoder creates a new LeptonDecoder
func NewLeptonDecoder(reader io.Reader, header *JpegHeader) (*LeptonDecoder, error) {
	boolReader, err := NewVPXBoolReader(reader)
	if err != nil {
		return nil, err
	}

	decoder := &LeptonDecoder{
		model:      NewModel(),
		boolReader: boolReader,
		qt:         make([]*QuantizationTables, header.Cmpc),
		header:     header,
	}

	// Create quantization tables for each component
	for i := 0; i < header.Cmpc; i++ {
		qtIdx := header.CmpInfo[i].QTableIndex
		decoder.qt[i] = NewQuantizationTables(header.QTables[qtIdx])
	}

	return decoder, nil
}

// DecodeRowRange decodes blocks for a range of luma rows
func (d *LeptonDecoder) DecodeRowRange(
	images []*BlockBasedImage,
	lumaYStart, lumaYEnd uint32,
	lastDC [MaxComponents]int16,
	maxDPos [MaxComponents]uint32,
	earlyEof bool,
) error {
	// Initialize truncate components for row spec calculation
	tc := NewTruncateComponents()
	tc.Init(d.header)

	// For early EOF files, set truncation bounds based on maxDPos
	if earlyEof {
		tc.SetTruncationBounds(d.header, maxDPos)
	}

	maxCodedHeights := tc.GetMaxCodedHeights()
	componentSizesInBlocks := tc.GetComponentSizesInBlocks()

	// Neighbor summary cache: 2 rows per component (current and above)
	neighborSummaryCache := make([][]NeighborSummary, len(images))
	isTopRow := make([]bool, len(images))

	for i, img := range images {
		numNonZerosLength := img.GetBlockWidth() * 2
		neighborSummaryCache[i] = make([]NeighborSummary, numNonZerosLength)
		isTopRow[i] = true
	}

	// Calculate total decode iterations
	decodeIndex := uint32(0)

	// Decode rows
	for {
		rowSpec := getRowSpecFromIndex(decodeIndex, images, d.header.Mcuv, maxCodedHeights)

		if rowSpec.done {
			break
		}

		if rowSpec.skip {
			decodeIndex++
			continue
		}

		// Check if we're in range
		if rowSpec.lumaY < lumaYStart {
			decodeIndex++
			continue
		}
		if rowSpec.lumaY >= lumaYEnd {
			break
		}

		cmp := rowSpec.component
		currY := rowSpec.currY

		// Determine probability tables based on position
		var leftModel, middleModel *ProbabilityTables
		if isTopRow[cmp] {
			isTopRow[cmp] = false
			leftModel = NoNeighbors
			middleModel = LeftOnly
		} else {
			leftModel = TopOnly
			middleModel = AllNeighbors
		}

		// Decode the row for this component
		err := d.decodeRow(
			images[cmp],
			neighborSummaryCache[cmp],
			currY,
			cmp,
			leftModel,
			middleModel,
			componentSizesInBlocks[cmp],
		)
		if err != nil {
			return err
		}

		decodeIndex++
	}

	return nil
}

// decodeRow decodes a single row of blocks
func (d *LeptonDecoder) decodeRow(
	image *BlockBasedImage,
	neighborSummaryCache []NeighborSummary,
	currY uint32,
	componentIdx int,
	leftModel, middleModel *ProbabilityTables,
	componentSizeInBlocks uint32,
) error {
	blockWidth := image.GetBlockWidth()
	colorIndex := getColorIndex(componentIdx)
	modelColor := d.model.GetPerColor(colorIndex)
	qt := d.qt[componentIdx]

	// Create block context for this row
	ctx := NewBlockContextForRow(currY, image)

	for blockX := uint32(0); blockX < blockWidth; blockX++ {
		// Select probability table based on position
		pt := leftModel
		if blockX > 0 {
			pt = middleModel
		}

		// Get neighbor data
		neighbors := ctx.GetNeighborData(image, neighborSummaryCache, pt)

		// Decode the block
		block, ns, err := d.decodeBlock(modelColor, qt, pt, colorIndex, neighbors)
		if err != nil {
			return err
		}

		// Store the block
		image.AppendBlock(block)

		// Store neighbor summary for next block
		ctx.SetNeighborSummaryHere(neighborSummaryCache, ns)

		offset := ctx.Next()

		// For truncated files, check if we've reached the truncation boundary
		if offset >= componentSizeInBlocks {
			return nil
		}
	}

	return nil
}

// decodeBlock decodes a single 8x8 block following Rust's read_coefficient_block
// Order: 7x7 interior → edges → DC
func (d *LeptonDecoder) decodeBlock(
	modelColor *ModelPerColor,
	qt *QuantizationTables,
	pt *ProbabilityTables,
	colorIndex int,
	neighbors *NeighborData,
) (AlignedBlock, NeighborSummary, error) {
	var block AlignedBlock

	// Step 1: Read the 7x7 interior coefficients
	numNonZeros7x7ContextBin := pt.CalcNumNonZeros7x7ContextBin(neighbors)

	numNonZeros7x7, err := modelColor.ReadNonZero7x7Count(d.boolReader, numNonZeros7x7ContextBin)
	if err != nil {
		return block, NeighborSummary{}, err
	}

	if numNonZeros7x7 > 49 {
		return block, NeighborSummary{}, NewLeptonError(ExitCodeStreamInconsistent, "numNonzeros7x7 > 49")
	}

	// Build raster for IDCT (dequantized coefficients in transposed order)
	var raster [8][8]int32

	// Track eob_x and eob_y (furthest non-zero positions)
	var eobX, eobY uint8 = 0, 0

	numNonZeros7x7Remaining := int(numNonZeros7x7)

	if numNonZeros7x7Remaining > 0 {
		// Calculate best priors from neighbors
		bestPriors := pt.CalcCoefficientContext7x7AavgBlock(neighbors)

		// Calculate bin for number of non-zeros
		numNonZerosBin := numNonZerosToBin7x7(numNonZeros7x7Remaining)

		// Iterate through coefficients in FORWARD zigzag order
		for zig49 := 0; zig49 < 49 && numNonZeros7x7Remaining > 0; zig49++ {
			coordTR := Unzigzag49TR[zig49]
			bestPriorBitLen := u16BitLength(bestPriors[coordTR])

			coef, err := modelColor.ReadCoef(d.boolReader, zig49, numNonZerosBin, int(bestPriorBitLen))
			if err != nil {
				return block, NeighborSummary{}, err
			}

			if coef != 0 {
				// Calculate x/y from transposed coord: coord_tr = y + x*8
				by := coordTR & 7
				bx := coordTR >> 3

				if bx > eobX {
					eobX = bx
				}
				if by > eobY {
					eobY = by
				}

				block.RawData[coordTR] = coef
				raster[coordTR>>3][coordTR&7] = int32(coef) * int32(qt.GetQTransposed(int(coordTR)))

				numNonZeros7x7Remaining--
				if numNonZeros7x7Remaining > 0 {
					numNonZerosBin = numNonZerosToBin7x7(numNonZeros7x7Remaining)
				}
			}
		}
	}

	if numNonZeros7x7Remaining > 0 {
		return block, NeighborSummary{}, NewLeptonError(ExitCodeStreamInconsistent, "not enough nonzeros in 7x7 block")
	}

	// Step 2: Decode edge coefficients
	numNonZerosBin := (numNonZeros7x7 + 3) / 7

	// Calculate current edge predictors from raster
	horizPred, vertPred := pt.PredictCurrentEdges(neighbors, &raster)

	// Decode horizontal edge (row 0, columns 1-7)
	nextHorizPred, err := d.decodeOneEdge(
		modelColor, qt, pt, &block, &raster,
		horizPred[:], true, numNonZerosBin, eobX,
	)
	if err != nil {
		return block, NeighborSummary{}, err
	}

	// Decode vertical edge (column 0, rows 1-7)
	nextVertPred, err := d.decodeOneEdge(
		modelColor, qt, pt, &block, &raster,
		vertPred[:], false, numNonZerosBin, eobY,
	)
	if err != nil {
		return block, NeighborSummary{}, err
	}

	// Calculate next edge predictions for neighbor blocks
	nextHorizPredFinal, nextVertPredFinal := pt.PredictNextEdges(&raster)

	// Step 3: Decode DC coefficient
	q0 := int32(qt.GetQ(0))
	dcResult := pt.AdvPredictDCPix(
		&raster,
		q0,
		neighbors,
		d.header.Use16BitAdvPredict,
		d.header.Use16BitDCEstimate,
	)

	dcDiff, err := d.model.ReadDC(d.boolReader, colorIndex, dcResult.Uncertainty, dcResult.Uncertainty2)
	if err != nil {
		return block, NeighborSummary{}, err
	}

	finalDC := advPredictOrUnpredictDC(dcDiff, true, dcResult.PredictedDC)
	block.SetDC(int16(finalDC))

	// Create neighbor summary
	// Use nextHorizPredFinal and nextVertPredFinal for edge coefs,
	// but we need the prediction values from decode for the coef predictors
	ns := NewNeighborSummaryFromDecode(
		dcResult.NextEdgePixelsH,
		dcResult.NextEdgePixelsV,
		int32(block.GetDC())*q0,
		numNonZeros7x7,
		combineEdgePreds(nextHorizPredFinal, nextHorizPred),
		combineEdgePreds(nextVertPredFinal, nextVertPred),
	)

	return block, ns, nil
}

// decodeOneEdge decodes one edge (horizontal or vertical)
func (d *LeptonDecoder) decodeOneEdge(
	modelColor *ModelPerColor,
	qt *QuantizationTables,
	pt *ProbabilityTables,
	block *AlignedBlock,
	raster *[8][8]int32,
	pred []int32,
	horizontal bool,
	numNonZerosBin uint8,
	estEob uint8,
) ([8]int32, error) {
	var result [8]int32

	numNonZerosEdge, err := modelColor.ReadNonZeroEdgeCount(d.boolReader, horizontal, estEob, numNonZerosBin)
	if err != nil {
		return result, err
	}

	var delta int
	var zig15offset int

	if horizontal {
		delta = 8 // Move along row (columns 1-7 of row 0)
		zig15offset = 0
	} else {
		delta = 1 // Move along column (rows 1-7 of column 0)
		zig15offset = 7
	}

	coordTR := delta // Start at position 1 (skip DC at 0)

	for lane := 0; lane < 7; lane++ {
		if numNonZerosEdge == 0 {
			break
		}

		bestPrior, err := pt.CalcCoefficientContext8Lak(qt, coordTR, pred, horizontal)
		if err != nil {
			return result, err
		}

		coef, err := modelColor.ReadEdgeCoefficient(d.boolReader, qt, zig15offset, numNonZerosEdge, bestPrior)
		if err != nil {
			return result, err
		}

		if coef != 0 {
			numNonZerosEdge--
			block.RawData[coordTR] = coef
			raster[coordTR>>3][coordTR&7] = int32(coef) * int32(qt.GetQTransposed(coordTR))
		}

		coordTR += delta
		zig15offset++
	}

	if numNonZerosEdge != 0 {
		return result, NewLeptonError(ExitCodeStreamInconsistent, "edge decode incomplete")
	}

	return result, nil
}

// Helper to combine edge predictions
func combineEdgePreds(next, current [8]int32) [8]int32 {
	return next // The next predictions from raster are what we store
}

// RowSpec describes which row and component to process
type RowSpec struct {
	lumaY                uint32
	component            int
	currY                uint32
	mcuRowIndex          uint32
	lastRowToCompleteMcu bool
	skip                 bool
	done                 bool
}

// getRowSpecFromIndex calculates which row and component to process for a given decode index
func getRowSpecFromIndex(
	decodeIndex uint32,
	imageData []*BlockBasedImage,
	mcuv uint32,
	maxCodedHeights []uint32,
) RowSpec {
	numCmp := len(imageData)

	heights := make([]uint32, numCmp)
	componentMultiple := make([]uint32, numCmp)
	var mcuMultiple uint32 = 0

	for i := 0; i < numCmp; i++ {
		heights[i] = imageData[i].GetOriginalHeight()
		componentMultiple[i] = heights[i] / mcuv
		mcuMultiple += componentMultiple[i]
	}

	mcuRow := decodeIndex / mcuMultiple
	minRowLumaY := mcuRow * componentMultiple[0]

	retval := RowSpec{
		skip:                 false,
		done:                 false,
		mcuRowIndex:          mcuRow,
		component:            numCmp,
		lumaY:                minRowLumaY,
		currY:                0,
		lastRowToCompleteMcu: false,
	}

	placeWithinScan := decodeIndex - (mcuRow * mcuMultiple)

	for i := numCmp - 1; i >= 0; i-- {
		if placeWithinScan < componentMultiple[i] {
			retval.component = i
			retval.currY = (mcuRow * componentMultiple[i]) + placeWithinScan
			retval.lastRowToCompleteMcu = (placeWithinScan+1 == componentMultiple[i]) && (i == 0)

			if retval.currY >= maxCodedHeights[i] {
				retval.skip = true
				retval.done = true
				for j := 0; j < numCmp-1; j++ {
					if mcuRow*componentMultiple[j] < maxCodedHeights[j] {
						retval.done = false
					}
				}
			}

			if i == 0 {
				retval.lumaY = retval.currY
			}

			break
		} else {
			placeWithinScan -= componentMultiple[i]
		}

		if i == 0 {
			retval.skip = true
			retval.done = true
			break
		}
	}

	return retval
}

// getColorIndex returns 0 for luma, 1 for chroma
func getColorIndex(component int) int {
	if component == 0 {
		return 0
	}
	return 1
}

// numNonZerosToBin7x7 maps non-zero count to bin
func numNonZerosToBin7x7(numNonZeros int) int {
	if numNonZeros >= len(NonZeroToBin7x7) {
		return int(NonZeroToBin7x7[len(NonZeroToBin7x7)-1])
	}
	return int(NonZeroToBin7x7[numNonZeros])
}

// advPredictOrUnpredictDC adjusts DC coefficient prediction
func advPredictOrUnpredictDC(savedDC int16, recoverOriginal bool, predictedVal int32) int32 {
	maxValue := int32(1 << (MaxExponent - 1))
	minValue := -maxValue
	adjustmentFactor := (2 * maxValue) + 1

	var retval int32
	if recoverOriginal {
		retval = int32(savedDC) + predictedVal
	} else {
		retval = int32(savedDC) - predictedVal
	}

	if retval < minValue {
		retval += adjustmentFactor
	}
	if retval > maxValue {
		retval -= adjustmentFactor
	}

	return retval
}
