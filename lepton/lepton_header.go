package lepton

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
)

// LeptonHeader contains the parsed Lepton file header
type LeptonHeader struct {
	// Version is the Lepton format version
	Version uint8

	// JpegType indicates baseline or progressive
	JpegType JpegType

	// ThreadCount is the number of encoding threads used
	ThreadCount uint8

	// GitRevision is the encoder git revision (for debugging)
	GitRevision uint32

	// EncoderVersion is the encoder version
	EncoderVersion uint32

	// Use16BitDCEstimate controls 16-bit DC estimate prediction compatibility.
	Use16BitDCEstimate bool

	// Use16BitAdvPredict controls 16-bit advanced prediction compatibility.
	Use16BitAdvPredict bool

	// OriginalFileSize is the size of the original JPEG
	OriginalFileSize uint32

	// RawJpegHeader contains the raw JPEG header bytes
	RawJpegHeader []byte

	// RawJpegHeaderReadIndex tracks how much of RawJpegHeader has been consumed
	// (up to and including SOS marker)
	RawJpegHeaderReadIndex int

	// JpegHeader is the parsed JPEG header
	JpegHeader *JpegHeader

	// ThreadHandoffs contains partition information for each thread
	ThreadHandoffs []ThreadHandoff

	// RecoveryInfo contains information needed for exact reconstruction
	RecoveryInfo *ReconstructionInfo
}

// ReconstructionInfo holds information needed to exactly reconstruct the JPEG
type ReconstructionInfo struct {
	// PadBit is the padding bit value used in the original JPEG
	PadBit *uint8

	// RestartCount is the number of restart intervals
	RestartCount int

	// RestartCounts contains the restart interval counts
	RestartCounts []uint32

	// RestartCountsSet indicates if RestartCounts was explicitly set
	RestartCountsSet bool

	// RestartErrors contains error recovery info for restart intervals
	RestartErrors []int

	// GarbageData is any garbage data after EOI
	GarbageData []byte

	// PrefixGarbage is any garbage data before SOI
	PrefixGarbage []byte

	// EarlyEofEncountered indicates if early EOF was encountered
	EarlyEofEncountered bool

	// MaxCmp is the max component for early EOF
	MaxCmp uint32

	// MaxBpos is the max block position for early EOF
	MaxBpos uint32

	// MaxSah is the max successive approximation high for early EOF
	MaxSah uint8

	// MaxDpos is the max data position for each component for early EOF
	MaxDpos [4]uint32

	// TruncatedEOI indicates if the EOI marker was truncated
	TruncatedEOI bool
}

// NewLeptonHeader creates a new LeptonHeader
func NewLeptonHeader() *LeptonHeader {
	return &LeptonHeader{
		RecoveryInfo:       &ReconstructionInfo{},
		Use16BitDCEstimate: true,
		Use16BitAdvPredict: true,
	}
}

