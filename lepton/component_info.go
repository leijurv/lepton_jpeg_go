package lepton

const (
	// MaxComponents is the maximum number of color components
	MaxComponents = 4
)

// ComponentInfo holds metadata about a JPEG color component
type ComponentInfo struct {
	// QTableIndex is the quantization table index
	QTableIndex uint8

	// HuffDC is the DC Huffman table index
	HuffDC uint8

	// HuffAC is the AC Huffman table index
	HuffAC uint8

	// Sfv is the vertical sampling factor
	Sfv uint32

	// Sfh is the horizontal sampling factor
	Sfh uint32

	// Mbs is the blocks in MCU
	Mbs uint32

	// Bcv is the block count vertical (interleaved)
	Bcv uint32

	// Bch is the block count horizontal (interleaved)
	Bch uint32

	// Bc is the total block count (interleaved)
	Bc uint32

	// Ncv is the block count vertical (non-interleaved)
	Ncv uint32

	// Nch is the block count horizontal (non-interleaved)
	Nch uint32

	// Nc is the total block count (non-interleaved)
	Nc uint32

	// Sid is the statistical identity
	Sid uint32

	// Jid is the JPEG internal ID
	Jid uint8
}

// NewComponentInfo creates a new ComponentInfo with default values
func NewComponentInfo() ComponentInfo {
	return ComponentInfo{
		QTableIndex: 0xff,
		Sfv:         0xffffffff,
		Sfh:         0xffffffff,
		Mbs:         0xffffffff,
		Bcv:         0xffffffff,
		Bch:         0xffffffff,
		Bc:          0xffffffff,
		Ncv:         0xffffffff,
		Nch:         0xffffffff,
		Nc:          0xffffffff,
		Sid:         0xffffffff,
		Jid:         0xff,
		HuffDC:      0xff,
		HuffAC:      0xff,
	}
}

// GetBlockWidth returns the width of the component in blocks
func (c *ComponentInfo) GetBlockWidth() uint32 {
	return c.Bch
}

// GetBlockHeight returns the height of the component in blocks
func (c *ComponentInfo) GetBlockHeight() uint32 {
	return c.Bcv
}
