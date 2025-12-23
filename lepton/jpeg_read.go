package lepton

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
)

// JpegReadResult contains the result of reading a JPEG file
type JpegReadResult struct {
	ImageData              []*BlockBasedImage
	Header                 *JpegHeader
	RawHeader              []byte
	GarbageData            []byte
	Partitions             []JpegPartition
	MaxDPos                [MaxComponents]uint32
	EarlyEOF               bool
	PadBit                 *uint8
	remainingFromBitReader []byte // unexported: bytes left in BitReader's buffer after scan
}

// JpegPartition contains information about a partition in the JPEG scan
type JpegPartition struct {
	Position        int64
	OverhangByte    uint8
	NumOverhangBits uint8
	LastDC          [MaxComponents]int16
	LumaYStart      uint32
	LumaYEnd        uint32
}

// ReadJpegFile reads a JPEG file and extracts DCT coefficients
func ReadJpegFile(reader io.Reader) (*JpegReadResult, error) {
	// Buffer the reader for efficient reading
	bufReader := bufio.NewReader(reader)

	// Read SOI marker
	header := make([]byte, 2)
	if _, err := io.ReadFull(bufReader, header); err != nil {
		return nil, fmt.Errorf("failed to read JPEG header: %w", err)
	}
	if header[0] != 0xFF || header[1] != MarkerSOI {
		return nil, NewLeptonError(ExitCodeUnsupportedJpeg, "JPEG must start with 0xFF 0xD8")
	}

	rawHeader := make([]byte, 0, 4096)
	rawHeader = append(rawHeader, header...)

	// Parse JPEG header
	jpegHeader, headerBytes, err := parseJpegHeaderFull(bufReader)
	if err != nil {
		return nil, err
	}
	rawHeader = append(rawHeader, headerBytes...)

	if jpegHeader.Cmpc > ColorChannelNumBlockTypes {
		return nil, NewLeptonError(ExitCodeUnsupported4Colors, "doesn't support 4 color channels")
	}

	// Create block-based images for each component
	imageData := make([]*BlockBasedImage, jpegHeader.Cmpc)
	for i := 0; i < jpegHeader.Cmpc; i++ {
		ci := &jpegHeader.CmpInfo[i]
		luma := &jpegHeader.CmpInfo[0]
		imageData[i] = NewBlockBasedImage(ci, luma)
	}

	result := &JpegReadResult{
		ImageData: imageData,
		Header:    jpegHeader,
		RawHeader: rawHeader,
	}

	if jpegHeader.JpegType == JpegTypeSequential {
		// Read the single baseline scan
		if err := readBaselineScan(bufReader, jpegHeader, result); err != nil {
			return nil, err
		}

		// Read any remaining data as garbage
		var garbage []byte
		if len(result.remainingFromBitReader) > 0 {
			garbage = append(garbage, result.remainingFromBitReader...)
		}
		remaining, err := io.ReadAll(bufReader)
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("failed to read garbage data: %w", err)
		}
		garbage = append(garbage, remaining...)
		result.GarbageData = garbage
	} else {
		// Progressive JPEG - read multiple scans
		if err := readProgressiveScans(bufReader, jpegHeader, result); err != nil {
			return nil, err
		}
	}

	return result, nil
}

// parseJpegHeaderFull parses the JPEG header segments until SOS
func parseJpegHeaderFull(reader *bufio.Reader) (*JpegHeader, []byte, error) {
	header := NewJpegHeader()
	rawBytes := make([]byte, 0, 2048)

	for {
		// Read marker
		marker := make([]byte, 2)
		if _, err := io.ReadFull(reader, marker); err != nil {
			return nil, nil, fmt.Errorf("failed to read marker: %w", err)
		}
		rawBytes = append(rawBytes, marker...)

		if marker[0] != 0xFF {
			return nil, nil, NewLeptonError(ExitCodeUnsupportedJpeg, "invalid marker")
		}

		markerType := marker[1]

		// Check for markers without length
		if markerType == MarkerEOI {
			return nil, nil, NewLeptonError(ExitCodeUnsupportedJpeg, "unexpected EOI marker")
		}

		// Read segment length
		lenBytes := make([]byte, 2)
		if _, err := io.ReadFull(reader, lenBytes); err != nil {
			return nil, nil, fmt.Errorf("failed to read segment length: %w", err)
		}
		rawBytes = append(rawBytes, lenBytes...)

		segmentLen := int(lenBytes[0])<<8 | int(lenBytes[1])
		if segmentLen < 2 {
			return nil, nil, NewLeptonError(ExitCodeUnsupportedJpeg, "segment too short")
		}

		// Read segment data
		segmentData := make([]byte, segmentLen-2)
		if _, err := io.ReadFull(reader, segmentData); err != nil {
			return nil, nil, fmt.Errorf("failed to read segment data: %w", err)
		}
		rawBytes = append(rawBytes, segmentData...)

		// Parse segment based on type
		switch markerType {
		case MarkerSOF0, MarkerSOF1:
			// Baseline or Extended Sequential DCT
			if err := parseSOFRead(header, segmentData, JpegTypeSequential); err != nil {
				return nil, nil, err
			}
		case MarkerSOF2:
			// Progressive DCT
			if err := parseSOFRead(header, segmentData, JpegTypeProgressive); err != nil {
				return nil, nil, err
			}
		case MarkerDHT:
			if err := parseDHTRead(header, segmentData); err != nil {
				return nil, nil, err
			}
		case MarkerDQT:
			if err := parseDQTRead(header, segmentData); err != nil {
				return nil, nil, err
			}
		case MarkerDRI:
			if len(segmentData) >= 2 {
				header.RestartInterval = uint16(segmentData[0])<<8 | uint16(segmentData[1])
			}
		case MarkerSOS:
			if err := parseSOSRead(header, segmentData); err != nil {
				return nil, nil, err
			}
			// SOS means we're done with headers
			return header, rawBytes, nil
		default:
			// Skip unknown/APP segments
		}
	}
}