// ReadLeptonHeader reads and parses a Lepton file header from a reader
func ReadLeptonHeader(r io.Reader) (*LeptonHeader, error) {
	header := NewLeptonHeader()

	// Read fixed header (28 bytes)
	fixedHeader := make([]byte, 28)
	if _, err := io.ReadFull(r, fixedHeader); err != nil {
		return nil, fmt.Errorf("failed to read fixed header: %w", err)
	}

	// Verify magic number
	if fixedHeader[0] != LeptonFileHeader[0] || fixedHeader[1] != LeptonFileHeader[1] {
		return nil, ErrExitCode(ExitCodeBadLeptonFile, "invalid Lepton magic number")
	}

	// Parse fixed header fields
	header.Version = fixedHeader[2]
	if header.Version != LeptonVersion {
		return nil, ErrExitCode(ExitCodeVersionUnsupported,
			fmt.Sprintf("unsupported Lepton version %d", header.Version))
	}

	// JPEG type
	switch fixedHeader[3] {
	case LeptonHeaderBaselineJpegType[0]:
		header.JpegType = JpegTypeSequential
	case LeptonHeaderProgressiveJpegType[0]:
		header.JpegType = JpegTypeProgressive
	default:
		return nil, ErrExitCode(ExitCodeBadLeptonFile,
			fmt.Sprintf("invalid JPEG type marker: %c", fixedHeader[3]))
	}

	// Thread count
	header.ThreadCount = fixedHeader[4]

	// Parse git revision / MS format section (bytes 8-20)
	// Can be either git revision OR MS format with uncompressed header size and flags
	if fixedHeader[8] == 'M' && fixedHeader[9] == 'S' {
		// MS format: bytes 10-14 = uncompressed header size, byte 14 = flags, byte 15 = encoder version
		// bytes 16-20 = git revision prefix
		flags := fixedHeader[14]
		if (flags & 0x80) != 0 {
			header.Use16BitDCEstimate = (flags & 0x01) != 0
			header.Use16BitAdvPredict = (flags & 0x02) != 0
		}
		header.EncoderVersion = uint32(fixedHeader[15])
		header.GitRevision = binary.LittleEndian.Uint32(fixedHeader[16:20])
	} else {
		// Original format: bytes 8-12 = git revision
		header.GitRevision = binary.LittleEndian.Uint32(fixedHeader[8:12])
	}

	// Bytes 20-24: Original JPEG file size
	header.OriginalFileSize = binary.LittleEndian.Uint32(fixedHeader[20:24])

	// Bytes 24-28: Compressed header size
	compressedHeaderSize := binary.LittleEndian.Uint32(fixedHeader[24:28])

	// Read compressed header
	compressedHeader := make([]byte, compressedHeaderSize)
	if _, err := io.ReadFull(r, compressedHeader); err != nil {
		return nil, fmt.Errorf("failed to read compressed header: %w", err)
	}

	// Decompress header using zlib
	zlibReader, err := zlib.NewReader(bytes.NewReader(compressedHeader))
	if err != nil {
		return nil, fmt.Errorf("failed to create zlib reader: %w", err)
	}
	defer zlibReader.Close()

	decompressedHeader, err := io.ReadAll(zlibReader)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress header: %w", err)
	}

	// Parse the decompressed header sections
	if err := header.parseDecompressedHeader(decompressedHeader); err != nil {
		return nil, err
	}

	// Set the last thread's LumaYEnd to the block height of component 0 (luma)
	if len(header.ThreadHandoffs) > 0 && header.JpegHeader != nil {
		maxLuma := header.JpegHeader.CmpInfo[0].Bcv
		header.ThreadHandoffs[len(header.ThreadHandoffs)-1].LumaYEnd = maxLuma
	}

	return header, nil
}

