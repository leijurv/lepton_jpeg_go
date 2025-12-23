package lepton

// AlignedBlock holds 64 DCT coefficients in transposed order
// The order is: DC, then edge coefficients, then 7x7 interior
type AlignedBlock struct {
	RawData [64]int16
}

// NewAlignedBlock creates a new empty AlignedBlock
func NewAlignedBlock() AlignedBlock {
	return AlignedBlock{}
}

// NewAlignedBlockFromData creates an AlignedBlock from raw data
func NewAlignedBlockFromData(data [64]int16) AlignedBlock {
	return AlignedBlock{RawData: data}
}

// GetDC returns the DC coefficient
func (b *AlignedBlock) GetDC() int16 {
	return b.RawData[0]
}

// SetDC sets the DC coefficient
func (b *AlignedBlock) SetDC(value int16) {
	b.RawData[0] = value
}

// GetCoefficient returns the coefficient at the given index
func (b *AlignedBlock) GetCoefficient(index int) int16 {
	return b.RawData[index]
}

// SetCoefficient sets the coefficient at the given index
func (b *AlignedBlock) SetCoefficient(index int, value int16) {
	b.RawData[index] = value
}

// GetTransposedFromZigzag returns the coefficient at zigzag index, converted to transposed
func (b *AlignedBlock) GetTransposedFromZigzag(index int) int16 {
	return b.RawData[ZigzagToTransposed[index]]
}

// SetTransposedFromZigzag sets the coefficient at zigzag index, converting to transposed
func (b *AlignedBlock) SetTransposedFromZigzag(index int, value int16) {
	b.RawData[ZigzagToTransposed[index]] = value
}

// GetBlock returns a pointer to the raw data
func (b *AlignedBlock) GetBlock() *[64]int16 {
	return &b.RawData
}

// ZigzagToTransposedBlock converts a zigzag-ordered block to transposed order
func ZigzagToTransposedBlock(a [64]int16) AlignedBlock {
	return AlignedBlock{
		RawData: [64]int16{
			a[0], a[2], a[3], a[9], a[10], a[20], a[21], a[35],
			a[1], a[4], a[8], a[11], a[19], a[22], a[34], a[36],
			a[5], a[7], a[12], a[18], a[23], a[33], a[37], a[48],
			a[6], a[13], a[17], a[24], a[32], a[38], a[47], a[49],
			a[14], a[16], a[25], a[31], a[39], a[46], a[50], a[57],
			a[15], a[26], a[30], a[40], a[45], a[51], a[56], a[58],
			a[27], a[29], a[41], a[44], a[52], a[55], a[59], a[62],
			a[28], a[42], a[43], a[53], a[54], a[60], a[61], a[63],
		},
	}
}

// ZigzagFromTransposed converts transposed order back to zigzag order
func (b *AlignedBlock) ZigzagFromTransposed() AlignedBlock {
	a := b.RawData
	return AlignedBlock{
		RawData: [64]int16{
			a[0], a[8], a[1], a[2], a[9], a[16], a[24], a[17],
			a[10], a[3], a[4], a[11], a[18], a[25], a[32], a[40],
			a[33], a[26], a[19], a[12], a[5], a[6], a[13], a[20],
			a[27], a[34], a[41], a[48], a[56], a[49], a[42], a[35],
			a[28], a[21], a[14], a[7], a[15], a[22], a[29], a[36],
			a[43], a[50], a[57], a[58], a[51], a[44], a[37], a[30],
			a[23], a[31], a[38], a[45], a[52], a[59], a[60], a[53],
			a[46], a[39], a[47], a[54], a[61], a[62], a[55], a[63],
		},
	}
}

// GetCountOfNonZeros7x7 counts non-zero values in the 7x7 interior block
func (b *AlignedBlock) GetCountOfNonZeros7x7() uint8 {
	count := uint8(0)
	for row := 1; row < 8; row++ {
		for col := 1; col < 8; col++ {
			if b.RawData[row*8+col] != 0 {
				count++
			}
		}
	}
	return count
}

// Transpose returns a transposed copy of the block
func (b *AlignedBlock) Transpose() AlignedBlock {
	var result AlignedBlock
	for row := 0; row < 8; row++ {
		for col := 0; col < 8; col++ {
			result.RawData[col*8+row] = b.RawData[row*8+col]
		}
	}
	return result
}

// GetRow returns the values in a given row (0-7)
func (b *AlignedBlock) GetRow(row int) [8]int16 {
	var result [8]int16
	copy(result[:], b.RawData[row*8:(row+1)*8])
	return result
}

// GetCol returns the values in a given column (0-7)
func (b *AlignedBlock) GetCol(col int) [8]int16 {
	var result [8]int16
	for row := 0; row < 8; row++ {
		result[row] = b.RawData[row*8+col]
	}
	return result
}

// EmptyBlock is a constant empty block
var EmptyBlock = AlignedBlock{}
