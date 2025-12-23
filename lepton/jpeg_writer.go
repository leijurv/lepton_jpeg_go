package lepton

import (
	"io"
)

// JpegWriter reconstructs a JPEG file from decoded DCT coefficients
type JpegWriter struct {
	header    *LeptonHeader
	bitWriter *BitWriter
	output    io.Writer

	// Huffman encoding tables
	dcCodes [4]*HuffmanEncodeTable
	acCodes [4]*HuffmanEncodeTable

	// Current DC values for differential encoding
	lastDC [MaxComponents]int16

	// Restart interval counter
	restartCounter int
}

// HuffmanEncodeTable contains precomputed codes and lengths for encoding
type HuffmanEncodeTable struct {
	codes     [256]uint16
	lengths   [256]uint8
	maxEOBRun uint16
}

// NewJpegWriter creates a new JpegWriter
func NewJpegWriter(header *LeptonHeader, output io.Writer) (*JpegWriter, error) {
	w := &JpegWriter{
		header:    header,
		bitWriter: NewBitWriter(65536),
		output:    output,
	}

	// Build Huffman encoding tables from decode tables
	for i := 0; i < 4; i++ {
		if header.JpegHeader.HuffDC[i] != nil {
			w.dcCodes[i] = buildEncodeTable(header.JpegHeader.HuffDC[i])
		}
		if header.JpegHeader.HuffAC[i] != nil {
			w.acCodes[i] = buildEncodeTable(header.JpegHeader.HuffAC[i])
		}
	}

	return w, nil
}

// buildEncodeTable builds a Huffman encoding table from a decode table
func buildEncodeTable(decodeTable *HuffmanTable) *HuffmanEncodeTable {
	encTable := &HuffmanEncodeTable{}

	code := uint16(0)
	symbolIdx := 0

	for bits := 1; bits <= 16; bits++ {
		for i := 0; i < int(decodeTable.NumCodes[bits]); i++ {
			symbol := decodeTable.Symbols[symbolIdx]
			encTable.codes[symbol] = code
			encTable.lengths[symbol] = uint8(bits)
			code++
			symbolIdx++
		}
		code <<= 1
	}

	// Determine max EOB run supported by this table (progressive AC)
	for i := 14; i >= 0; i-- {
		symbol := uint8(i << 4)
		if encTable.lengths[symbol] > 0 {
			encTable.maxEOBRun = uint16((2 << i) - 1)
			break
		}
	}

	return encTable
}

// WriteJpeg writes the complete reconstructed JPEG to the output
func (w *JpegWriter) WriteJpeg(images []*BlockBasedImage) error {
	// Write prefix garbage if any
	if len(w.header.RecoveryInfo.PrefixGarbage) > 0 {
		if _, err := w.output.Write(w.header.RecoveryInfo.PrefixGarbage); err != nil {
			return err
		}
	}

	// Write SOI marker first (not included in RawJpegHeader)
	if _, err := w.output.Write(SOI[:]); err != nil {
		return err
	}

	// Branch based on JPEG type
	if w.header.JpegType == JpegTypeProgressive {
		return w.writeProgressiveJpeg(images)
	}

	return w.writeBaselineJpeg(images)
}

// writeBaselineJpeg writes a baseline (sequential) JPEG
func (w *JpegWriter) writeBaselineJpeg(images []*BlockBasedImage) error {
	// Write the raw JPEG header up to and including SOS
	// (RawJpegHeaderReadIndex marks end of first scan's headers)
	headerToWrite := w.header.RawJpegHeader[:w.header.RawJpegHeaderReadIndex]
	if _, err := w.output.Write(headerToWrite); err != nil {
		return err
	}

	// Calculate expected scan data length for early EOF files
	// This prevents writing RST markers that would exceed the original scan data length
	maxScanBytes := int64(-1) // -1 means no limit
	if w.header.RecoveryInfo.EarlyEofEncountered {
		prefixLen := len(w.header.RecoveryInfo.PrefixGarbage)
		soiLen := 2
		headerLen := w.header.RawJpegHeaderReadIndex
		remainingHeaderLen := len(w.header.RawJpegHeader) - w.header.RawJpegHeaderReadIndex

		// For all-zero garbage, it will be part of zero padding, so don't subtract it
		garbageLen := len(w.header.RecoveryInfo.GarbageData)
		allZerosGarbage := true
		for _, b := range w.header.RecoveryInfo.GarbageData {
			if b != 0 {
				allZerosGarbage = false
				break
			}
		}
		if allZerosGarbage {
			garbageLen = 0
		}

		maxScanBytes = int64(w.header.OriginalFileSize) - int64(prefixLen) - int64(soiLen) -
			int64(headerLen) - int64(remainingHeaderLen) - int64(garbageLen)
	}
	if maxScanBytes < 0 && len(w.header.ThreadHandoffs) == 1 {
		segmentSize := w.header.ThreadHandoffs[0].SegmentSize
		if segmentSize > 0 {
			// Mirror Rust behavior: cap output to the recorded scan segment size.
			maxScanBytes = int64(segmentSize)
		}
	}

	// Write scan data
	// For partitioned paths, track the slack in the last segment which indicates
	// whether there's room for trailing RST markers from RestartErrors.
	var lastSegmentSlack int
	usedPartitionedPath := false

	numScanComponents := len(w.header.JpegHeader.ScanComponentOrder)
	if numScanComponents == 1 {
		if !w.header.RecoveryInfo.EarlyEofEncountered && len(w.header.ThreadHandoffs) > 1 {
			usedPartitionedPath = true
			var err error
			lastSegmentSlack, err = w.writeScanDataPartitionedNonInterleaved(images)
			if err != nil {
				return err
			}
		} else {
			if err := w.writeScanDataNonInterleaved(images, maxScanBytes); err != nil {
				return err
			}
		}
	} else {
		if !w.header.RecoveryInfo.EarlyEofEncountered && len(w.header.ThreadHandoffs) > 1 {
			usedPartitionedPath = true
			var err error
			lastSegmentSlack, err = w.writeScanDataPartitioned(images)
			if err != nil {
				return err
			}
		} else {
			if err := w.writeScanData(images, maxScanBytes); err != nil {
				return err
			}
		}
	}

	// Write trailing RST markers if needed (for compatibility with C++ Lepton)
	// RestartErrors (rst_err) represents extra RST markers at the end of scan data.
	//
	// For partitioned paths: only write rst_err if there's slack in the last segment.
	// If the last segment generated exactly its SegmentSize worth of data, the trailing
	// RST marker is already included in the segment data via RestartCounts. If there's
	// slack (generated < SegmentSize), the rst_err markers fill that reserved space.
	//
	// For non-partitioned paths: always write rst_err (original behavior for C++ Lepton compat).
	writeRstErr := len(w.header.RecoveryInfo.RestartErrors) > 0
	if usedPartitionedPath && lastSegmentSlack == 0 {
		// No slack means trailing RST is already in the segment data
		writeRstErr = false
	}

	if writeRstErr {
		jpegHeader := w.header.JpegHeader
		cumulativeResetMarkers := uint8(0)
		if jpegHeader.RestartInterval != 0 {
			mcuc := jpegHeader.Mcuh * jpegHeader.Mcuv
			cumulativeResetMarkers = uint8((mcuc - 1) / uint32(jpegHeader.RestartInterval))
		}
		for i := 0; i < w.header.RecoveryInfo.RestartErrors[0]; i++ {
			rst := MarkerRST0 + ((cumulativeResetMarkers + uint8(i)) & 7)
			if _, err := w.output.Write([]byte{0xFF, rst}); err != nil {
				return err
			}
		}
	}

	// Write remaining header data (after scan, before garbage)
	// This handles files with trailing data after the main image
	if w.header.RawJpegHeaderReadIndex < len(w.header.RawJpegHeader) {
		remainingHeader := w.header.RawJpegHeader[w.header.RawJpegHeaderReadIndex:]
		if _, err := w.output.Write(remainingHeader); err != nil {
			return err
		}
	}

	// Write garbage data (which includes EOI marker)
	// For early EOF files with all-zero garbage, skip writing it - it will be part of zero padding
	if len(w.header.RecoveryInfo.GarbageData) > 0 {
		skipGarbage := false
		if w.header.RecoveryInfo.EarlyEofEncountered {
			// Check if garbage is all zeros
			allZeros := true
			for _, b := range w.header.RecoveryInfo.GarbageData {
				if b != 0 {
					allZeros = false
					break
				}
			}
			skipGarbage = allZeros
		}

		if !skipGarbage {
			if _, err := w.output.Write(w.header.RecoveryInfo.GarbageData); err != nil {
				return err
			}
		}
	}

	return nil
}