// parseDecompressedHeader parses the sections in the decompressed header
func (h *LeptonHeader) parseDecompressedHeader(data []byte) error {
	pos := 0

	for pos < len(data) {
		if pos+3 > len(data) {
			break
		}

		marker := data[pos : pos+3]
		pos += 3

		switch {
		case bytes.Equal(marker, LeptonHeaderMarker[:]):
			// HDR section - raw JPEG header
			if pos+4 > len(data) {
				return ErrExitCode(ExitCodeBadLeptonFile, "HDR section too short")
			}
			size := binary.LittleEndian.Uint32(data[pos:])
			pos += 4

			if pos+int(size) > len(data) {
				return ErrExitCode(ExitCodeBadLeptonFile, "HDR data beyond end")
			}
			h.RawJpegHeader = data[pos : pos+int(size)]
			pos += int(size)

			// Parse the JPEG header and get position after SOS
			var err error
			var readIndex int
			h.JpegHeader, readIndex, err = ParseJpegHeader(h.RawJpegHeader)
			if err != nil {
				return err
			}
			h.RawJpegHeaderReadIndex = readIndex
			h.JpegHeader.JpegType = h.JpegType
			h.JpegHeader.Use16BitDCEstimate = h.Use16BitDCEstimate
			h.JpegHeader.Use16BitAdvPredict = h.Use16BitAdvPredict

		case marker[0] == LeptonHeaderLumaSplitMarker[0] && marker[1] == LeptonHeaderLumaSplitMarker[1]:
			// HH section - thread handoffs
			// Third byte of marker is the number of threads
			numThreads := int(marker[2])

			handoffs, n, err := parseThreadHandoffs(data[pos:], numThreads)
			if err != nil {
				return err
			}
			pos += n

			h.ThreadHandoffs = append(h.ThreadHandoffs, handoffs...)

		case bytes.Equal(marker, LeptonHeaderPadMarker[:]):
			// P0D section - pad bit
			if pos >= len(data) {
				return ErrExitCode(ExitCodeBadLeptonFile, "P0D section too short")
			}
			padBit := data[pos]
			pos++
			h.RecoveryInfo.PadBit = &padBit

		case bytes.Equal(marker, LeptonHeaderGarbageMarker[:]):
			// GRB section - garbage data after EOI
			if pos+4 > len(data) {
				return ErrExitCode(ExitCodeBadLeptonFile, "GRB section too short")
			}
			size := binary.LittleEndian.Uint32(data[pos:])
			pos += 4

			if pos+int(size) > len(data) {
				return ErrExitCode(ExitCodeBadLeptonFile, "GRB data beyond end")
			}
			h.RecoveryInfo.GarbageData = data[pos : pos+int(size)]
			pos += int(size)

		case bytes.Equal(marker, LeptonHeaderPrefixGarbageMarker[:]):
			// PGR section - prefix garbage before SOI
			if pos+4 > len(data) {
				return ErrExitCode(ExitCodeBadLeptonFile, "PGR section too short")
			}
			size := binary.LittleEndian.Uint32(data[pos:])
			pos += 4

			if pos+int(size) > len(data) {
				return ErrExitCode(ExitCodeBadLeptonFile, "PGR data beyond end")
			}
			h.RecoveryInfo.PrefixGarbage = data[pos : pos+int(size)]
			pos += int(size)

		case bytes.Equal(marker, LeptonHeaderJpgRestartsMarker[:]):
			// CRS section - restart marker info (array of u32 values)
			if pos+4 > len(data) {
				return ErrExitCode(ExitCodeBadLeptonFile, "CRS section too short")
			}
			count := binary.LittleEndian.Uint32(data[pos:])
			pos += 4

			h.RecoveryInfo.RestartCount = int(count)
			h.RecoveryInfo.RestartCountsSet = true
			h.RecoveryInfo.RestartCounts = make([]uint32, count)
			for i := uint32(0); i < count; i++ {
				if pos+4 > len(data) {
					return ErrExitCode(ExitCodeBadLeptonFile, "CRS data beyond end")
				}
				h.RecoveryInfo.RestartCounts[i] = binary.LittleEndian.Uint32(data[pos:])
				pos += 4
			}

		case bytes.Equal(marker, LeptonHeaderJpgRestartErrorsMarker[:]):
			// FRS section - restart error recovery (byte array)
			if pos+4 > len(data) {
				return ErrExitCode(ExitCodeBadLeptonFile, "FRS section too short")
			}
			count := binary.LittleEndian.Uint32(data[pos:])
			pos += 4

			if pos+int(count) > len(data) {
				return ErrExitCode(ExitCodeBadLeptonFile, "FRS data beyond end")
			}
			h.RecoveryInfo.RestartErrors = make([]int, count)
			for i := uint32(0); i < count; i++ {
				h.RecoveryInfo.RestartErrors[i] = int(data[pos])
				pos++
			}

		case bytes.Equal(marker, LeptonHeaderEarlyEofMarker[:]):
			// EEE section - early EOF marker (28 bytes: 7 x uint32)
			if pos+28 > len(data) {
				return ErrExitCode(ExitCodeBadLeptonFile, "EEE section too short")
			}
			h.RecoveryInfo.MaxCmp = binary.LittleEndian.Uint32(data[pos:])
			pos += 4
			h.RecoveryInfo.MaxBpos = binary.LittleEndian.Uint32(data[pos:])
			pos += 4
			h.RecoveryInfo.MaxSah = uint8(binary.LittleEndian.Uint32(data[pos:]))
			pos += 4
			h.RecoveryInfo.MaxDpos[0] = binary.LittleEndian.Uint32(data[pos:])
			pos += 4
			h.RecoveryInfo.MaxDpos[1] = binary.LittleEndian.Uint32(data[pos:])
			pos += 4
			h.RecoveryInfo.MaxDpos[2] = binary.LittleEndian.Uint32(data[pos:])
			pos += 4
			h.RecoveryInfo.MaxDpos[3] = binary.LittleEndian.Uint32(data[pos:])
			pos += 4
			h.RecoveryInfo.EarlyEofEncountered = true

		default:
			// Unknown marker - skip it if we can determine size
			// For safety, return error
			return ErrExitCode(ExitCodeBadLeptonFile,
				fmt.Sprintf("unknown header marker: %v", marker))
		}
	}

	// If no garbage data was specified, add EOI marker as default
	if len(h.RecoveryInfo.GarbageData) == 0 {
		h.RecoveryInfo.GarbageData = EOI[:]
	}

	return nil
}

