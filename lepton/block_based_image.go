package lepton

// BlockBasedImage stores DCT coefficients as 8x8 blocks for a color component
type BlockBasedImage struct {
	// blocks holds all the coefficient blocks
	blocks []AlignedBlock

	// blockWidth is the number of blocks horizontally
	blockWidth uint32

	// originalHeight is the original block height (before potential truncation)
	originalHeight uint32

	// dposOffset stores the starting position for each row
	dposOffset []uint32
}

// NewBlockBasedImage creates a new BlockBasedImage for a component
func NewBlockBasedImage(componentInfo *ComponentInfo, luma *ComponentInfo) *BlockBasedImage {
	blockWidth := componentInfo.Bch
	blockHeight := componentInfo.Bcv
	totalBlocks := int(componentInfo.Bc)

	img := &BlockBasedImage{
		blocks:         make([]AlignedBlock, 0, totalBlocks), // Empty slice with capacity
		blockWidth:     blockWidth,
		originalHeight: blockHeight,
		dposOffset:     make([]uint32, blockHeight+1),
	}

	// Calculate dpos offsets for each row
	var ratio uint32 = 1
	if luma != nil && componentInfo != luma {
		ratio = luma.Bcv / componentInfo.Bcv
	}

	for y := uint32(0); y <= blockHeight; y++ {
		// Each row's dpos offset accounts for ratio with luma
		img.dposOffset[y] = y * blockWidth * ratio
	}

	return img
}

// NewBlockBasedImageSize creates a BlockBasedImage with specified dimensions
func NewBlockBasedImageSize(width, height uint32) *BlockBasedImage {
	totalBlocks := int(width * height)

	img := &BlockBasedImage{
		blocks:         make([]AlignedBlock, 0, totalBlocks), // Empty slice with capacity
		blockWidth:     width,
		originalHeight: height,
		dposOffset:     make([]uint32, height+1),
	}

	for y := uint32(0); y <= height; y++ {
		img.dposOffset[y] = y * width
	}

	return img
}

// GetBlockWidth returns the number of blocks horizontally
func (img *BlockBasedImage) GetBlockWidth() uint32 {
	return img.blockWidth
}

// GetOriginalHeight returns the original block height
func (img *BlockBasedImage) GetOriginalHeight() uint32 {
	return img.originalHeight
}

// GetBlockXY returns a pointer to the block at the given position
func (img *BlockBasedImage) GetBlockXY(blockX, blockY uint32) *AlignedBlock {
	index := blockY*img.blockWidth + blockX
	if index >= uint32(len(img.blocks)) {
		return nil
	}
	return &img.blocks[index]
}

// GetBlock returns a pointer to the block at the given linear index (dpos)
// NOTE: Returns &emptyBlock for non-existent blocks - DO NOT modify the returned block
// Use EnsureBlock if you need to modify the block
func (img *BlockBasedImage) GetBlock(dpos uint32) *AlignedBlock {
	if dpos >= uint32(len(img.blocks)) {
		return &emptyBlock
	}
	return &img.blocks[dpos]
}

// EnsureBlock ensures the block at dpos exists and returns a pointer to it
// This creates empty blocks up to dpos if they don't exist
func (img *BlockBasedImage) EnsureBlock(dpos uint32) *AlignedBlock {
	for uint32(len(img.blocks)) <= dpos {
		img.blocks = append(img.blocks, AlignedBlock{})
	}
	return &img.blocks[dpos]
}

// GetBlockByIndex returns a pointer to the block at the given linear index
func (img *BlockBasedImage) GetBlockByIndex(index int) *AlignedBlock {
	if index < 0 || index >= len(img.blocks) {
		return nil
	}
	return &img.blocks[index]
}

// SetBlock sets the block at the given position
func (img *BlockBasedImage) SetBlock(blockX, blockY uint32, block AlignedBlock) {
	index := blockY*img.blockWidth + blockX
	if index < uint32(len(img.blocks)) {
		img.blocks[index] = block
	}
}

// AppendBlock appends a block to the image
func (img *BlockBasedImage) AppendBlock(block AlignedBlock) {
	img.blocks = append(img.blocks, block)
}

// SetBlockByDpos sets the block at the given linear index
func (img *BlockBasedImage) SetBlockByDpos(dpos uint32, block AlignedBlock) {
	// Extend if necessary
	for uint32(len(img.blocks)) <= dpos {
		img.blocks = append(img.blocks, AlignedBlock{})
	}
	img.blocks[dpos] = block
}