// writeScanData writes the Huffman-encoded scan data
// maxScanBytes limits the scan data output for early EOF files (-1 means no limit)
func (w *JpegWriter) writeScanData(images []*BlockBasedImage, maxScanBytes int64) error {
	jpegHeader := w.header.JpegHeader
	restartInterval := int(jpegHeader.RestartInterval)
	mcuCount := 0
	restartMarkerIdx := 0
	earlyEof := w.header.RecoveryInfo.EarlyEofEncountered

	// Check if garbage is all zeros
	allZerosGarbage := true
	for _, b := range w.header.RecoveryInfo.GarbageData {
		if b != 0 {
			allZerosGarbage = false
			break
		}
	}
	bytesWritten := int64(0)
	reachedLimit := false

	writeLimited := func(data []byte) error {
		if len(data) == 0 || reachedLimit {
			return nil
		}
		if maxScanBytes >= 0 {
			remaining := maxScanBytes - bytesWritten
			if remaining <= 0 {
				reachedLimit = true
				return nil
			}
			if int64(len(data)) > remaining {
				data = data[:remaining]
				reachedLimit = true
			}
		}
		if _, err := w.output.Write(data); err != nil {
			return err
		}
		bytesWritten += int64(len(data))
		return nil
	}

	// Initialize last DC values
	for i := 0; i < MaxComponents; i++ {
		w.lastDC[i] = 0
	}

	// Iterate through MCUs
	for mcuY := uint32(0); mcuY < jpegHeader.Mcuv; mcuY++ {
		for mcuX := uint32(0); mcuX < jpegHeader.Mcuh; mcuX++ {
			if reachedLimit {
				goto endMCULoop
			}
			// For early EOF, check if all components in this MCU would exceed their MaxDpos
			if earlyEof {
				allExceeded := true
				for _, cmp := range jpegHeader.ScanComponentOrder {
					ci := &jpegHeader.CmpInfo[cmp]
					// Check first block of this component in this MCU
					blockX := mcuX * ci.Sfh
					blockY := mcuY * ci.Sfv
					dpos := blockY*ci.Bch + blockX
					if dpos <= w.header.RecoveryInfo.MaxDpos[cmp] {
						allExceeded = false
						break
					}
				}
				if allExceeded {
					goto endMCULoop
				}
			}

			// Write all blocks in this MCU in SOS component order
			for _, cmp := range jpegHeader.ScanComponentOrder {
				ci := &jpegHeader.CmpInfo[cmp]

				// Each component may have multiple blocks per MCU
				for v := uint32(0); v < ci.Sfv; v++ {
					for h := uint32(0); h < ci.Sfh; h++ {
						blockX := mcuX*ci.Sfh + h
						blockY := mcuY*ci.Sfv + v

						// For early EOF, skip blocks beyond MaxDpos for this component
						if earlyEof {
							dpos := blockY*ci.Bch + blockX
							if dpos > w.header.RecoveryInfo.MaxDpos[cmp] {
								continue
							}
						}

						block := images[cmp].GetBlockXY(blockX, blockY)
						if block == nil {
							block = &EmptyBlock
						}

						if err := w.writeBlock(block, cmp); err != nil {
							return err
						}
					}
				}
			}

			mcuCount++

			// Check for restart interval
			if restartInterval > 0 && mcuCount >= restartInterval {
				// Pad to byte boundary
				padBit := byte(0xFF)
				if w.header.RecoveryInfo.PadBit != nil {
					padBit = *w.header.RecoveryInfo.PadBit
				}
				w.bitWriter.Pad(padBit)

				// Flush buffer
				data := w.bitWriter.DetachBuffer()
				if err := writeLimited(data); err != nil {
					return err
				}

				// Write restart marker (if not at end and not about to trigger early EOF)
				if mcuY < jpegHeader.Mcuv-1 || mcuX < jpegHeader.Mcuh-1 {
					writeRst := true

					// For early EOF with all-zero garbage, check if we should skip the RST marker
					if earlyEof && allZerosGarbage {
						// Check 1: if the NEXT MCU would trigger early EOF, skip RST
						nextMcuX := mcuX + 1
						nextMcuY := mcuY
						if nextMcuX >= jpegHeader.Mcuh {
							nextMcuX = 0
							nextMcuY++
						}
						if nextMcuY < jpegHeader.Mcuv {
							// Check if all components in next MCU would exceed MaxDpos
							allExceeded := true
							for _, cmp := range jpegHeader.ScanComponentOrder {
								ci := &jpegHeader.CmpInfo[cmp]
								blockX := nextMcuX * ci.Sfh
								blockY := nextMcuY * ci.Sfv
								dpos := blockY*ci.Bch + blockX
								if dpos <= w.header.RecoveryInfo.MaxDpos[cmp] {
									allExceeded = false
									break
								}
							}
							if allExceeded {
								writeRst = false
							}
						}

						// Check 2: if writing RST would put us in the padding zone, skip RST
						// For early EOF with all-zero garbage, the GarbageData doesn't capture
						// all trailing zeros. Use thresholds to detect the padding zone.
						if writeRst && maxScanBytes > 0 {
							// The original file had trailing zeros that aren't in GarbageData.
							// Smaller files tend to have larger padding ratios, so use
							// different strategies:
							// - Large files (>10KB): use fixed threshold (256 bytes)
							// - Small files (<10KB): use percentage threshold (70%)
							var paddingThreshold int64
							if maxScanBytes > 10000 {
								paddingThreshold = 256
							} else {
								paddingThreshold = maxScanBytes * 30 / 100 // 30% reserved for padding
							}

							if bytesWritten+2 > maxScanBytes-paddingThreshold {
								writeRst = false
							}
						}
					}

					if writeRst {
						rstMarker := []byte{0xFF, byte(MarkerRST0 + (restartMarkerIdx & 7))}
						if err := writeLimited(rstMarker); err != nil {
							return err
						}
						restartMarkerIdx++
					}
				}

				// Reset DC values
				for i := 0; i < MaxComponents; i++ {
					w.lastDC[i] = 0
				}

				mcuCount = 0
			}
		}
	}

endMCULoop:

	// Pad final bits and flush
	padBit := byte(0xFF)
	if w.header.RecoveryInfo.PadBit != nil {
		padBit = *w.header.RecoveryInfo.PadBit
	}
	w.bitWriter.Pad(padBit)

	data := w.bitWriter.DetachBuffer()
	if err := writeLimited(data); err != nil {
		return err
	}

	return nil
}