// parseSOFRead parses Start Of Frame segment during JPEG reading
func parseSOFRead(header *JpegHeader, data []byte, jpegType JpegType) error {
	if len(data) < 6 {
		return NewLeptonError(ExitCodeUnsupportedJpeg, "SOF segment too short")
	}

	if header.JpegType != JpegTypeUnknown {
		return NewLeptonError(ExitCodeUnsupportedJpeg, "multiple SOF markers")
	}

	header.JpegType = jpegType

	precision := data[0]
	if precision != 8 {
		return NewLeptonError(ExitCodeUnsupportedJpeg,
			fmt.Sprintf("%d bit precision not supported", precision))
	}

	header.Height = uint32(data[1])<<8 | uint32(data[2])
	header.Width = uint32(data[3])<<8 | uint32(data[4])
	header.Cmpc = int(data[5])

	if header.Height == 0 || header.Width == 0 {
		return NewLeptonError(ExitCodeUnsupportedJpeg, "image dimensions cannot be zero")
	}

	if header.Cmpc > 4 {
		return NewLeptonError(ExitCodeUnsupportedJpeg,
			fmt.Sprintf("image has %d components, max 4 supported", header.Cmpc))
	}

	pos := 6
	for cmp := 0; cmp < header.Cmpc; cmp++ {
		if pos+3 > len(data) {
			return NewLeptonError(ExitCodeUnsupportedJpeg, "SOF segment too short for components")
		}

		header.CmpInfo[cmp].Jid = data[pos]
		header.CmpInfo[cmp].Sfv = uint32(data[pos+1] >> 4)
		header.CmpInfo[cmp].Sfh = uint32(data[pos+1] & 0x0F)

		if header.CmpInfo[cmp].Sfv > 2 || header.CmpInfo[cmp].Sfh > 2 {
			return NewLeptonError(ExitCodeSamplingBeyondTwoUnsupported,
				"sampling factor beyond 2 not supported")
		}

		qTableIdx := data[pos+2]
		if qTableIdx >= 4 {
			return NewLeptonError(ExitCodeUnsupportedJpeg, "quantization table index too big")
		}
		header.CmpInfo[cmp].QTableIndex = qTableIdx

		pos += 3
	}

	// Calculate MCU dimensions
	header.MaxSfh = 1
	header.MaxSfv = 1
	for cmp := 0; cmp < header.Cmpc; cmp++ {
		if header.CmpInfo[cmp].Sfh > header.MaxSfh {
			header.MaxSfh = header.CmpInfo[cmp].Sfh
		}
		if header.CmpInfo[cmp].Sfv > header.MaxSfv {
			header.MaxSfv = header.CmpInfo[cmp].Sfv
		}
	}

	header.McuHeight = header.MaxSfh * 8
	header.McuWidth = header.MaxSfv * 8
	header.Mcuv = (header.Height + header.McuHeight - 1) / header.McuHeight
	header.Mcuh = (header.Width + header.McuWidth - 1) / header.McuWidth

	// Calculate component block counts
	for cmp := 0; cmp < header.Cmpc; cmp++ {
		ci := &header.CmpInfo[cmp]
		ci.Mbs = ci.Sfv * ci.Sfh
		ci.Bcv = header.Mcuv * ci.Sfh
		ci.Bch = header.Mcuh * ci.Sfv
		ci.Bc = ci.Bcv * ci.Bch
		ci.Ncv = ((header.Height * ci.Sfh) + (header.MaxSfh*8 - 1)) / (header.MaxSfh * 8)
		ci.Nch = ((header.Width * ci.Sfv) + (header.MaxSfv*8 - 1)) / (header.MaxSfv * 8)
		ci.Nc = ci.Ncv * ci.Nch
		if cmp < 3 {
			ci.Sid = uint32(cmp)
		}
	}

	return nil
}

// parseDHTRead parses Define Huffman Table segment during JPEG reading
func parseDHTRead(header *JpegHeader, data []byte) error {
	pos := 0
	for pos < len(data) {
		if pos >= len(data) {
			break
		}

		tableClass := (data[pos] >> 4) & 0x0F // 0=DC, 1=AC
		tableID := data[pos] & 0x0F
		pos++

		if tableClass > 1 || tableID > 3 {
			return NewLeptonError(ExitCodeUnsupportedJpeg, "invalid Huffman table index")
		}

		// Read code counts
		if pos+16 > len(data) {
			return NewLeptonError(ExitCodeUnsupportedJpeg, "DHT segment too short")
		}

		ht := NewHuffmanTable()
		totalSymbols := 0
		for i := 1; i <= 16; i++ {
			ht.NumCodes[i] = data[pos+i-1]
			totalSymbols += int(ht.NumCodes[i])
		}
		pos += 16

		// Read symbols
		if pos+totalSymbols > len(data) {
			return NewLeptonError(ExitCodeUnsupportedJpeg, "DHT segment too short for symbols")
		}

		for i := 0; i < totalSymbols; i++ {
			ht.Symbols[i] = data[pos+i]
		}
		pos += totalSymbols

		ht.BuildDerivedTable()

		if tableClass == 0 {
			header.HuffDC[tableID] = ht
		} else {
			header.HuffAC[tableID] = ht
		}
	}

	return nil
}