// parseThreadHandoffs parses all thread handoffs from the HH section
func parseThreadHandoffs(data []byte, numThreads int) ([]ThreadHandoff, int, error) {
	// Each handoff: 2 (luma_y_start) + 4 (segment_size) + 1 (overhang) + 1 (num_bits) + 8 (last_dc[4]) = 16 bytes
	const handoffSize = 16
	if len(data) < numThreads*handoffSize {
		return nil, 0, ErrExitCode(ExitCodeBadLeptonFile, "thread handoff data too short")
	}

	handoffs := make([]ThreadHandoff, numThreads)
	pos := 0

	for i := 0; i < numThreads; i++ {
		handoffs[i].LumaYStart = uint32(binary.LittleEndian.Uint16(data[pos:]))
		pos += 2

		handoffs[i].SegmentSize = binary.LittleEndian.Uint32(data[pos:])
		pos += 4

		handoffs[i].OverhangByte = data[pos]
		pos++

		handoffs[i].NumOverhangBits = data[pos]
		pos++

		// Read 4 i16 values for last_dc (only first 3 are used for up to 3 components)
		for j := 0; j < 4; j++ {
			handoffs[i].LastDC[j] = int16(binary.LittleEndian.Uint16(data[pos:]))
			pos += 2
		}
	}

	// Compute luma_y_end from the next handoff's luma_y_start
	for i := 1; i < numThreads; i++ {
		handoffs[i-1].LumaYEnd = handoffs[i].LumaYStart
	}
	// Last handoff's LumaYEnd will be filled in later by the caller

	return handoffs, pos, nil
}

// ParseJpegHeader parses raw JPEG header bytes into a JpegHeader struct
// Returns the header, the position after SOS marker, and any error
func ParseJpegHeader(data []byte) (*JpegHeader, int, error) {
	header := NewJpegHeader()
	header.RawHeader = data
	pos := 0

	// Look for markers and parse them
	for pos < len(data) {
		if data[pos] != 0xFF {
			pos++
			continue
		}

		if pos+1 >= len(data) {
			break
		}

		marker := data[pos+1]
		pos += 2

		switch marker {
		case MarkerSOI:
			// Start of image - nothing to parse

		case MarkerSOF0, MarkerSOF1:
			// Baseline DCT
			header.JpegType = JpegTypeSequential
			if err := parseSOF(header, data[pos:]); err != nil {
				return nil, 0, err
			}
			pos += int(binary.BigEndian.Uint16(data[pos:]))

		case MarkerSOF2:
			// Progressive DCT
			header.JpegType = JpegTypeProgressive
			if err := parseSOF(header, data[pos:]); err != nil {
				return nil, 0, err
			}
			pos += int(binary.BigEndian.Uint16(data[pos:]))

		case MarkerDQT:
			// Quantization table
			length := int(binary.BigEndian.Uint16(data[pos:]))
			if err := parseDQT(header, data[pos+2:pos+length]); err != nil {
				return nil, 0, err
			}
			pos += length

		case MarkerDHT:
			// Huffman table
			length := int(binary.BigEndian.Uint16(data[pos:]))
			if err := parseDHT(header, data[pos+2:pos+length]); err != nil {
				return nil, 0, err
			}
			pos += length

		case MarkerDRI:
			// Restart interval
			header.RestartInterval = binary.BigEndian.Uint16(data[pos+2:])
			pos += int(binary.BigEndian.Uint16(data[pos:]))

		case MarkerSOS:
			// Start of scan - parse component Huffman table mapping
			if err := parseSOS(header, data[pos:]); err != nil {
				return nil, 0, err
			}
			// Return position after SOS segment
			sosLength := int(binary.BigEndian.Uint16(data[pos:]))
			pos += sosLength
			return header, pos, nil

		default:
			// Skip unknown markers
			if pos+2 <= len(data) {
				length := int(binary.BigEndian.Uint16(data[pos:]))
				pos += length
			}
		}
	}

	return header, pos, nil
}