// writeScanDataNonInterleaved writes scan data for single-component baseline scans.
// Restart intervals are counted per block to match the Rust encoder behavior for cs_cmpc == 1.
func (w *JpegWriter) writeScanDataNonInterleaved(images []*BlockBasedImage, maxScanBytes int64) error {
	jpegHeader := w.header.JpegHeader
	restartInterval := int(jpegHeader.RestartInterval)
	blockCount := 0
	restartMarkerIdx := 0
	earlyEof := w.header.RecoveryInfo.EarlyEofEncountered

	// Check if garbage is all zeros
	allZerosGarbage := true
	for _, b := range w.header.RecoveryInfo.GarbageData {
		if b != 0 {
			allZerosGarbage = false
			break
		}
	}
	bytesWritten := int64(0)
	reachedLimit := false

	writeLimited := func(data []byte) error {
		if len(data) == 0 || reachedLimit {
			return nil
		}
		if maxScanBytes >= 0 {
			remaining := maxScanBytes - bytesWritten
			if remaining <= 0 {
				reachedLimit = true
				return nil
			}
			if int64(len(data)) > remaining {
				data = data[:remaining]
				reachedLimit = true
			}
		}
		if _, err := w.output.Write(data); err != nil {
			return err
		}
		bytesWritten += int64(len(data))
		return nil
	}

	cmp := jpegHeader.ScanComponentOrder[0]
	ci := &jpegHeader.CmpInfo[cmp]
	totalBlocks := ci.Bch * ci.Bcv

	for dpos := uint32(0); dpos < totalBlocks; dpos++ {
		blockX := dpos % ci.Bch
		blockY := dpos / ci.Bch

		// Skip padding blocks outside natural dimensions
		if blockX >= ci.Nch || blockY >= ci.Ncv {
			continue
		}

		block := images[cmp].GetBlockXY(blockX, blockY)
		if block == nil {
			block = &EmptyBlock
		}

		if err := w.writeBlock(block, cmp); err != nil {
			return err
		}

		blockCount++
		if restartInterval > 0 && blockCount >= restartInterval {
			padBit := byte(0xFF)
			if w.header.RecoveryInfo.PadBit != nil {
				padBit = *w.header.RecoveryInfo.PadBit
			}
			w.bitWriter.Pad(padBit)

			data := w.bitWriter.DetachBuffer()
			if err := writeLimited(data); err != nil {
				return err
			}

			if !reachedLimit {
				writeRst := true
				if earlyEof && allZerosGarbage {
					if maxScanBytes >= 0 {
						// Mirror the interleaved path behavior: avoid RST markers in padding zone.
						var paddingThreshold int64
						if maxScanBytes > 10000 {
							paddingThreshold = 256
						} else {
							paddingThreshold = maxScanBytes * 30 / 100
						}
						if bytesWritten+2 > maxScanBytes-paddingThreshold {
							writeRst = false
						}
					}
				}

				if writeRst {
					rstMarker := []byte{0xFF, byte(MarkerRST0 + (restartMarkerIdx & 7))}
					if err := writeLimited(rstMarker); err != nil {
						return err
					}
					restartMarkerIdx++
				}
			}

			for i := 0; i < MaxComponents; i++ {
				w.lastDC[i] = 0
			}
			blockCount = 0
		}
	}

	// Pad final bits and flush
	padBit := byte(0xFF)
	if w.header.RecoveryInfo.PadBit != nil {
		padBit = *w.header.RecoveryInfo.PadBit
	}
	w.bitWriter.Pad(padBit)

	data := w.bitWriter.DetachBuffer()
	if err := writeLimited(data); err != nil {
		return err
	}

	return nil
}