// parseDQTRead parses Define Quantization Table segment during JPEG reading
func parseDQTRead(header *JpegHeader, data []byte) error {
	pos := 0
	for pos < len(data) {
		precision := (data[pos] >> 4) & 0x0F
		tableID := data[pos] & 0x0F
		pos++

		if tableID > 3 {
			return NewLeptonError(ExitCodeUnsupportedJpeg, "invalid quantization table index")
		}

		if precision == 0 {
			// 8-bit precision
			if pos+64 > len(data) {
				return NewLeptonError(ExitCodeUnsupportedJpeg, "DQT segment too short")
			}
			for i := 0; i < 64; i++ {
				header.QTables[tableID][i] = uint16(data[pos+i])
			}
			pos += 64
		} else {
			// 16-bit precision
			if pos+128 > len(data) {
				return NewLeptonError(ExitCodeUnsupportedJpeg, "DQT segment too short")
			}
			for i := 0; i < 64; i++ {
				header.QTables[tableID][i] = uint16(data[pos+i*2])<<8 | uint16(data[pos+i*2+1])
			}
			pos += 128
		}
	}

	return nil
}

// parseSOSRead parses Start Of Scan segment during JPEG reading
func parseSOSRead(header *JpegHeader, data []byte) error {
	if len(data) < 1 {
		return NewLeptonError(ExitCodeUnsupportedJpeg, "SOS segment too short")
	}

	numComponents := int(data[0])
	if numComponents == 0 {
		return NewLeptonError(ExitCodeUnsupportedJpeg, "zero components in scan")
	}
	if numComponents > header.Cmpc {
		return NewLeptonError(ExitCodeUnsupportedJpeg, "too many components in scan")
	}

	header.ScanComponentOrder = make([]int, numComponents)

	pos := 1
	for i := 0; i < numComponents; i++ {
		if pos+2 > len(data) {
			return NewLeptonError(ExitCodeUnsupportedJpeg, "SOS segment too short for components")
		}

		componentID := data[pos]
		// Find component by ID
		cmpIdx := -1
		for j := 0; j < header.Cmpc; j++ {
			if header.CmpInfo[j].Jid == componentID {
				cmpIdx = j
				break
			}
		}
		if cmpIdx < 0 {
			return NewLeptonError(ExitCodeUnsupportedJpeg, "component ID mismatch in SOS")
		}

		header.ScanComponentOrder[i] = cmpIdx
		header.CmpInfo[cmpIdx].HuffDC = (data[pos+1] >> 4) & 0x0F
		header.CmpInfo[cmpIdx].HuffAC = data[pos+1] & 0x0F

		pos += 2
	}

	if pos+3 > len(data) {
		return NewLeptonError(ExitCodeUnsupportedJpeg, "SOS segment too short for spectral selection")
	}

	header.CsFrom = data[pos]
	header.CsTo = data[pos+1]
	header.CsSah = (data[pos+2] >> 4) & 0x0F
	header.CsSal = data[pos+2] & 0x0F

	return nil
}

// readBaselineScan reads baseline JPEG scan data
func readBaselineScan(reader *bufio.Reader, header *JpegHeader, result *JpegReadResult) error {
	bitReader := NewBitReader(reader)

	state := NewJpegPositionState(header, 0)

	lastDC := [MaxComponents]int16{}
	doHandoff := true

	for {
		state.ResetRstw(header)

		for {
			// Collect partition info at MCU row boundaries
			if doHandoff {
				overhangBits, overhangByte := bitReader.Overhang()
				mcuY := state.GetMcu() / header.Mcuh
				lumaMul := header.CmpInfo[0].Bcv / header.Mcuv

				result.Partitions = append(result.Partitions, JpegPartition{
					Position:        bitReader.StreamPosition(),
					OverhangByte:    overhangByte,
					NumOverhangBits: overhangBits,
					LastDC:          lastDC,
					LumaYStart:      lumaMul * mcuY,
					LumaYEnd:        lumaMul * (mcuY + 1),
				})
				doHandoff = false
			}

			if !bitReader.IsEOF() {
				cmp := state.GetCmp()
				if state.GetDpos() > result.MaxDPos[cmp] {
					result.MaxDPos[cmp] = state.GetDpos()
				}
			}

			// Decode block
			block, eob, err := decodeBlockSeq(bitReader, header, state.GetCmp())
			if err != nil {
				return err
			}

			if eob > 1 && block[eob-1] == 0 {
				return NewLeptonError(ExitCodeUnsupportedJpeg, "cannot encode image with eob after last 0")
			}

			// Apply DC prediction
			cmp := state.GetCmp()
			block[0] = block[0] + lastDC[cmp]
			lastDC[cmp] = block[0]

			// Convert to transposed form and store
			blockTr := zigzagToTransposed(block)
			result.ImageData[cmp].SetBlockByDpos(state.GetDpos(), blockTr)

			// Move to next position
			oldMcu := state.GetMcu()
			sta := state.NextMcuPos(header)

			// Check for handoff at MCU row boundaries
			if state.GetMcu()%header.Mcuh == 0 && oldMcu != state.GetMcu() {
				doHandoff = true
			}

			// Check for EOF
			if bitReader.IsEOF() {
				result.EarlyEOF = true
				return nil
			}

			if sta == ScanCompleted {
				// Capture final padding bits if not already set
				if result.PadBit == nil {
					var padBitSet bool
					var padBit uint8
					if err := bitReader.ReadAndVerifyFillBits(&padBit, &padBitSet); err != nil {
						return err
					}
					if padBitSet {
						result.PadBit = &padBit
					}
				}
				// Store remaining buffer for garbage data collection
				result.remainingFromBitReader = bitReader.RemainingBuffer()
				return nil
			}

			if sta == RestartIntervalExpired {
				// Verify and consume fill bits
				var padBitSet bool
				var padBit uint8
				if result.PadBit != nil {
					padBit = *result.PadBit
					padBitSet = true
				}
				if err := bitReader.ReadAndVerifyFillBits(&padBit, &padBitSet); err != nil {
					return err
				}
				if padBitSet && result.PadBit == nil {
					result.PadBit = &padBit
				}

				// Verify reset code
				if err := bitReader.VerifyResetCode(); err != nil {
					return err
				}

				// Reset DC values
				lastDC = [MaxComponents]int16{}
				break
			}
		}
	}
}

