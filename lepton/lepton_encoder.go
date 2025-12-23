package lepton

import (
	"io"
)

// LeptonEncoder encodes DCT coefficients to Lepton format
type LeptonEncoder struct {
	boolWriter *VPXBoolWriter
	model      *Model
	header     *JpegHeader
}

// NewLeptonEncoder creates a new LeptonEncoder
func NewLeptonEncoder(writer io.Writer, header *JpegHeader) (*LeptonEncoder, error) {
	boolWriter, err := NewVPXBoolWriter(writer)
	if err != nil {
		return nil, err
	}

	return &LeptonEncoder{
		boolWriter: boolWriter,
		model:      NewModel(),
		header:     header,
	}, nil
}

// EncodeRowRange encodes a range of rows from the image data
// Uses the same row iteration order as the decoder (getRowSpecFromIndex)
func (e *LeptonEncoder) EncodeRowRange(
	quantizationTables []*QuantizationTables,
	imageData []*BlockBasedImage,
	minY, maxY uint32,
) error {
	// Initialize helper structures
	numComponents := len(imageData)
	isTopRow := make([]bool, numComponents)
	for i := range isTopRow {
		isTopRow[i] = true
	}

	neighborSummaryCache := make([][]NeighborSummary, numComponents)
	for i := 0; i < numComponents; i++ {
		width := imageData[i].GetBlockWidth()
		neighborSummaryCache[i] = make([]NeighborSummary, width*2) // 2 rows for alternating
	}

	// Get max coded heights (all blocks for encoding)
	maxCodedHeights := make([]uint32, numComponents)
	for i := 0; i < numComponents; i++ {
		maxCodedHeights[i] = imageData[i].GetOriginalHeight()
	}

	// Use the same row iteration order as the decoder
	decodeIndex := uint32(0)
	for {
		rowSpec := getRowSpecFromIndex(decodeIndex, imageData, e.header.Mcuv, maxCodedHeights)

		if rowSpec.done {
			break
		}

		if rowSpec.skip {
			decodeIndex++
			continue
		}

		// Check if we're in range
		if rowSpec.lumaY < minY {
			decodeIndex++
			continue
		}
		if rowSpec.lumaY >= maxY {
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

		// Encode the row for this component
		if err := e.processRow(
			cmp,
			quantizationTables[cmp],
			imageData[cmp],
			neighborSummaryCache[cmp],
			currY,
			leftModel,
			middleModel,
		); err != nil {
			return err
		}

		decodeIndex++
	}

	return nil
}

// processRow encodes a single row of blocks
func (e *LeptonEncoder) processRow(
	cmp int,
	qt *QuantizationTables,
	imageData *BlockBasedImage,
	neighborSummaryCache []NeighborSummary,
	rowY uint32,
	leftModel *ProbabilityTables,
	middleModel *ProbabilityTables,
) error {
	blockContext := NewBlockContextForRow(rowY, imageData)
	blockWidth := imageData.GetBlockWidth()
	colorIndex := getColorIndex(cmp)

	for x := uint32(0); x < blockWidth; x++ {
		var pt *ProbabilityTables
		if x == 0 {
			pt = leftModel
		} else {
			pt = middleModel
		}

		block := imageData.GetBlock(blockContext.curBlockIndex)
		neighbors := blockContext.GetNeighborData(imageData, neighborSummaryCache, pt)

		ns, err := e.writeCoefficientsBlock(
			qt,
			pt,
			colorIndex,
			neighbors,
			block,
		)
		if err != nil {
			return err
		}

		blockContext.SetNeighborSummaryHere(neighborSummaryCache, ns)
		blockContext.Next()
	}

	return nil
}

// writeCoefficientsBlock writes an 8x8 coefficient block
func (e *LeptonEncoder) writeCoefficientsBlock(
	qt *QuantizationTables,
	pt *ProbabilityTables,
	colorIndex int,
	neighbors *NeighborData,
	block *AlignedBlock,
) (NeighborSummary, error) {
	modelColor := e.model.GetPerColor(colorIndex)

	// Step 1: Count and encode non-zero 7x7 coefficients
	numNonZeros7x7 := block.GetCountOfNonZeros7x7()
	numNonZeros7x7ContextBin := pt.CalcNumNonZeros7x7ContextBin(neighbors)

	if err := modelColor.WriteNonZero7x7Count(e.boolWriter, numNonZeros7x7ContextBin, numNonZeros7x7); err != nil {
		return NeighborSummary{}, err
	}

	// Build raster for prediction
	var raster [8][8]int32

	// Track furthest non-zero positions for edge prediction
	var eobX, eobY uint8 = 0, 0
	numNonZeros7x7Remaining := int(numNonZeros7x7)

	if numNonZeros7x7Remaining > 0 {
		// Calculate best priors from neighbors
		bestPriors := pt.CalcCoefficientContext7x7AavgBlock(neighbors)

		// Calculate bin for number of non-zeros
		numNonZerosBin := numNonZerosToBin7x7(numNonZeros7x7Remaining)

		// Iterate through coefficients in zigzag order
		for zig49 := 0; zig49 < 49; zig49++ {
			coordTR := Unzigzag49TR[zig49]
			bestPriorBitLen := u16BitLength(bestPriors[coordTR])

			coef := block.RawData[coordTR]

			if err := modelColor.WriteCoef(e.boolWriter, coef, zig49, numNonZerosBin, int(bestPriorBitLen)); err != nil {
				return NeighborSummary{}, err
			}

			if coef != 0 {
				by := coordTR & 7
				bx := coordTR >> 3

				if bx > eobX {
					eobX = bx
				}
				if by > eobY {
					eobY = by
				}

				raster[coordTR>>3][coordTR&7] = int32(coef) * int32(qt.GetQTransposed(int(coordTR)))

				numNonZeros7x7Remaining--
				if numNonZeros7x7Remaining == 0 {
					break
				}
				numNonZerosBin = numNonZerosToBin7x7(numNonZeros7x7Remaining)
			}
		}
	}

	// Step 2: Encode edge coefficients
	numNonZerosBin := (numNonZeros7x7 + 3) / 7

	// Calculate current edge predictors from raster
	horizPred, vertPred := pt.PredictCurrentEdges(neighbors, &raster)

	// Encode horizontal edge (row 0, columns 1-7)
	if err := e.encodeOneEdge(
		modelColor, qt, pt, block, &raster,
		horizPred[:], true, numNonZerosBin, eobX,
	); err != nil {
		return NeighborSummary{}, err
	}

	// Encode vertical edge (column 0, rows 1-7)
	if err := e.encodeOneEdge(
		modelColor, qt, pt, block, &raster,
		vertPred[:], false, numNonZerosBin, eobY,
	); err != nil {
		return NeighborSummary{}, err
	}

	// Calculate next edge predictions for neighbor blocks
	nextHorizPred, nextVertPred := pt.PredictNextEdges(&raster)

	// Step 3: Encode DC coefficient
	q0 := int32(qt.GetQ(0))
	dcResult := pt.AdvPredictDCPix(
		&raster,
		q0,
		neighbors,
		e.header.Use16BitAdvPredict,
		e.header.Use16BitDCEstimate,
	)

	// Calculate difference from predicted DC
	actualDC := block.GetDC()
	avgPredictedDC := advPredictOrUnpredictDC(int16(actualDC), false, dcResult.PredictedDC)

	if err := e.model.WriteDC(e.boolWriter, colorIndex, int16(avgPredictedDC), dcResult.Uncertainty, dcResult.Uncertainty2); err != nil {
		return NeighborSummary{}, err
	}

	// Create neighbor summary
	ns := NewNeighborSummaryFromDecode(
		dcResult.NextEdgePixelsH,
		dcResult.NextEdgePixelsV,
		int32(actualDC)*q0,
		numNonZeros7x7,
		nextHorizPred,
		nextVertPred,
	)

	return ns, nil
}

// encodeOneEdge encodes one edge (horizontal or vertical)
func (e *LeptonEncoder) encodeOneEdge(
	modelColor *ModelPerColor,
	qt *QuantizationTables,
	pt *ProbabilityTables,
	block *AlignedBlock,
	raster *[8][8]int32,
	pred []int32,
	horizontal bool,
	numNonZerosBin uint8,
	estEob uint8,
) error {
	// Count non-zeros on this edge
	var numNonZerosEdge uint8
	var delta int
	var zig15offset int

	if horizontal {
		// Row 0, columns 1-7 (in transposed order: coefficients 8, 16, 24, 32, 40, 48, 56)
		for col := 1; col < 8; col++ {
			if block.RawData[col*8] != 0 {
				numNonZerosEdge++
			}
		}
		delta = 8
		zig15offset = 0
	} else {
		// Column 0, rows 1-7 (in transposed order: coefficients 1, 2, 3, 4, 5, 6, 7)
		for row := 1; row < 8; row++ {
			if block.RawData[row] != 0 {
				numNonZerosEdge++
			}
		}
		delta = 1
		zig15offset = 7
	}

	// Write the count
	if err := modelColor.WriteNonZeroEdgeCount(e.boolWriter, horizontal, estEob, numNonZerosBin, numNonZerosEdge); err != nil {
		return err
	}

	// Write the coefficients
	coordTR := delta
	for lane := 0; lane < 7; lane++ {
		if numNonZerosEdge == 0 {
			break
		}

		bestPrior, err := pt.CalcCoefficientContext8Lak(qt, coordTR, pred, horizontal)
		if err != nil {
			return err
		}

		coef := block.RawData[coordTR]

		if err := modelColor.WriteEdgeCoefficient(e.boolWriter, qt, coef, zig15offset, numNonZerosEdge, bestPrior); err != nil {
			return err
		}

		if coef != 0 {
			numNonZerosEdge--
		}

		// Update raster for subsequent predictions
		raster[coordTR>>3][coordTR&7] = int32(coef) * int32(qt.GetQTransposed(coordTR))

		coordTR += delta
		zig15offset++
	}

	return nil
}

// Finish completes encoding and writes final data
func (e *LeptonEncoder) Finish() error {
	return e.boolWriter.Finish()
}