func (w *JpegWriter) writeScanDataPartitioned(images []*BlockBasedImage) (int, error) {
	jpegHeader := w.header.JpegHeader
	if jpegHeader.Cmpc == 0 {
		return 0, nil
	}

	lumaSfv := jpegHeader.CmpInfo[0].Sfv
	if lumaSfv == 0 {
		return 0, ErrExitCode(ExitCodeBadLeptonFile, "invalid luma sampling factor")
	}

	var lastSegmentSlack int
	for idx, handoff := range w.header.ThreadHandoffs {
		w.bitWriter.ResetFromOverhang(handoff.OverhangByte, uint32(handoff.NumOverhangBits))

		for i := 0; i < MaxComponents; i++ {
			w.lastDC[i] = 0
		}
		for i := 0; i < jpegHeader.Cmpc && i < len(handoff.LastDC); i++ {
			w.lastDC[i] = handoff.LastDC[i]
		}

		mcuYStart := handoff.LumaYStart / lumaSfv
		mcuYEnd := handoff.LumaYEnd / lumaSfv
		if mcuYEnd > jpegHeader.Mcuv {
			mcuYEnd = jpegHeader.Mcuv
		}

		padAtEnd := idx == len(w.header.ThreadHandoffs)-1
		buf, err := w.encodeScanMcuRange(images, mcuYStart, mcuYEnd, padAtEnd)
		if err != nil {
			return 0, err
		}

		lastSegmentSlack = int(handoff.SegmentSize) - len(buf)
		if int(handoff.SegmentSize) < len(buf) {
			buf = buf[:handoff.SegmentSize]
		}
		if _, err := w.output.Write(buf); err != nil {
			return 0, err
		}
	}

	return lastSegmentSlack, nil
}

func (w *JpegWriter) writeScanDataPartitionedNonInterleaved(images []*BlockBasedImage) (int, error) {
	jpegHeader := w.header.JpegHeader
	if jpegHeader.Cmpc == 0 {
		return 0, nil
	}
	if len(jpegHeader.ScanComponentOrder) != 1 {
		return 0, ErrExitCode(ExitCodeBadLeptonFile, "invalid scan component order for non-interleaved baseline")
	}

	cmp := jpegHeader.ScanComponentOrder[0]
	ci := &jpegHeader.CmpInfo[cmp]

	var lastSegmentSlack int
	for idx, handoff := range w.header.ThreadHandoffs {
		w.bitWriter.ResetFromOverhang(handoff.OverhangByte, uint32(handoff.NumOverhangBits))

		for i := 0; i < MaxComponents; i++ {
			w.lastDC[i] = 0
		}
		for i := 0; i < jpegHeader.Cmpc && i < len(handoff.LastDC); i++ {
			w.lastDC[i] = handoff.LastDC[i]
		}

		startDpos := handoff.LumaYStart * ci.Bch
		endDpos := handoff.LumaYEnd * ci.Bch
		if endDpos > ci.Bc {
			endDpos = ci.Bc
		}

		padAtEnd := idx == len(w.header.ThreadHandoffs)-1
		buf, err := w.encodeScanDposRange(images, cmp, startDpos, endDpos, padAtEnd)
		if err != nil {
			return 0, err
		}

		lastSegmentSlack = int(handoff.SegmentSize) - len(buf)
		if int(handoff.SegmentSize) < len(buf) {
			buf = buf[:handoff.SegmentSize]
		}
		if _, err := w.output.Write(buf); err != nil {
			return 0, err
		}
	}

	return lastSegmentSlack, nil
}

func (w *JpegWriter) encodeScanDposRange(
	images []*BlockBasedImage,
	cmp int,
	startDpos, endDpos uint32,
	padAtEnd bool,
) ([]byte, error) {
	jpegHeader := w.header.JpegHeader
	ci := &jpegHeader.CmpInfo[cmp]
	restartInterval := int(jpegHeader.RestartInterval)

	blockCount := 0
	restartMarkerIdx := 0
	if restartInterval > 0 {
		globalStart := int(startDpos)
		restartMarkerIdx = globalStart / restartInterval
		blockCount = globalStart % restartInterval
	}

	for dpos := startDpos; dpos < endDpos; dpos++ {
		blockX := dpos % ci.Bch
		blockY := dpos / ci.Bch

		if blockX >= ci.Nch || blockY >= ci.Ncv {
			continue
		}

		block := images[cmp].GetBlockXY(blockX, blockY)
		if block == nil {
			block = &EmptyBlock
		}

		if err := w.writeBlock(block, cmp); err != nil {
			return nil, err
		}

		if restartInterval > 0 {
			blockCount++
			if blockCount >= restartInterval {
				padBit := byte(0xFF)
				if w.header.RecoveryInfo.PadBit != nil {
					padBit = *w.header.RecoveryInfo.PadBit
				}
				w.bitWriter.Pad(padBit)

				// Only write RST marker if RestartCounts allows it
				// (mirrors Rust behavior for backward compatibility with C++ Lepton files)
				shouldWriteRst := len(w.header.RecoveryInfo.RestartCounts) == 0 ||
					!w.header.RecoveryInfo.RestartCountsSet ||
					restartMarkerIdx < int(w.header.RecoveryInfo.RestartCounts[0])

				if shouldWriteRst {
					w.bitWriter.WriteByteUnescaped(0xFF)
					w.bitWriter.WriteByteUnescaped(byte(MarkerRST0 + (restartMarkerIdx & 7)))
				}
				restartMarkerIdx++

				for i := 0; i < MaxComponents; i++ {
					w.lastDC[i] = 0
				}
				blockCount = 0
			}
		}
	}

	if padAtEnd {
		padBit := byte(0xFF)
		if w.header.RecoveryInfo.PadBit != nil {
			padBit = *w.header.RecoveryInfo.PadBit
		}
		w.bitWriter.Pad(padBit)
	}

	return w.bitWriter.DetachBuffer(), nil
}