// parseSOF parses a Start Of Frame marker
func parseSOF(header *JpegHeader, data []byte) error {
	if len(data) < 8 {
		return ErrExitCode(ExitCodeBadLeptonFile, "SOF too short")
	}

	// Skip length (2 bytes) and precision (1 byte)
	header.Height = uint32(binary.BigEndian.Uint16(data[3:5]))
	header.Width = uint32(binary.BigEndian.Uint16(data[5:7]))
	header.Cmpc = int(data[7])

	if header.Cmpc > MaxComponents {
		return ErrExitCode(ExitCodeUnsupported4Colors, "too many components")
	}

	// Parse component info
	pos := 8
	header.MaxSfh = 1
	header.MaxSfv = 1

	for i := 0; i < header.Cmpc; i++ {
		if pos+3 > len(data) {
			return ErrExitCode(ExitCodeBadLeptonFile, "SOF component data too short")
		}

		header.CmpInfo[i].Jid = data[pos]
		samplingFactor := data[pos+1]
		header.CmpInfo[i].QTableIndex = data[pos+2]

		header.CmpInfo[i].Sfh = uint32((samplingFactor >> 4) & 0x0F)
		header.CmpInfo[i].Sfv = uint32(samplingFactor & 0x0F)

		if header.CmpInfo[i].Sfh > header.MaxSfh {
			header.MaxSfh = header.CmpInfo[i].Sfh
		}
		if header.CmpInfo[i].Sfv > header.MaxSfv {
			header.MaxSfv = header.CmpInfo[i].Sfv
		}

		pos += 3
	}

	// Calculate MCU dimensions
	header.McuWidth = header.MaxSfh * 8
	header.McuHeight = header.MaxSfv * 8
	header.Mcuh = (header.Width + header.McuWidth - 1) / header.McuWidth
	header.Mcuv = (header.Height + header.McuHeight - 1) / header.McuHeight

	// Calculate block counts for each component
	for i := 0; i < header.Cmpc; i++ {
		ci := &header.CmpInfo[i]
		ci.Mbs = ci.Sfh * ci.Sfv

		ci.Bch = header.Mcuh * ci.Sfh
		ci.Bcv = header.Mcuv * ci.Sfv
		ci.Bc = ci.Bch * ci.Bcv

		ci.Nch = (header.Width*ci.Sfh + header.MaxSfh*8 - 1) / (header.MaxSfh * 8)
		ci.Ncv = (header.Height*ci.Sfv + header.MaxSfv*8 - 1) / (header.MaxSfv * 8)
		ci.Nc = ci.Nch * ci.Ncv

		// Set statistical identity (for model selection)
		if i == 0 || (ci.Sfh == header.CmpInfo[0].Sfh && ci.Sfv == header.CmpInfo[0].Sfv) {
			ci.Sid = 0
		} else {
			ci.Sid = 1
		}
	}

	return nil
}