// decodeBlockSeq decodes a sequential baseline block
func decodeBlockSeq(bitReader *BitReader, header *JpegHeader, cmp int) ([64]int16, int, error) {
	var block [64]int16
	eob := 64

	dcTable := header.HuffDC[header.CmpInfo[cmp].HuffDC]
	acTable := header.HuffAC[header.CmpInfo[cmp].HuffAC]

	if dcTable == nil || acTable == nil {
		return block, eob, NewLeptonError(ExitCodeUnsupportedJpeg, "missing Huffman table")
	}

	// Decode DC coefficient
	dcCoef, err := readDC(bitReader, dcTable)
	if err != nil {
		return block, eob, err
	}
	block[0] = dcCoef

	// Decode AC coefficients
	pos := 1
	for pos < 64 {
		z, coef, isEOB, err := readACCoef(bitReader, acTable)
		if err != nil {
			return block, eob, err
		}

		if isEOB {
			eob = pos
			break
		}

		if z+pos >= 64 {
			if !bitReader.IsEOF() {
				return block, eob, NewLeptonError(ExitCodeUnsupportedJpeg,
					"run length exceeds block boundary")
			}
			// Handle truncated file
			break
		}

		pos += z
		block[pos] = coef
		pos++
	}

	return block, eob, nil
}

// readDC reads a DC coefficient
func readDC(bitReader *BitReader, table *HuffmanTable) (int16, error) {
	code, err := nextHuffCode(bitReader, table)
	if err != nil {
		return 0, err
	}

	if code == 0 {
		return 0, nil
	}

	bits, err := bitReader.Read(uint32(code))
	if err != nil {
		return 0, err
	}

	return decodeVLI(code, bits), nil
}

// readACCoef reads an AC coefficient
func readACCoef(bitReader *BitReader, table *HuffmanTable) (int, int16, bool, error) {
	code, err := nextHuffCode(bitReader, table)
	if err != nil {
		return 0, 0, false, err
	}

	if code == 0 {
		// EOB
		return 0, 0, true, nil
	}

	z := int(code >> 4)   // run length
	s := int(code & 0x0F) // coefficient size

	if s == 0 {
		// ZRL (zero run length of 16)
		return z, 0, false, nil
	}

	bits, err := bitReader.Read(uint32(s))
	if err != nil {
		return 0, 0, false, err
	}

	return z, decodeVLI(uint8(s), bits), false, nil
}

// nextHuffCode reads the next Huffman code
func nextHuffCode(bitReader *BitReader, table *HuffmanTable) (uint8, error) {
	// Try fast lookup first
	peek, peekLen := bitReader.Peek()
	if peekLen >= 8 {
		lookup := table.FastLookup[peek]
		if lookup >= 0 {
			codeLen := uint32(lookup >> 8)
			bitReader.Advance(codeLen)
			return uint8(lookup & 0xFF), nil
		}
	}

	// Slow path - read bit by bit
	code := 0
	for bits := 1; bits <= 16; bits++ {
		bit, err := bitReader.Read(1)
		if err != nil {
			return 0, err
		}
		code = (code << 1) | int(bit)

		if code <= int(table.MaxCode[bits]) {
			idx := table.ValPtr[bits] + int32(code)
			return table.Symbols[idx], nil
		}
	}

	return 0, NewLeptonError(ExitCodeUnsupportedJpeg, "invalid Huffman code")
}

// decodeVLI decodes a variable length integer
func decodeVLI(size uint8, bits uint16) int16 {
	if size == 0 {
		return 0
	}
	// If MSB is 0, value is negative
	if bits < (1 << (size - 1)) {
		return int16(bits) - int16((1<<size)-1)
	}
	return int16(bits)
}

// zigzagToTransposed converts zigzag order to transposed order
func zigzagToTransposed(block [64]int16) AlignedBlock {
	var result AlignedBlock
	for i := 0; i < 64; i++ {
		result.RawData[ZigzagToTransposed[i]] = block[i]
	}
	return result
}

// ReadJpegBytes is a convenience function that reads from a byte slice
func ReadJpegBytes(data []byte) (*JpegReadResult, error) {
	return ReadJpegFile(bytes.NewReader(data))
}

