package lepton

// NeighborSummary caches edge pixels and coefficients from neighboring blocks
// for prediction in the arithmetic coding
type NeighborSummary struct {
	// EdgePixelsH contains 8 horizontal edge pixels (bottom row pixels)
	EdgePixelsH [8]int16

	// EdgePixelsV contains 8 vertical edge pixels (right column pixels)
	EdgePixelsV [8]int16

	// EdgeCoefsH contains 8 horizontal edge coefficient predictors
	EdgeCoefsH [8]int32

	// EdgeCoefsV contains 8 vertical edge coefficient predictors
	EdgeCoefsV [8]int32

	// NumNonZeros is the count of non-zero coefficients in the 7x7 block
	NumNonZeros uint8
}

// NewNeighborSummaryFromDecode creates a NeighborSummary from decoded data
func NewNeighborSummaryFromDecode(
	edgePixelsH, edgePixelsV [8]int16,
	dcDeq int32,
	numNonZeros7x7 uint8,
	horizPred, vertPred [8]int32,
) NeighborSummary {
	ns := NeighborSummary{
		EdgeCoefsH:  horizPred,
		EdgeCoefsV:  vertPred,
		NumNonZeros: numNonZeros7x7,
	}

	// Add DC contribution to edge pixels
	dcAsInt16 := int16(dcDeq)
	for i := 0; i < 8; i++ {
		ns.EdgePixelsH[i] = edgePixelsH[i] + dcAsInt16
		ns.EdgePixelsV[i] = edgePixelsV[i] + dcAsInt16
	}

	return ns
}

// GetNumNonZeros returns the count of non-zero 7x7 coefficients
func (ns *NeighborSummary) GetNumNonZeros() uint8 {
	return ns.NumNonZeros
}

// GetVerticalPix returns the vertical edge pixels
func (ns *NeighborSummary) GetVerticalPix() [8]int16 {
	return ns.EdgePixelsV
}

// GetHorizontalPix returns the horizontal edge pixels
func (ns *NeighborSummary) GetHorizontalPix() [8]int16 {
	return ns.EdgePixelsH
}

// GetVerticalCoef returns the vertical edge coefficient predictors
func (ns *NeighborSummary) GetVerticalCoef() [8]int32 {
	return ns.EdgeCoefsV
}

// GetHorizontalCoef returns the horizontal edge coefficient predictors
func (ns *NeighborSummary) GetHorizontalCoef() [8]int32 {
	return ns.EdgeCoefsH
}