// SetBlockByIndex sets the block at the given linear index
func (img *BlockBasedImage) SetBlockByIndex(index int, block AlignedBlock) {
	if index >= 0 && index < len(img.blocks) {
		img.blocks[index] = block
	}
}

// GetDposOffset returns the dpos offset for a given row
func (img *BlockBasedImage) GetDposOffset(y uint32) uint32 {
	if y >= uint32(len(img.dposOffset)) {
		return 0
	}
	return img.dposOffset[y]
}

// GetNumBlocks returns the total number of blocks
func (img *BlockBasedImage) GetNumBlocks() int {
	return len(img.blocks)
}

// GetBlocks returns the underlying blocks slice
func (img *BlockBasedImage) GetBlocks() []AlignedBlock {
	return img.blocks
}

// FillDCsFromLast fills DC values from the last values for progressive JPEG
func (img *BlockBasedImage) FillDCsFromLast(width, height uint32, lastDC int16) {
	for y := uint32(0); y < height; y++ {
		for x := uint32(0); x < width; x++ {
			block := img.GetBlockXY(x, y)
			if block != nil {
				block.SetDC(lastDC)
			}
		}
	}
}

// TruncateComponents holds information about truncated images
type TruncateComponents struct {
	truncBcv           []uint32
	truncBc            []uint32
	componentsCount    int
	mcuCountHorizontal uint32
	mcuCountVertical   uint32
}

// NewTruncateComponents creates a new TruncateComponents
func NewTruncateComponents() *TruncateComponents {
	return &TruncateComponents{}
}

// Init initializes TruncateComponents from a JPEG header
func (tc *TruncateComponents) Init(header *JpegHeader) {
	tc.mcuCountHorizontal = header.GetMcuh()
	tc.mcuCountVertical = header.GetMcuv()
	tc.componentsCount = header.Cmpc

	tc.truncBcv = make([]uint32, header.Cmpc)
	tc.truncBc = make([]uint32, header.Cmpc)

	for i := 0; i < header.Cmpc; i++ {
		tc.truncBcv[i] = header.CmpInfo[i].Bcv
		tc.truncBc[i] = header.CmpInfo[i].Bc
	}
}

// GetMaxCodedHeights returns the maximum coded heights for each component
func (tc *TruncateComponents) GetMaxCodedHeights() []uint32 {
	result := make([]uint32, tc.componentsCount)
	for i := 0; i < tc.componentsCount; i++ {
		result[i] = tc.truncBcv[i]
	}
	return result
}

// GetBlockHeight returns the block height for a component
func (tc *TruncateComponents) GetBlockHeight(cmp int) uint32 {
	if cmp < 0 || cmp >= len(tc.truncBcv) {
		return 0
	}
	return tc.truncBcv[cmp]
}

// GetComponentSizesInBlocks returns the block sizes for each component
func (tc *TruncateComponents) GetComponentSizesInBlocks() []uint32 {
	result := make([]uint32, tc.componentsCount)
	for i := 0; i < tc.componentsCount; i++ {
		result[i] = tc.truncBc[i]
	}
	return result
}

// SetTruncationBounds sets the truncation bounds based on max dpos values
func (tc *TruncateComponents) SetTruncationBounds(header *JpegHeader, maxDPos [4]uint32) {
	for i := 0; i < tc.componentsCount; i++ {
		tc.setBlockCountDpos(i, &header.CmpInfo[i], maxDPos[i]+1)
	}
}

func (tc *TruncateComponents) setBlockCountDpos(componentIdx int, ci *ComponentInfo, truncBc uint32) {
	var verticalScanLines uint32
	if truncBc%ci.Bch != 0 {
		verticalScanLines = truncBc/ci.Bch + 1
	} else {
		verticalScanLines = truncBc / ci.Bch
	}
	if verticalScanLines > ci.Bcv {
		verticalScanLines = ci.Bcv
	}

	ratio := ci.Bcv / tc.mcuCountVertical

	for verticalScanLines%ratio != 0 && verticalScanLines+1 <= ci.Bcv {
		verticalScanLines++
	}

	tc.truncBcv[componentIdx] = verticalScanLines
	tc.truncBc[componentIdx] = truncBc
}