// readProgressiveScans reads all progressive scans from a JPEG file
func readProgressiveScans(reader *bufio.Reader, header *JpegHeader, result *JpegReadResult) error {
	// Read first scan (DC first stage for all components)
	scanReader, err := readProgressiveFirstScan(reader, header, result)
	if err != nil {
		return err
	}

	// Read subsequent scans until we hit EOI
	for {
		// Create a combined reader: remaining buffer from BitReader + underlying reader
		var combinedReader *bufio.Reader
		if scanReader != nil && len(scanReader.RemainingBuffer()) > 0 {
			remaining := scanReader.RemainingBuffer()
			combinedReader = bufio.NewReader(io.MultiReader(bytes.NewReader(remaining), reader))
		} else {
			combinedReader = reader
		}

		// Try to parse the next scan header
		moreScans, headerBytes, err := parseNextScanHeader(combinedReader, header)
		if err != nil {
			return err
		}

		// Accumulate header bytes
		result.RawHeader = append(result.RawHeader, headerBytes...)

		if !moreScans {
			// We hit EOI, done reading scans
			// EOI is included in headerBytes, treat as garbage since we'll reconstruct it
			result.GarbageData = []byte{0xFF, MarkerEOI}

			// Read any remaining data after EOI as additional garbage
			remaining, err := io.ReadAll(combinedReader)
			if err != nil && err != io.EOF {
				return fmt.Errorf("failed to read garbage data: %w", err)
			}
			if len(remaining) > 0 {
				result.GarbageData = append(result.GarbageData, remaining...)
			}
			return nil
		}

		// Read this scan
		scanReader, err = readProgressiveScanWithReader(combinedReader, header, result)
		if err != nil {
			return err
		}
	}
}

// parseNextScanHeader parses headers until the next SOS or EOI marker
// Returns true if more scans to read, false if EOI was encountered
func parseNextScanHeader(reader *bufio.Reader, header *JpegHeader) (bool, []byte, error) {
	rawBytes := make([]byte, 0, 256)

	for {
		// Read marker
		marker := make([]byte, 2)
		if _, err := io.ReadFull(reader, marker); err != nil {
			return false, nil, fmt.Errorf("failed to read marker: %w", err)
		}
		rawBytes = append(rawBytes, marker...)

		if marker[0] != 0xFF {
			return false, nil, NewLeptonError(ExitCodeUnsupportedJpeg, "invalid marker in progressive scan")
		}

		markerType := marker[1]

		// Check for EOI - end of image
		if markerType == MarkerEOI {
			// Remove EOI from raw bytes since it will be in garbage data
			rawBytes = rawBytes[:len(rawBytes)-2]
			return false, rawBytes, nil
		}

		// Read segment length
		lenBytes := make([]byte, 2)
		if _, err := io.ReadFull(reader, lenBytes); err != nil {
			return false, nil, fmt.Errorf("failed to read segment length: %w", err)
		}
		rawBytes = append(rawBytes, lenBytes...)

		segmentLen := int(lenBytes[0])<<8 | int(lenBytes[1])
		if segmentLen < 2 {
			return false, nil, NewLeptonError(ExitCodeUnsupportedJpeg, "segment too short")
		}

		// Read segment data
		segmentData := make([]byte, segmentLen-2)
		if _, err := io.ReadFull(reader, segmentData); err != nil {
			return false, nil, fmt.Errorf("failed to read segment data: %w", err)
		}
		rawBytes = append(rawBytes, segmentData...)

		// Parse segment based on type
		switch markerType {
		case MarkerDHT:
			if err := parseDHTRead(header, segmentData); err != nil {
				return false, nil, err
			}
		case MarkerDQT:
			if err := parseDQTRead(header, segmentData); err != nil {
				return false, nil, err
			}
		case MarkerDRI:
			if len(segmentData) >= 2 {
				header.RestartInterval = uint16(segmentData[0])<<8 | uint16(segmentData[1])
			}
		case MarkerSOS:
			if err := parseSOSRead(header, segmentData); err != nil {
				return false, nil, err
			}
			// SOS means we have another scan to read
			return true, rawBytes, nil
		default:
			// Skip unknown/APP segments between scans
		}
	}
}