func (w *JpegWriter) encodeScanMcuRange(
	images []*BlockBasedImage,
	mcuYStart, mcuYEnd uint32,
	padAtEnd bool,
) ([]byte, error) {
	jpegHeader := w.header.JpegHeader
	restartInterval := int(jpegHeader.RestartInterval)

	mcuCount := 0
	restartMarkerIdx := 0
	if restartInterval > 0 {
		globalStart := int(mcuYStart * jpegHeader.Mcuh)
		restartMarkerIdx = globalStart / restartInterval
		mcuCount = globalStart % restartInterval
	}

	for mcuY := mcuYStart; mcuY < mcuYEnd; mcuY++ {
		for mcuX := uint32(0); mcuX < jpegHeader.Mcuh; mcuX++ {
			for _, cmp := range jpegHeader.ScanComponentOrder {
				ci := &jpegHeader.CmpInfo[cmp]

				for v := uint32(0); v < ci.Sfv; v++ {
					for h := uint32(0); h < ci.Sfh; h++ {
						blockX := mcuX*ci.Sfh + h
						blockY := mcuY*ci.Sfv + v

						block := images[cmp].GetBlockXY(blockX, blockY)
						if block == nil {
							block = &EmptyBlock
						}

						if err := w.writeBlock(block, cmp); err != nil {
							return nil, err
						}
					}
				}
			}

			if restartInterval > 0 {
				mcuCount++
				if mcuCount >= restartInterval {
					padBit := byte(0xFF)
					if w.header.RecoveryInfo.PadBit != nil {
						padBit = *w.header.RecoveryInfo.PadBit
					}
					w.bitWriter.Pad(padBit)

					// Only write RST marker if RestartCounts allows it
					// (mirrors Rust behavior for backward compatibility with C++ Lepton files)
					shouldWriteRst := len(w.header.RecoveryInfo.RestartCounts) == 0 ||
						!w.header.RecoveryInfo.RestartCountsSet ||
						restartMarkerIdx < int(w.header.RecoveryInfo.RestartCounts[0])

					if shouldWriteRst {
						w.bitWriter.WriteByteUnescaped(0xFF)
						w.bitWriter.WriteByteUnescaped(byte(MarkerRST0 + (restartMarkerIdx & 7)))
					}
					restartMarkerIdx++

					for i := 0; i < MaxComponents; i++ {
						w.lastDC[i] = 0
					}
					mcuCount = 0
				}
			}
		}
	}

	if padAtEnd {
		padBit := byte(0xFF)
		if w.header.RecoveryInfo.PadBit != nil {
			padBit = *w.header.RecoveryInfo.PadBit
		}
		w.bitWriter.Pad(padBit)
	}

	return w.bitWriter.DetachBuffer(), nil
}

// writeBlock writes a single 8x8 block using Huffman encoding
func (w *JpegWriter) writeBlock(block *AlignedBlock, componentIdx int) error {
	jpegHeader := w.header.JpegHeader
	ci := &jpegHeader.CmpInfo[componentIdx]

	dcTable := w.dcCodes[ci.HuffDC]
	acTable := w.acCodes[ci.HuffAC]

	// Get the block in zigzag order (convert from transposed)
	zigzagBlock := block.ZigzagFromTransposed()

	// Encode DC coefficient (differential)
	dc := zigzagBlock.RawData[0]
	dcDiff := dc - w.lastDC[componentIdx]
	w.lastDC[componentIdx] = dc

	w.encodeDC(dcDiff, dcTable)

	// Encode AC coefficients
	w.encodeAC(&zigzagBlock, acTable)

	return nil
}

// encodeDC encodes a DC coefficient difference
func (w *JpegWriter) encodeDC(diff int16, table *HuffmanEncodeTable) {
	// Calculate category (bit size)
	absVal := diff
	if absVal < 0 {
		absVal = -absVal
	}

	category := uint8(0)
	temp := absVal
	for temp > 0 {
		category++
		temp >>= 1
	}

	// Write category using Huffman code
	w.bitWriter.Write(uint32(table.codes[category]), uint32(table.lengths[category]))

	// Write additional bits for the actual value
	if category > 0 {
		var additionalBits uint32
		if diff >= 0 {
			additionalBits = uint32(diff)
		} else {
			// For negative numbers, use one's complement
			additionalBits = uint32(diff-1) & ((1 << category) - 1)
		}
		w.bitWriter.Write(additionalBits, uint32(category))
	}
}

// encodeAC encodes AC coefficients
func (w *JpegWriter) encodeAC(block *AlignedBlock, table *HuffmanEncodeTable) {
	zeroRunLength := 0

	for i := 1; i < 64; i++ {
		coef := block.RawData[i]

		if coef == 0 {
			zeroRunLength++
		} else {
			// Before encoding this non-zero coefficient, emit ZRL codes for any runs >= 16
			for zeroRunLength >= 16 {
				// ZRL (zero run length of 16)
				w.bitWriter.Write(uint32(table.codes[0xF0]), uint32(table.lengths[0xF0]))
				zeroRunLength -= 16
			}

			// Encode the coefficient with remaining zero run
			absCoef := coef
			if absCoef < 0 {
				absCoef = -absCoef
			}

			// Calculate category
			category := uint8(0)
			temp := absCoef
			for temp > 0 {
				category++
				temp >>= 1
			}

			// Symbol = (zero_run << 4) | category
			symbol := uint8(zeroRunLength<<4) | category

			// Write Huffman code for the symbol
			w.bitWriter.Write(uint32(table.codes[symbol]), uint32(table.lengths[symbol]))

			// Write additional bits
			var additionalBits uint32
			if coef >= 0 {
				additionalBits = uint32(coef)
			} else {
				additionalBits = uint32(coef-1) & ((1 << category) - 1)
			}
			w.bitWriter.Write(additionalBits, uint32(category))

			zeroRunLength = 0
		}
	}

	// If block ends with zeros (or is all zeros), write EOB
	// Note: zeroRunLength > 0 means there are trailing zeros
	// For all-zero AC, zeroRunLength will be 63
	if zeroRunLength > 0 {
		// EOB marker
		w.bitWriter.Write(uint32(table.codes[0x00]), uint32(table.lengths[0x00]))
	}
}