// parseDQT parses a Define Quantization Table marker
func parseDQT(header *JpegHeader, data []byte) error {
	pos := 0
	for pos < len(data) {
		info := data[pos]
		pos++

		tableIdx := int(info & 0x0F)
		precision := (info >> 4) & 0x0F

		if tableIdx >= 4 {
			return ErrExitCode(ExitCodeBadLeptonFile, "invalid quantization table index")
		}

		if precision == 0 {
			// 8-bit values
			if pos+64 > len(data) {
				return ErrExitCode(ExitCodeBadLeptonFile, "DQT too short")
			}
			for i := 0; i < 64; i++ {
				value := data[pos+i]
				header.QTables[tableIdx][i] = uint16(value)
				if value == 0 {
					break
				}
			}
			pos += 64
		} else {
			// 16-bit values
			if pos+128 > len(data) {
				return ErrExitCode(ExitCodeBadLeptonFile, "DQT too short")
			}
			for i := 0; i < 64; i++ {
				value := binary.BigEndian.Uint16(data[pos+i*2:])
				header.QTables[tableIdx][i] = value
				if value == 0 {
					break
				}
			}
			pos += 128
		}
	}

	return nil
}

// parseDHT parses a Define Huffman Table marker
func parseDHT(header *JpegHeader, data []byte) error {
	pos := 0
	for pos < len(data) {
		info := data[pos]
		pos++

		tableIdx := int(info & 0x0F)
		tableClass := (info >> 4) & 0x0F // 0 = DC, 1 = AC

		if tableIdx >= 4 {
			return ErrExitCode(ExitCodeBadLeptonFile, "invalid Huffman table index")
		}

		table := NewHuffmanTable()

		// Read code counts (16 bytes)
		if pos+16 > len(data) {
			return ErrExitCode(ExitCodeBadLeptonFile, "DHT too short")
		}
		totalSymbols := 0
		for i := 1; i <= 16; i++ {
			table.NumCodes[i] = data[pos+i-1]
			totalSymbols += int(table.NumCodes[i])
		}
		pos += 16

		// Read symbols
		if pos+totalSymbols > len(data) {
			return ErrExitCode(ExitCodeBadLeptonFile, "DHT symbols too short")
		}
		for i := 0; i < totalSymbols; i++ {
			table.Symbols[i] = data[pos+i]
		}
		table.SymbolCount = totalSymbols
		pos += totalSymbols

		// Build derived tables
		table.BuildDerivedTable()

		// Store in header
		if tableClass == 0 {
			header.HuffDC[tableIdx] = table
		} else {
			header.HuffAC[tableIdx] = table
		}
	}

	return nil
}

// parseSOS parses a Start Of Scan marker to get component Huffman table mappings
func parseSOS(header *JpegHeader, data []byte) error {
	if len(data) < 3 {
		return ErrExitCode(ExitCodeBadLeptonFile, "SOS too short")
	}

	// Skip length (2 bytes)
	numComponents := int(data[2])

	// SOS structure: 2 bytes length + 1 byte numComponents + (2 bytes per component) + 3 bytes scan params
	expectedLen := 3 + numComponents*2 + 3
	if len(data) < expectedLen {
		return ErrExitCode(ExitCodeBadLeptonFile, "SOS component data too short")
	}

	pos := 3
	header.ScanComponentOrder = make([]int, numComponents)

	for i := 0; i < numComponents; i++ {
		compId := data[pos]
		huffTable := data[pos+1]
		pos += 2

		dcTable := (huffTable >> 4) & 0x0F
		acTable := huffTable & 0x0F

		// Find the component with matching Jid and set its Huffman tables
		for j := 0; j < header.Cmpc; j++ {
			if header.CmpInfo[j].Jid == compId {
				header.CmpInfo[j].HuffDC = dcTable
				header.CmpInfo[j].HuffAC = acTable
				header.ScanComponentOrder[i] = j
				break
			}
		}
	}

	// Parse progressive scan parameters (Ss, Se, Ah, Al)
	header.CsFrom = data[pos]         // Spectral selection start (Ss)
	header.CsTo = data[pos+1]         // Spectral selection end (Se)
	header.CsSah = data[pos+2] >> 4   // Successive approximation high (Ah)
	header.CsSal = data[pos+2] & 0x0F // Successive approximation low (Al)

	return nil
}