// readProgressiveFirstScan reads the first progressive scan (typically DC first stage)
// Returns the BitReader so we can get any remaining buffered data
func readProgressiveFirstScan(reader *bufio.Reader, header *JpegHeader, result *JpegReadResult) (*BitReader, error) {
	bitReader := NewBitReader(reader)
	state := NewJpegPositionState(header, 0)
	doHandoff := true

	// First scan should be DC only (Ss=0, Se=0)
	if header.CsTo == 0 && header.CsSah == 0 {
		// DC first stage - verify Huffman tables
		for _, cmpIdx := range header.ScanComponentOrder {
			if header.HuffDC[header.CmpInfo[cmpIdx].HuffDC] == nil {
				return nil, NewLeptonError(ExitCodeUnsupportedJpeg, "missing DC Huffman table for progressive scan")
			}
		}

		lastDC := [MaxComponents]int16{}
		sta := DecodeInProgress

		for sta != ScanCompleted {
			state.ResetRstw(header)

			for sta == DecodeInProgress {
				// Collect partition info at MCU row boundaries
				if doHandoff {
					overhangBits, overhangByte := bitReader.Overhang()
					mcuY := state.GetMcu() / header.Mcuh
					lumaMul := header.CmpInfo[0].Bcv / header.Mcuv

					result.Partitions = append(result.Partitions, JpegPartition{
						Position:        bitReader.StreamPosition(),
						OverhangByte:    overhangByte,
						NumOverhangBits: overhangBits,
						LastDC:          lastDC,
						LumaYStart:      lumaMul * mcuY,
						LumaYEnd:        lumaMul * (mcuY + 1),
					})
					doHandoff = false
				}

				// Decode DC coefficient
				cmp := state.GetCmp()
				dcTable := header.HuffDC[header.CmpInfo[cmp].HuffDC]
				dcCoef, err := readDC(bitReader, dcTable)
				if err != nil {
					return nil, err
				}

				v := dcCoef + lastDC[cmp]
				lastDC[cmp] = v

				// Store with successive approximation shift
				block := result.ImageData[cmp].EnsureBlock(state.GetDpos())
				block.SetTransposedFromZigzag(0, v<<header.CsSal)

				// Move to next position
				oldMcu := state.GetMcu()
				sta = state.NextMcuPos(header)

				if state.GetMcu()%header.Mcuh == 0 && oldMcu != state.GetMcu() {
					doHandoff = true
				}
			}

			// Verify fill bits at end of restart interval or scan
			var padBitSet bool
			var padBit uint8
			if result.PadBit != nil {
				padBit = *result.PadBit
				padBitSet = true
			}
			if err := bitReader.ReadAndVerifyFillBits(&padBit, &padBitSet); err != nil {
				return nil, err
			}
			if padBitSet && result.PadBit == nil {
				result.PadBit = &padBit
			}

			if sta == RestartIntervalExpired {
				if err := bitReader.VerifyResetCode(); err != nil {
					return nil, err
				}
				lastDC = [MaxComponents]int16{}
				sta = DecodeInProgress
			}
		}

		return bitReader, nil
	}

	return nil, NewLeptonError(ExitCodeUnsupportedJpeg, "progressive JPEG must start with DC first stage")
}

// readProgressiveScanWithReader reads a subsequent progressive scan
// Returns the BitReader so we can get any remaining buffered data
func readProgressiveScanWithReader(reader *bufio.Reader, header *JpegHeader, result *JpegReadResult) (*BitReader, error) {
	bitReader := NewBitReader(reader)
	state := NewJpegPositionState(header, 0)

	if debugACRefine {
		scanType := "?"
		if header.CsTo == 0 {
			if header.CsSah == 0 {
				scanType = "DC first"
			} else {
				scanType = "DC refine"
			}
		} else if header.CsSah == 0 {
			scanType = "AC first"
		} else {
			scanType = "AC refine"
		}
		fmt.Printf("Progressive scan: %s, CsFrom=%d, CsTo=%d, CsSah=%d, CsSal=%d, Components=%v\n",
			scanType, header.CsFrom, header.CsTo, header.CsSah, header.CsSal, header.ScanComponentOrder)
	}

	if header.CsTo == 0 {
		// DC scan
		if header.CsSah == 0 {
			return nil, NewLeptonError(ExitCodeUnsupportedJpeg, "progressive can't have two DC first stages")
		}
		// DC refinement stage
		if err := readProgressiveDCRefine(bitReader, header, result, state); err != nil {
			return nil, err
		}
		return bitReader, nil
	}

	// AC scan
	if header.CsFrom == 0 || header.CsTo >= 64 || header.CsFrom > header.CsTo {
		return nil, NewLeptonError(ExitCodeUnsupportedJpeg,
			fmt.Sprintf("progressive encoding range was invalid %d to %d", header.CsFrom, header.CsTo))
	}

	// Verify we have exactly one component for AC scans
	if len(header.ScanComponentOrder) != 1 {
		return nil, NewLeptonError(ExitCodeUnsupportedJpeg, "progressive AC encoding cannot be interleaved")
	}

	// Verify AC Huffman table exists
	cmpIdx := header.ScanComponentOrder[0]
	if header.HuffAC[header.CmpInfo[cmpIdx].HuffAC] == nil {
		return nil, NewLeptonError(ExitCodeUnsupportedJpeg, "missing AC Huffman table for progressive scan")
	}

	if header.CsSah == 0 {
		// AC first stage
		if err := readProgressiveACFirst(bitReader, header, result, state); err != nil {
			return nil, err
		}
		return bitReader, nil
	}
	// AC refinement stage
	if err := readProgressiveACRefine(bitReader, header, result, state); err != nil {
		return nil, err
	}
	return bitReader, nil
}

// readProgressiveDCRefine reads a DC refinement scan
func readProgressiveDCRefine(bitReader *BitReader, header *JpegHeader, result *JpegReadResult, state *JpegPositionState) error {
	sta := DecodeInProgress

	for sta != ScanCompleted {
		state.ResetRstw(header)

		for sta == DecodeInProgress {
			cmp := state.GetCmp()
			block := result.ImageData[cmp].EnsureBlock(state.GetDpos())

			// Read one bit for refinement
			bit, err := bitReader.Read(1)
			if err != nil {
				return err
			}

			// Add the refinement bit
			current := block.GetTransposedFromZigzag(0)
			block.SetTransposedFromZigzag(0, current+(int16(bit)<<header.CsSal))

			sta = state.NextMcuPos(header)
		}

		// Verify fill bits
		var padBitSet bool
		var padBit uint8
		if result.PadBit != nil {
			padBit = *result.PadBit
			padBitSet = true
		}
		if err := bitReader.ReadAndVerifyFillBits(&padBit, &padBitSet); err != nil {
			return err
		}
		if padBitSet && result.PadBit == nil {
			result.PadBit = &padBit
		}

		if sta == RestartIntervalExpired {
			if err := bitReader.VerifyResetCode(); err != nil {
				return err
			}
			sta = DecodeInProgress
		}
	}

	return nil
}