// writeProgressiveJpeg writes a progressive JPEG with multiple scans
func (w *JpegWriter) writeProgressiveJpeg(images []*BlockBasedImage) error {
	// Write the raw JPEG header up to and including first SOS
	headerToWrite := w.header.RawJpegHeader[:w.header.RawJpegHeaderReadIndex]
	if _, err := w.output.Write(headerToWrite); err != nil {
		return err
	}

	// Track current scan index for RST marker counting
	scanIndex := 0

	// Loop through scans
	for {
		// Write scan data for current scan
		if err := w.writeProgressiveScanData(images, scanIndex); err != nil {
			return err
		}

		// Try to advance to next header segment
		oldPos := w.header.RawJpegHeaderReadIndex
		hasMore, err := w.advanceNextHeaderSegment()
		if err != nil {
			return err
		}

		if !hasMore {
			// No more scans - write remaining header (may contain EOI)
			if oldPos < len(w.header.RawJpegHeader) {
				remainingHeader := w.header.RawJpegHeader[oldPos:]
				if _, err := w.output.Write(remainingHeader); err != nil {
					return err
				}
			}
			break
		}

		// Write inter-scan headers (DHT, etc.) and next SOS
		interScanHeader := w.header.RawJpegHeader[oldPos:w.header.RawJpegHeaderReadIndex]
		if _, err := w.output.Write(interScanHeader); err != nil {
			return err
		}

		scanIndex++
	}

	// Write garbage data (which includes EOI marker)
	if len(w.header.RecoveryInfo.GarbageData) > 0 {
		if _, err := w.output.Write(w.header.RecoveryInfo.GarbageData); err != nil {
			return err
		}
	}

	return nil
}

// advanceNextHeaderSegment parses and advances to the next header segment
// Returns true if there's another scan, false if we've reached EOI or end of header
func (w *JpegWriter) advanceNextHeaderSegment() (bool, error) {
	data := w.header.RawJpegHeader
	pos := w.header.RawJpegHeaderReadIndex

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
		case MarkerEOI:
			// End of image - no more scans
			w.header.RawJpegHeaderReadIndex = pos
			return false, nil

		case MarkerDHT:
			// Parse new Huffman table and rebuild encoding tables
			length := int(data[pos])<<8 | int(data[pos+1])
			dhtContent := data[pos+2 : pos+length]
			if err := parseDHT(w.header.JpegHeader, dhtContent); err != nil {
				return false, err
			}
			// Rebuild encoding tables
			for i := 0; i < 4; i++ {
				if w.header.JpegHeader.HuffDC[i] != nil {
					w.dcCodes[i] = buildEncodeTable(w.header.JpegHeader.HuffDC[i])
				}
				if w.header.JpegHeader.HuffAC[i] != nil {
					w.acCodes[i] = buildEncodeTable(w.header.JpegHeader.HuffAC[i])
				}
			}
			pos += length

		case MarkerSOS:
			// Start of next scan - parse scan params and return
			if err := parseSOS(w.header.JpegHeader, data[pos:]); err != nil {
				return false, err
			}
			sosLength := int(data[pos])<<8 | int(data[pos+1])
			pos += sosLength
			w.header.RawJpegHeaderReadIndex = pos
			return true, nil

		case MarkerDRI:
			// Restart interval update
			w.header.JpegHeader.RestartInterval = uint16(data[pos+2])<<8 | uint16(data[pos+3])
			length := int(data[pos])<<8 | int(data[pos+1])
			pos += length

		default:
			// Skip unknown markers
			if pos+2 <= len(data) {
				length := int(data[pos])<<8 | int(data[pos+1])
				pos += length
			}
		}
	}

	w.header.RawJpegHeaderReadIndex = pos
	return false, nil
}

// writeProgressiveScanData writes scan data for a single progressive scan
func (w *JpegWriter) writeProgressiveScanData(images []*BlockBasedImage, scanIndex int) error {
	jpegHeader := w.header.JpegHeader

	// Reset DC values at start of scan
	for i := 0; i < MaxComponents; i++ {
		w.lastDC[i] = 0
	}

	// Determine scan type
	isDCOnly := jpegHeader.CsTo == 0
	isFirstStage := jpegHeader.CsSah == 0
	numScanComponents := len(jpegHeader.ScanComponentOrder)

	// For single-component scans, use non-interleaved iteration
	// For multi-component scans, use MCU-interleaved iteration
	if numScanComponents == 1 {
		return w.writeProgressiveScanNonInterleaved(images, isDCOnly, isFirstStage)
	}
	return w.writeProgressiveScanInterleaved(images, isDCOnly, isFirstStage)
}

// writeProgressiveScanNonInterleaved writes a single-component progressive scan
func (w *JpegWriter) writeProgressiveScanNonInterleaved(images []*BlockBasedImage, isDCOnly, isFirstStage bool) error {
	jpegHeader := w.header.JpegHeader
	cmp := jpegHeader.ScanComponentOrder[0]
	ci := &jpegHeader.CmpInfo[cmp]

	restartInterval := int(jpegHeader.RestartInterval)
	blockCount := 0
	restartMarkerIdx := 0
	eobRun := 0
	correctionBits := make([]uint8, 0)

	// Total blocks for this component
	totalBlocks := ci.Bch * ci.Bcv

	// Iterate through all blocks of this component
	for dpos := uint32(0); dpos < totalBlocks; dpos++ {
		blockX := dpos % ci.Bch
		blockY := dpos / ci.Bch

		// Skip blocks beyond natural dimensions (padding blocks)
		if blockX >= ci.Nch || blockY >= ci.Ncv {
			continue
		}

		block := images[cmp].GetBlockXY(blockX, blockY)
		if block == nil {
			block = &EmptyBlock
		}

		if isDCOnly {
			if isFirstStage {
				w.encodeDCFirst(block, cmp)
			} else {
				w.encodeDCRefine(block)
			}
		} else {
			if isFirstStage {
				var err error
				eobRun, err = w.encodeACFirst(block, cmp, eobRun)
				if err != nil {
					return err
				}
			} else {
				var err error
				eobRun, err = w.encodeACRefine(block, cmp, eobRun, &correctionBits)
				if err != nil {
					return err
				}
			}
		}

		blockCount++

		// Check for restart interval (counted per block in non-interleaved)
		if restartInterval > 0 && blockCount >= restartInterval {
			// Flush any pending EOBrun before restart
			if !isDCOnly && eobRun > 0 {
				w.encodeEOBRun(cmp, eobRun)
				w.writeCorrectionBits(&correctionBits)
				eobRun = 0
			}

			// Pad to byte boundary
			padBit := byte(0xFF)
			if w.header.RecoveryInfo.PadBit != nil {
				padBit = *w.header.RecoveryInfo.PadBit
			}
			w.bitWriter.Pad(padBit)

			// Flush buffer
			data := w.bitWriter.DetachBuffer()
			if _, err := w.output.Write(data); err != nil {
				return err
			}

			// Write restart marker (if not at end)
			if dpos < totalBlocks-1 {
				rstMarker := []byte{0xFF, byte(MarkerRST0 + (restartMarkerIdx & 7))}
				if _, err := w.output.Write(rstMarker); err != nil {
					return err
				}
				restartMarkerIdx++
			}

			// Reset DC values and EOBrun
			for i := 0; i < MaxComponents; i++ {
				w.lastDC[i] = 0
			}
			eobRun = 0
			correctionBits = correctionBits[:0]

			blockCount = 0
		}
	}

	// Flush any pending EOBrun at end of scan
	if !isDCOnly && eobRun > 0 {
		w.encodeEOBRun(cmp, eobRun)
		w.writeCorrectionBits(&correctionBits)
	}

	// Pad final bits and flush
	padBit := byte(0xFF)
	if w.header.RecoveryInfo.PadBit != nil {
		padBit = *w.header.RecoveryInfo.PadBit
	}
	w.bitWriter.Pad(padBit)

	data := w.bitWriter.DetachBuffer()
	if _, err := w.output.Write(data); err != nil {
		return err
	}

	return nil
}

