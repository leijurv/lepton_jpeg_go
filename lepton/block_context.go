package lepton

// BlockContext tracks the current block position in a row
type BlockContext struct {
	blockWidth               uint32
	curBlockIndex            uint32
	curNeighborSummaryIndex  uint32
	aboveNeighborSummaryIndex uint32
}

// NeighborData contains references to neighboring blocks and their summaries
type NeighborData struct {
	Above               *AlignedBlock
	Left                *AlignedBlock
	AboveLeft           *AlignedBlock
	NeighborContextAbove *NeighborSummary
	NeighborContextLeft  *NeighborSummary
}

// Empty block for when neighbors don't exist
var emptyBlock = AlignedBlock{}
var emptyNeighborSummary = NeighborSummary{}

// NewBlockContextForRow creates a new BlockContext for a row at given y-coordinate
func NewBlockContextForRow(y uint32, image *BlockBasedImage) *BlockContext {
	blockWidth := image.GetBlockWidth()
	curBlockIndex := blockWidth * y

	// Alternate between two rows in the summary cache
	var curNeighborSummaryIndex, aboveNeighborSummaryIndex uint32
	if (y & 1) != 0 {
		curNeighborSummaryIndex = blockWidth
		aboveNeighborSummaryIndex = 0
	} else {
		curNeighborSummaryIndex = 0
		aboveNeighborSummaryIndex = blockWidth
	}

	return &BlockContext{
		blockWidth:               blockWidth,
		curBlockIndex:            curBlockIndex,
		curNeighborSummaryIndex:  curNeighborSummaryIndex,
		aboveNeighborSummaryIndex: aboveNeighborSummaryIndex,
	}
}

// Next advances to the next block position and returns the new block index
func (ctx *BlockContext) Next() uint32 {
	ctx.curBlockIndex++
	ctx.curNeighborSummaryIndex++
	ctx.aboveNeighborSummaryIndex++
	return ctx.curBlockIndex
}

// GetNeighborData returns neighbor blocks and summaries based on probability tables
func (ctx *BlockContext) GetNeighborData(
	image *BlockBasedImage,
	neighborSummary []NeighborSummary,
	pt *ProbabilityTables,
) *NeighborData {
	nd := &NeighborData{
		Above:               &emptyBlock,
		Left:                &emptyBlock,
		AboveLeft:           &emptyBlock,
		NeighborContextAbove: &emptyNeighborSummary,
		NeighborContextLeft:  &emptyNeighborSummary,
	}

	if pt.IsAllPresent() {
		// All neighbors present
		nd.AboveLeft = image.GetBlock(ctx.curBlockIndex - ctx.blockWidth - 1)
		nd.Above = image.GetBlock(ctx.curBlockIndex - ctx.blockWidth)
		nd.Left = image.GetBlock(ctx.curBlockIndex - 1)
		nd.NeighborContextAbove = &neighborSummary[ctx.aboveNeighborSummaryIndex]
		nd.NeighborContextLeft = &neighborSummary[ctx.curNeighborSummaryIndex-1]
	} else {
		if pt.IsAbovePresent() {
			nd.Above = image.GetBlock(ctx.curBlockIndex - ctx.blockWidth)
			nd.NeighborContextAbove = &neighborSummary[ctx.aboveNeighborSummaryIndex]
		}
		if pt.IsLeftPresent() {
			nd.Left = image.GetBlock(ctx.curBlockIndex - 1)
			nd.NeighborContextLeft = &neighborSummary[ctx.curNeighborSummaryIndex-1]
		}
	}

	return nd
}

// SetNeighborSummaryHere stores the neighbor summary at the current position
func (ctx *BlockContext) SetNeighborSummaryHere(neighborSummaryCache []NeighborSummary, ns NeighborSummary) {
	neighborSummaryCache[ctx.curNeighborSummaryIndex] = ns
}

// GetHereIndex returns the current block index (for debugging)
func (ctx *BlockContext) GetHereIndex() uint32 {
	return ctx.curBlockIndex
}