// readProgressiveACFirst reads an AC first stage scan
func readProgressiveACFirst(bitReader *BitReader, header *JpegHeader, result *JpegReadResult, state *JpegPositionState) error {
	cmpIdx := header.ScanComponentOrder[0]
	acTable := header.HuffAC[header.CmpInfo[cmpIdx].HuffAC]
	sta := DecodeInProgress

	for sta != ScanCompleted {
		state.ResetRstw(header)

		for sta == DecodeInProgress {
			cmp := state.GetCmp()
			block := result.ImageData[cmp].EnsureBlock(state.GetDpos())

			if state.Eobrun == 0 {
				// Decode AC coefficients for this block
				eob, err := decodeACProgressiveFirst(bitReader, acTable, block, state, header.CsFrom, header.CsTo, header.CsSal)
				if err != nil {
					return err
				}

				// Check optimal eobrun
				if err := state.CheckOptimalEobrun(eob == header.CsFrom, getMaxEobRun(acTable)); err != nil {
					return err
				}
			}

			var err error
			sta, err = state.SkipEobrun(header)
			if err != nil {
				return err
			}

			if sta == DecodeInProgress {
				sta = state.NextMcuPos(header)
			}
		}

		// Verify fill bits
		var padBitSet bool
		var padBit uint8
		if result.PadBit != nil {
			padBit = *result.PadBit
			padBitSet = true
		}
		if err := bitReader.ReadAndVerifyFillBits(&padBit, &padBitSet); err != nil {
			return err
		}
		if padBitSet && result.PadBit == nil {
			result.PadBit = &padBit
		}

		if sta == RestartIntervalExpired {
			if err := bitReader.VerifyResetCode(); err != nil {
				return err
			}
			sta = DecodeInProgress
		}
	}

	return nil
}

// decodeACProgressiveFirst decodes AC coefficients for progressive first stage
func decodeACProgressiveFirst(bitReader *BitReader, acTable *HuffmanTable, block *AlignedBlock, state *JpegPositionState, csFrom, csTo, csSal uint8) (uint8, error) {
	bpos := csFrom

	for bpos <= csTo {
		hc, err := nextHuffCode(bitReader, acTable)
		if err != nil {
			return bpos, err
		}

		l := hc >> 4       // run length
		r := hc & 0x0F     // coefficient size

		if l == 15 || r > 0 {
			// Run/level combination
			z := l
			s := r

			if z+bpos > csTo {
				return bpos, NewLeptonError(ExitCodeUnsupportedJpeg, "AC run length too long")
			}

			// Skip zeros (they're already zero)
			bpos += z

			// Read coefficient value
			bits, err := bitReader.Read(uint32(s))
			if err != nil {
				return bpos, err
			}
			coef := decodeVLI(s, bits)

			// Store with successive approximation shift
			block.SetTransposedFromZigzag(int(bpos), coef<<csSal)
			bpos++
		} else {
			// EOB run
			s := l
			n, err := bitReader.Read(uint32(s))
			if err != nil {
				return bpos, err
			}
			state.Eobrun = decodeEobrunBits(s, n)
			state.Eobrun--
			break
		}
	}

	return bpos, nil
}

// readProgressiveACRefine reads an AC refinement stage scan
func readProgressiveACRefine(bitReader *BitReader, header *JpegHeader, result *JpegReadResult, state *JpegPositionState) error {
	cmpIdx := header.ScanComponentOrder[0]
	acTable := header.HuffAC[header.CmpInfo[cmpIdx].HuffAC]
	sta := DecodeInProgress

	if debugACRefine {
		fmt.Printf("AC Refinement scan: CsFrom=%d, CsTo=%d, CsSah=%d, CsSal=%d\n",
			header.CsFrom, header.CsTo, header.CsSah, header.CsSal)
	}

	blockNum := 0
	for sta != ScanCompleted {
		state.ResetRstw(header)

		for sta == DecodeInProgress {
			cmp := state.GetCmp()
			block := result.ImageData[cmp].EnsureBlock(state.GetDpos())
			blockNum++

			// Create temp block (matching Rust's implementation)
			var tempBlock [64]int16
			for bpos := header.CsFrom; bpos <= header.CsTo; bpos++ {
				tempBlock[bpos] = block.GetTransposedFromZigzag(int(bpos))
			}

			if state.Eobrun == 0 {
				if debugACRefine && blockNum == 12 {
					fmt.Printf("=== Block %d, dpos=%d ===\n", blockNum, state.GetDpos())
					fmt.Printf("tempBlock: ")
					for i := uint8(1); i <= 63; i++ {
						if tempBlock[i] != 0 {
							fmt.Printf("[%d]=%d ", i, tempBlock[i])
						}
					}
					fmt.Println()
				}
				// Decode AC refinement for this block
				eob, err := decodeACProgressiveRefineTmp(bitReader, acTable, tempBlock[:], state, header.CsFrom, header.CsTo)
				if err != nil {
					if debugACRefine {
						fmt.Printf("Error at block %d, dpos=%d\n", blockNum, state.GetDpos())
					}
					return err
				}

				// Check optimal eobrun
				if err := state.CheckOptimalEobrun(eob == header.CsFrom, getMaxEobRun(acTable)); err != nil {
					return err
				}
			} else {
				// Decode zero block refinement (into temp block)
				if err := decodeEobrunRefineTemp(bitReader, tempBlock[:], state, header.CsFrom, header.CsTo); err != nil {
					return err
				}
			}

			// Copy temp block back to real block, adding (delta << csSal) to current values
			for bpos := header.CsFrom; bpos <= header.CsTo; bpos++ {
				current := block.GetTransposedFromZigzag(int(bpos))
				block.SetTransposedFromZigzag(int(bpos), current+(tempBlock[bpos]<<header.CsSal))
			}

			// In AC refinement, we process each block individually (even EOBRUN blocks need correction bits)
			// So just move to next position without skipping
			sta = state.NextMcuPos(header)
		}

		// Verify fill bits
		var padBitSet bool
		var padBit uint8
		if result.PadBit != nil {
			padBit = *result.PadBit
			padBitSet = true
		}
		if err := bitReader.ReadAndVerifyFillBits(&padBit, &padBitSet); err != nil {
			return err
		}
		if padBitSet && result.PadBit == nil {
			result.PadBit = &padBit
		}

		if sta == RestartIntervalExpired {
			if err := bitReader.VerifyResetCode(); err != nil {
				return err
			}
			sta = DecodeInProgress
		}
	}

	return nil
}

