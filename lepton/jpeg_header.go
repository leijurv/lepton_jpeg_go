package lepton

// JPEG marker codes
const (
	MarkerSOI  = 0xD8 // Start Of Image
	MarkerEOI  = 0xD9 // End Of Image
	MarkerSOS  = 0xDA // Start Of Scan
	MarkerDQT  = 0xDB // Define Quantization Table
	MarkerDHT  = 0xC4 // Define Huffman Table
	MarkerDRI  = 0xDD // Define Restart Interval
	MarkerAPP0 = 0xE0 // Application Segment 0
	MarkerAPP1 = 0xE1 // Application Segment 1
	MarkerSOF0 = 0xC0 // Baseline DCT
	MarkerSOF1 = 0xC1 // Extended Sequential DCT
	MarkerSOF2 = 0xC2 // Progressive DCT
	MarkerRST0 = 0xD0 // Restart marker 0
	MarkerRST7 = 0xD7 // Restart marker 7
	MarkerCOM  = 0xFE // Comment
)

// JpegHeader contains parsed JPEG header information
type JpegHeader struct {
	// JpegType indicates baseline or progressive
	JpegType JpegType

	// CmpInfo contains component information for up to 4 components
	CmpInfo [MaxComponents]ComponentInfo

	// Cmpc is the component count
	Cmpc int

	// QTables contains up to 4 quantization tables
	QTables [4][64]uint16

	// HuffDC contains DC Huffman tables
	HuffDC [4]*HuffmanTable

	// HuffAC contains AC Huffman tables
	HuffAC [4]*HuffmanTable

	// Height is the image height in pixels
	Height uint32

	// Width is the image width in pixels
	Width uint32

	// Mcuh is the MCU count horizontal
	Mcuh uint32

	// Mcuv is the MCU count vertical
	Mcuv uint32

	// McuWidth is the MCU width in pixels
	McuWidth uint32

	// McuHeight is the MCU height in pixels
	McuHeight uint32

	// RestartInterval is the restart interval in MCUs
	RestartInterval uint16

	// MaxSfh is the maximum horizontal sampling factor
	MaxSfh uint32

	// MaxSfv is the maximum vertical sampling factor
	MaxSfv uint32

	// PadBit is the padding bit value (0x00 or 0xFF)
	PadBit *uint8

	// RawHeader stores the raw JPEG header bytes before SOS
	RawHeader []byte

	// ScanComponentOrder stores the order of components in the scan (by index into CmpInfo)
	ScanComponentOrder []int

	// Progressive scan parameters (also used for baseline where Ss=0, Se=63, Ah=0, Al=0)
	// CsFrom is spectral selection start (Ss) - inclusive
	CsFrom uint8
	// CsTo is spectral selection end (Se) - inclusive
	CsTo uint8
	// CsSah is successive approximation bit high (Ah)
	CsSah uint8
	// CsSal is successive approximation bit low (Al)
	CsSal uint8

	// Use16BitDCEstimate controls 16-bit DC estimate prediction compatibility.
	Use16BitDCEstimate bool

	// Use16BitAdvPredict controls 16-bit advanced prediction compatibility.
	Use16BitAdvPredict bool
}

// NewJpegHeader creates a new JpegHeader with default values
func NewJpegHeader() *JpegHeader {
	h := &JpegHeader{
		JpegType:           JpegTypeUnknown,
		Use16BitDCEstimate: true,
		Use16BitAdvPredict: true,
	}
	for i := range h.CmpInfo {
		h.CmpInfo[i] = NewComponentInfo()
	}
	return h
}

// GetMcuh returns the MCU count horizontal
func (h *JpegHeader) GetMcuh() uint32 {
	return h.Mcuh
}

// GetMcuv returns the MCU count vertical
func (h *JpegHeader) GetMcuv() uint32 {
	return h.Mcuv
}

// ComponentCountBlocksPerMcu returns the number of blocks per MCU for a component
func (h *JpegHeader) ComponentCountBlocksPerMcu(cmp int) uint32 {
	return h.CmpInfo[cmp].Mbs
}

// GetBlockWidth returns the block width for a component
func (h *JpegHeader) GetBlockWidth(cmp int) uint32 {
	return h.CmpInfo[cmp].Bch
}

// GetBlockHeight returns the block height for a component
func (h *JpegHeader) GetBlockHeight(cmp int) uint32 {
	return h.CmpInfo[cmp].Bcv
}

// HuffmanTable represents a Huffman table for encoding/decoding
type HuffmanTable struct {
	// NumCodes is the count of codes for each bit length (1-16)
	NumCodes [17]uint8

	// Symbols are the symbols in order of code length
	Symbols [256]uint8

	// SymbolCount is the total number of symbols
	SymbolCount int

	// FastLookup provides fast symbol lookup for short codes
	FastLookup [256]int16

	// MaxCode contains the maximum code for each bit length
	MaxCode [18]int32

	// ValPtr contains value pointers for each bit length
	ValPtr [17]int32

	// MinCode contains the minimum code for each bit length
	MinCode [17]int32
}

// NewHuffmanTable creates a new empty HuffmanTable
func NewHuffmanTable() *HuffmanTable {
	return &HuffmanTable{}
}

// BuildDerivedTable builds derived lookup tables for fast decoding
func (h *HuffmanTable) BuildDerivedTable() {
	// Count total symbols
	h.SymbolCount = 0
	for i := 1; i <= 16; i++ {
		h.SymbolCount += int(h.NumCodes[i])
	}

	// Build fast lookup table for codes up to 8 bits
	for i := range h.FastLookup {
		h.FastLookup[i] = -1
	}

	code := 0
	symbolIdx := 0
	for bits := 1; bits <= 8; bits++ {
		for i := 0; i < int(h.NumCodes[bits]); i++ {
			// Fill all table entries that start with this code
			shift := 8 - bits
			baseIdx := code << shift
			numEntries := 1 << shift
			for j := 0; j < numEntries; j++ {
				// Encode symbol and bit length in lookup value
				h.FastLookup[baseIdx+j] = int16(h.Symbols[symbolIdx]) | int16(bits<<8)
			}
			code++
			symbolIdx++
		}
		code <<= 1
	}

	// Build min/max code tables for longer codes
	code = 0
	symbolIdx = 0
	for bits := 1; bits <= 16; bits++ {
		h.MinCode[bits] = int32(code)
		h.ValPtr[bits] = int32(symbolIdx) - int32(code)

		if h.NumCodes[bits] > 0 {
			h.MaxCode[bits] = int32(code) + int32(h.NumCodes[bits]) - 1
			symbolIdx += int(h.NumCodes[bits])
		} else {
			h.MaxCode[bits] = -1
		}

		code = (code + int(h.NumCodes[bits])) << 1
	}
	h.MaxCode[17] = 0x7FFFFFFF
}

// ThreadHandoff contains information for thread partitioning
type ThreadHandoff struct {
	// LumaYStart is the starting luma Y block row
	LumaYStart uint32

	// LumaYEnd is the ending luma Y block row
	LumaYEnd uint32

	// SegmentSize is the size of the encoded segment
	SegmentSize uint32

	// OverhangByte is the partial byte at thread boundary
	OverhangByte uint8

	// NumOverhangBits is the number of bits in the overhang byte
	NumOverhangBits uint8

	// LastDC contains the last DC values for each component
	LastDC [MaxComponents]int16
}

// NewThreadHandoff creates a new ThreadHandoff with default values
func NewThreadHandoff() ThreadHandoff {
	return ThreadHandoff{}
}