// writeProgressiveScanInterleaved writes a multi-component progressive scan (MCU-interleaved)
func (w *JpegWriter) writeProgressiveScanInterleaved(images []*BlockBasedImage, isDCOnly, isFirstStage bool) error {
	jpegHeader := w.header.JpegHeader
	restartInterval := int(jpegHeader.RestartInterval)
	mcuCount := 0
	restartMarkerIdx := 0
	eobRun := 0
	correctionBits := make([]uint8, 0)

	// Iterate through MCUs
	for mcuY := uint32(0); mcuY < jpegHeader.Mcuv; mcuY++ {
		for mcuX := uint32(0); mcuX < jpegHeader.Mcuh; mcuX++ {
			// Write all blocks in this MCU in SOS component order
			for _, cmp := range jpegHeader.ScanComponentOrder {
				ci := &jpegHeader.CmpInfo[cmp]

				// Each component may have multiple blocks per MCU
				for v := uint32(0); v < ci.Sfv; v++ {
					for h := uint32(0); h < ci.Sfh; h++ {
						blockX := mcuX*ci.Sfh + h
						blockY := mcuY*ci.Sfv + v

						block := images[cmp].GetBlockXY(blockX, blockY)
						if block == nil {
							block = &EmptyBlock
						}

						if isDCOnly {
							if isFirstStage {
								w.encodeDCFirst(block, cmp)
							} else {
								w.encodeDCRefine(block)
							}
						} else {
							if isFirstStage {
								var err error
								eobRun, err = w.encodeACFirst(block, cmp, eobRun)
								if err != nil {
									return err
								}
							} else {
								var err error
								eobRun, err = w.encodeACRefine(block, cmp, eobRun, &correctionBits)
								if err != nil {
									return err
								}
							}
						}
					}
				}
			}

			mcuCount++

			// Check for restart interval
			if restartInterval > 0 && mcuCount >= restartInterval {
				// Flush any pending EOBrun before restart
				if !isDCOnly && eobRun > 0 {
					w.encodeEOBRun(jpegHeader.ScanComponentOrder[0], eobRun)
					w.writeCorrectionBits(&correctionBits)
					eobRun = 0
				}

				// Pad to byte boundary
				padBit := byte(0xFF)
				if w.header.RecoveryInfo.PadBit != nil {
					padBit = *w.header.RecoveryInfo.PadBit
				}
				w.bitWriter.Pad(padBit)

				// Flush buffer
				data := w.bitWriter.DetachBuffer()
				if _, err := w.output.Write(data); err != nil {
					return err
				}

				// Write restart marker (if not at end)
				if mcuY < jpegHeader.Mcuv-1 || mcuX < jpegHeader.Mcuh-1 {
					rstMarker := []byte{0xFF, byte(MarkerRST0 + (restartMarkerIdx & 7))}
					if _, err := w.output.Write(rstMarker); err != nil {
						return err
					}
					restartMarkerIdx++
				}

				// Reset DC values
				for i := 0; i < MaxComponents; i++ {
					w.lastDC[i] = 0
				}

				mcuCount = 0
				correctionBits = correctionBits[:0]
			}
		}
	}

	// Flush any pending EOBrun at end of scan
	if !isDCOnly && eobRun > 0 {
		w.encodeEOBRun(jpegHeader.ScanComponentOrder[0], eobRun)
		w.writeCorrectionBits(&correctionBits)
	}

	// Pad final bits and flush
	padBit := byte(0xFF)
	if w.header.RecoveryInfo.PadBit != nil {
		padBit = *w.header.RecoveryInfo.PadBit
	}
	w.bitWriter.Pad(padBit)

	data := w.bitWriter.DetachBuffer()
	if _, err := w.output.Write(data); err != nil {
		return err
	}

	return nil
}

// encodeDCFirst encodes DC coefficient for progressive first stage
func (w *JpegWriter) encodeDCFirst(block *AlignedBlock, componentIdx int) {
	jpegHeader := w.header.JpegHeader
	ci := &jpegHeader.CmpInfo[componentIdx]
	dcTable := w.dcCodes[ci.HuffDC]

	// Get DC value from zigzag position 0 (which is transposed position 0)
	dc := block.RawData[0]

	// Shift right by cs_sal (successive approximation low)
	shiftedDC := dc >> jpegHeader.CsSal

	// Differential encoding
	diff := shiftedDC - w.lastDC[componentIdx]
	w.lastDC[componentIdx] = shiftedDC

	// Encode the difference
	w.encodeDC(diff, dcTable)
}

// encodeDCRefine encodes DC coefficient for progressive refinement stage
func (w *JpegWriter) encodeDCRefine(block *AlignedBlock) {
	jpegHeader := w.header.JpegHeader

	// Get DC value and extract the bit at position cs_sal
	dc := block.RawData[0]
	bit := (dc >> jpegHeader.CsSal) & 1

	// Write single bit
	w.bitWriter.Write(uint32(bit), 1)
}