var debugACRefine = false

// decodeACProgressiveRefineTmp decodes AC coefficients for progressive refinement stage into a temp block
// The temp block should already be populated with current values
// Returns the eob position
func decodeACProgressiveRefineTmp(bitReader *BitReader, acTable *HuffmanTable, tempBlock []int16, state *JpegPositionState, csFrom, csTo uint8) (uint8, error) {
	bpos := csFrom
	eob := csTo

	for bpos <= csTo {
		hc, err := nextHuffCode(bitReader, acTable)
		if err != nil {
			return bpos, err
		}

		l := hc >> 4
		r := hc & 0x0F


		if l == 15 || r > 0 {
			// Run/level combination
			z := l
			s := r

			var v int16
			if s == 0 {
				v = 0
			} else if s == 1 {
				n, err := bitReader.Read(1)
				if err != nil {
					return bpos, err
				}
				if n == 0 {
					v = -1
				} else {
					v = 1
				}
			} else {
				return bpos, NewLeptonError(ExitCodeUnsupportedJpeg, "invalid coefficient size in AC refinement")
			}


			// Write zeros / correction bits using temp block
			for {
				current := tempBlock[bpos]
				if current == 0 {
					// Skip zeros / write value
					if z > 0 {
						z--
					} else {
						tempBlock[bpos] = v
						bpos++
						break
					}
				} else {
					// Read correction bit
					n, err := bitReader.Read(1)
					if err != nil {
						return bpos, err
					}
					if current > 0 {
						tempBlock[bpos] = int16(n)
					} else {
						tempBlock[bpos] = -int16(n)
					}
				}
				// Check BEFORE incrementing (matching Rust exactly)
				if bpos >= csTo {
					return bpos, NewLeptonError(ExitCodeUnsupportedJpeg, "AC refinement decoding error")
				}
				bpos++
			}
		} else {
			// EOB run
			eob = bpos
			s := l
			n, err := bitReader.Read(uint32(s))
			if err != nil {
				return bpos, err
			}
			state.Eobrun = decodeEobrunBits(s, n)

			// Since we hit EOB, use zero block decoder for the rest (on temp block)
			if err := decodeEobrunRefineTemp(bitReader, tempBlock, state, bpos, csTo); err != nil {
				return eob, err
			}
			break
		}
	}

	return eob, nil
}

// decodeEobrunRefineTemp decodes refinement bits for a zero block run into a temp block
func decodeEobrunRefineTemp(bitReader *BitReader, tempBlock []int16, state *JpegPositionState, from, to uint8) error {
	for bpos := from; bpos <= to; bpos++ {
		if tempBlock[bpos] != 0 {
			n, err := bitReader.Read(1)
			if err != nil {
				return err
			}
			if tempBlock[bpos] > 0 {
				tempBlock[bpos] = int16(n)
			} else {
				tempBlock[bpos] = -int16(n)
			}
		}
	}

	state.Eobrun--
	return nil
}

// decodeEobrunBits decodes an EOB run length
func decodeEobrunBits(s uint8, n uint16) uint16 {
	return n + (1 << s)
}

// getMaxEobRun returns the maximum EOB run for a Huffman table
func getMaxEobRun(acTable *HuffmanTable) uint16 {
	// Find the largest EOB run symbol in the table
	// EOB run symbols are 0xN0 where N is the run length bits (0-14)
	maxRun := uint16(1) // Minimum is 1 (EOB with no run)
	for i := 0; i < acTable.SymbolCount; i++ {
		sym := acTable.Symbols[i]
		if sym&0x0F == 0 && sym != 0xF0 { // EOB symbols have low nibble 0, except ZRL (0xF0)
			runBits := sym >> 4
			if runBits < 15 {
				run := uint16(1) << runBits
				if run > maxRun {
					maxRun = run
				}
			}
		}
	}
	return maxRun
}
