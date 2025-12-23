// Package core provides fundamental constants and types for Lepton JPEG compression
package lepton

// JpegType indicates whether JPEG is baseline or progressive
type JpegType int

const (
	JpegTypeUnknown JpegType = iota
	JpegTypeSequential
	JpegTypeProgressive
)

// JpegDecodeStatus indicates the current state of JPEG decoding
type JpegDecodeStatus int

const (
	DecodeInProgress JpegDecodeStatus = iota
	RestartIntervalExpired
	ScanCompleted
)

// ColorChannelNumBlockTypes is the number of color channel block types (Y, Cb, Cr)
const ColorChannelNumBlockTypes = 3

// RasterToZigzag maps raster order to zigzag order
var RasterToZigzag = [64]uint8{
	0, 1, 5, 6, 14, 15, 27, 28, 2, 4, 7, 13, 16, 26, 29, 42,
	3, 8, 12, 17, 25, 30, 41, 43, 9, 11, 18, 24, 31, 40, 44, 53,
	10, 19, 23, 32, 39, 45, 52, 54, 20, 22, 33, 38, 46, 51, 55, 60,
	21, 34, 37, 47, 50, 56, 59, 61, 35, 36, 48, 49, 57, 58, 62, 63,
}

// ZigzagToTransposed maps zigzag order to transposed order
var ZigzagToTransposed = [64]uint8{
	0, 8, 1, 2, 9, 16, 24, 17, 10, 3, 4, 11, 18, 25, 32, 40,
	33, 26, 19, 12, 5, 6, 13, 20, 27, 34, 41, 48, 56, 49, 42, 35,
	28, 21, 14, 7, 15, 22, 29, 36, 43, 50, 57, 58, 51, 44, 37, 30,
	23, 31, 38, 45, 52, 59, 60, 53, 46, 39, 47, 54, 61, 62, 55, 63,
}

// Unzigzag49TR maps zigzag order to transposed 7x7 order
var Unzigzag49TR = [49]uint8{
	9, 17, 10, 11, 18, 25, 33, 26, 19, 12, 13, 20, 27, 34, 41, 49,
	42, 35, 28, 21, 14, 15, 22, 29, 36, 43, 50, 57, 58, 51, 44, 37,
	30, 23, 31, 38, 45, 52, 59, 60, 53, 46, 39, 47, 54, 61, 62, 55,
	63,
}

// IcosBased8192Scaled contains precalculated IDCT values scaled by 8192
// DC coef is zeroed intentionally (used for coefficient prediction)
var IcosBased8192Scaled = [8]int32{0, 11363, 10703, 9633, 8192, 6436, 4433, 2260}

// IcosBased8192ScaledPM contains precalculated IDCT values with alternating signs
// Used for predicting edges for neighboring blocks
var IcosBased8192ScaledPM = [8]int32{8192, -11363, 10703, -9633, 8192, -6436, 4433, -2260}

// FreqMax contains maximum frequency values for edge coefficients
var FreqMax = [14]uint16{
	931, 985, 968, 1020, 968, 1020, 1020, 932, 985, 967, 1020, 969, 1020, 1020,
}

// NonZeroToBin maps non-zero count to bin for prediction (26 elements)
// Used in calc_non_zero_counts_context_7x7 for context bin calculation
// Matches Rust NON_ZERO_TO_BIN
var NonZeroToBin = [26]uint8{
	0, 1, 2, 3, 4, 4, 5, 5, 5, 6, 6, 6, 6, 7, 7, 7, 7, 7, 7, 7, 7, 8, 8, 8, 8, 8,
}

// NonZeroToBin7x7 maps non-zero count in 7x7 block to bin
var NonZeroToBin7x7 = [50]uint8{
	0, 0, 1, 2, 3, 3, 4, 4, 4, 5, 5, 5, 5, 6, 6, 6, 6, 6, 6, 6, 6, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
}

// ResidualNoiseFloor is the noise floor for residual encoding
const ResidualNoiseFloor = 7

// LeptonVersion is the Lepton format version
const LeptonVersion uint8 = 1

// SmallFileBytesPerEncodingThread is bytes per thread for small files
const SmallFileBytesPerEncodingThread = 125000

// MaxThreadsSupportedByLeptonFormat is the max threads the format supports
const MaxThreadsSupportedByLeptonFormat = 16

// EOI is the JPEG End Of Image marker
var EOI = [2]byte{0xFF, 0xD9}

// SOI is the JPEG Start Of Image marker
var SOI = [2]byte{0xFF, 0xD8}

// LeptonFileHeader is the Lepton file magic number (tau symbol in UTF-8)
var LeptonFileHeader = [2]byte{0xcf, 0x84}

// LeptonHeaderBaselineJpegType marks baseline JPEG
var LeptonHeaderBaselineJpegType = [1]byte{'Z'}

// LeptonHeaderProgressiveJpegType marks progressive JPEG
var LeptonHeaderProgressiveJpegType = [1]byte{'X'}

// LeptonHeaderMarker is the header section marker
var LeptonHeaderMarker = [3]byte{'H', 'D', 'R'}

// LeptonHeaderPadMarker is the padding section marker
var LeptonHeaderPadMarker = [3]byte{'P', '0', 'D'}

// LeptonHeaderJpgRestartsMarker is the restart section marker
var LeptonHeaderJpgRestartsMarker = [3]byte{'C', 'R', 'S'}

// LeptonHeaderJpgRestartErrorsMarker is the restart errors section marker
var LeptonHeaderJpgRestartErrorsMarker = [3]byte{'F', 'R', 'S'}

// LeptonHeaderLumaSplitMarker is the luma split marker
var LeptonHeaderLumaSplitMarker = [2]byte{'H', 'H'}

// LeptonHeaderEarlyEofMarker is the early EOF marker
var LeptonHeaderEarlyEofMarker = [3]byte{'E', 'E', 'E'}

// LeptonHeaderPrefixGarbageMarker is the prefix garbage marker
var LeptonHeaderPrefixGarbageMarker = [3]byte{'P', 'G', 'R'}

// LeptonHeaderGarbageMarker is the garbage section marker
var LeptonHeaderGarbageMarker = [3]byte{'G', 'R', 'B'}

// LeptonHeaderCompletionMarker is the completion marker
var LeptonHeaderCompletionMarker = [3]byte{'C', 'M', 'P'}

// MaxExponent is the maximum exponent for coefficient encoding
const MaxExponent = 11