// encodeACFirst encodes AC coefficients for progressive first stage
func (w *JpegWriter) encodeACFirst(block *AlignedBlock, componentIdx int, eobRun int) (int, error) {
	jpegHeader := w.header.JpegHeader
	ci := &jpegHeader.CmpInfo[componentIdx]
	acTable := w.acCodes[ci.HuffAC]

	// Get block in zigzag order
	zigzagBlock := block.ZigzagFromTransposed()

	// Encode coefficients
	zeroRunLength := 0
	for i := int(jpegHeader.CsFrom); i <= int(jpegHeader.CsTo); i++ {
		coef := divPow2(zigzagBlock.RawData[i], jpegHeader.CsSal)
		if coef != 0 {
			if eobRun > 0 {
				w.encodeEOBRun(componentIdx, eobRun)
				eobRun = 0
			}
			for zeroRunLength >= 16 {
				w.bitWriter.Write(uint32(acTable.codes[0xF0]), uint32(acTable.lengths[0xF0]))
				zeroRunLength -= 16
			}
			w.writeCoef(acTable, coef, zeroRunLength)
			zeroRunLength = 0
		} else {
			zeroRunLength++
		}
	}

	if zeroRunLength > 0 {
		if acTable.maxEOBRun == 0 {
			return 0, ErrExitCode(ExitCodeUnsupportedJpeg, "no EOB run symbol in Huffman table")
		}
		eobRun++
		if eobRun == int(acTable.maxEOBRun) {
			w.encodeEOBRun(componentIdx, eobRun)
			eobRun = 0
		}
	}

	return eobRun, nil
}

// encodeACRefine encodes AC coefficients for progressive refinement stage
func (w *JpegWriter) encodeACRefine(block *AlignedBlock, componentIdx int, eobRun int, correctionBits *[]uint8) (int, error) {
	jpegHeader := w.header.JpegHeader
	ci := &jpegHeader.CmpInfo[componentIdx]
	acTable := w.acCodes[ci.HuffAC]

	// Get block in zigzag order
	zigzagBlock := block.ZigzagFromTransposed()

	from := int(jpegHeader.CsFrom)
	to := int(jpegHeader.CsTo)

	eob := from
	for bpos := to; bpos >= from; bpos-- {
		coef := divPow2(zigzagBlock.RawData[bpos], jpegHeader.CsSal)
		if coef == 1 || coef == -1 {
			eob = bpos + 1
			break
		}
	}

	if eob > from && eobRun > 0 {
		w.encodeEOBRun(componentIdx, eobRun)
		w.writeCorrectionBits(correctionBits)
		eobRun = 0
	}

	zeroRunLength := 0
	for bpos := from; bpos < eob; bpos++ {
		coef := divPow2(zigzagBlock.RawData[bpos], jpegHeader.CsSal)
		if coef == 0 {
			zeroRunLength++
			if zeroRunLength == 16 {
				w.bitWriter.Write(uint32(acTable.codes[0xF0]), uint32(acTable.lengths[0xF0]))
				w.writeCorrectionBits(correctionBits)
				zeroRunLength = 0
			}
			continue
		}

		if coef == 1 || coef == -1 {
			w.writeCoef(acTable, coef, zeroRunLength)
			w.writeCorrectionBits(correctionBits)
			zeroRunLength = 0
		} else {
			*correctionBits = append(*correctionBits, uint8(coef&1))
		}
	}

	for bpos := eob; bpos <= to; bpos++ {
		coef := divPow2(zigzagBlock.RawData[bpos], jpegHeader.CsSal)
		if coef != 0 {
			*correctionBits = append(*correctionBits, uint8(coef&1))
		}
	}

	if eob <= to {
		if acTable.maxEOBRun == 0 {
			return 0, ErrExitCode(ExitCodeUnsupportedJpeg, "no EOB run symbol in Huffman table")
		}
		eobRun++
		if eobRun == int(acTable.maxEOBRun) {
			w.encodeEOBRun(componentIdx, eobRun)
			w.writeCorrectionBits(correctionBits)
			eobRun = 0
		}
	}

	return eobRun, nil
}

// encodeEOBRun encodes an EOB run
func (w *JpegWriter) encodeEOBRun(componentIdx int, eobRun int) {
	if eobRun == 0 {
		return
	}

	jpegHeader := w.header.JpegHeader
	ci := &jpegHeader.CmpInfo[componentIdx]
	acTable := w.acCodes[ci.HuffAC]

	// Calculate category (bit length of eobRun)
	category := 0
	temp := eobRun
	for temp > 0 {
		category++
		temp >>= 1
	}
	category-- // EOBn symbol uses category-1

	// Symbol for EOBn is (category << 4)
	symbol := uint8(category << 4)

	// Write Huffman code
	w.bitWriter.Write(uint32(acTable.codes[symbol]), uint32(acTable.lengths[symbol]))

	// Write additional bits (eobRun minus the leading 1 bit)
	if category > 0 {
		additionalBits := eobRun - (1 << category)
		w.bitWriter.Write(uint32(additionalBits), uint32(category))
	}
}

func (w *JpegWriter) writeCoef(table *HuffmanEncodeTable, coef int16, zeroRunLength int) {
	absCoef := coef
	if absCoef < 0 {
		absCoef = -absCoef
	}

	category := uint8(0)
	temp := absCoef
	for temp > 0 {
		category++
		temp >>= 1
	}

	symbol := uint8(zeroRunLength<<4) | category
	w.bitWriter.Write(uint32(table.codes[symbol]), uint32(table.lengths[symbol]))

	if category > 0 {
		var additionalBits uint32
		if coef >= 0 {
			additionalBits = uint32(coef)
		} else {
			additionalBits = uint32(coef-1) & ((1 << category) - 1)
		}
		w.bitWriter.Write(additionalBits, uint32(category))
	}
}

func (w *JpegWriter) writeCorrectionBits(bits *[]uint8) {
	for _, b := range *bits {
		w.bitWriter.Write(uint32(b), 1)
	}
	*bits = (*bits)[:0]
}

// divPow2 divides by 2^p rounding toward zero (JPEG progressive requirement).
func divPow2(v int16, p uint8) int16 {
	if p == 0 {
		return v
	}
	val := int32(v)
	if val < 0 {
		val += (1 << p) - 1
	}
	return int16(val >> p)
}
