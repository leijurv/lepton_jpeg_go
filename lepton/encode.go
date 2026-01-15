package lepton

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"io"
	"sync"
)

// DefaultMaxThreads matches Rust's default max_partitions (8)
const DefaultMaxThreads = 8

// Encode compresses a JPEG image to Lepton format
// Uses multi-threading based on restart intervals found in the JPEG
func Encode(reader io.Reader, writer io.Writer) error {
	return EncodeWithThreads(reader, writer, DefaultMaxThreads)
}

// EncodeWithThreads compresses a JPEG image to Lepton format using up to maxThreads
func EncodeWithThreads(reader io.Reader, writer io.Writer, maxThreads int) error {
	// Read all JPEG data (needed for header size)
	jpegData, err := io.ReadAll(reader)
	if err != nil {
		return err
	}

	// Parse the JPEG
	jpegResult, err := ReadJpegFile(bytes.NewReader(jpegData))
	if err != nil {
		return err
	}

	// Create quantization tables
	quantizationTables := make([]*QuantizationTables, jpegResult.Header.Cmpc)
	for i := 0; i < jpegResult.Header.Cmpc; i++ {
		qtIdx := jpegResult.Header.CmpInfo[i].QTableIndex
		quantizationTables[i] = NewQuantizationTables(jpegResult.Header.QTables[qtIdx])
	}

	// Calculate luma height for encoding
	lumaHeight := jpegResult.Header.CmpInfo[0].Bcv

	// Calculate segment size: this is the size of the JPEG scan data
	// (original file minus header minus garbage data including EOI)
	segmentSize := len(jpegData) - len(jpegResult.RawHeader) - len(jpegResult.GarbageData)
	if segmentSize < 0 {
		segmentSize = 0
	}

	// Set up header flags
	jpegResult.Header.Use16BitDCEstimate = true
	jpegResult.Header.Use16BitAdvPredict = true

	// Determine number of threads based on partition count (matching Rust behavior)
	// Rust uses the number of restart intervals (partitions) from the JPEG, not image dimensions
	numPartitions := len(jpegResult.Partitions)
	if numPartitions == 0 {
		numPartitions = 1 // If no partitions, treat as single partition
	}
	numThreads := getNumberOfThreadsForEncoding(numPartitions, segmentSize, maxThreads)

	// Split rows into thread partitions using original JPEG partition info if available
	// For progressive JPEGs, Rust uses the full file size as segment size (not just scan data)
	segmentSizeForHandoffs := segmentSize
	if jpegResult.Header.JpegType == JpegTypeProgressive {
		segmentSizeForHandoffs = len(jpegData)
	}
	handoffs := splitPartitionsToThreads(jpegResult.Partitions, lumaHeight, numThreads, segmentSizeForHandoffs, jpegResult.Header.JpegType)

	// Encode partitions in parallel
	encodedPartitions := make([][]byte, numThreads)
	var wg sync.WaitGroup
	var encodeErr error
	var errMu sync.Mutex

	for threadID := 0; threadID < numThreads; threadID++ {
		wg.Add(1)
		go func(tid int) {
			defer wg.Done()

			var buf bytes.Buffer
			encoder, err := NewLeptonEncoder(&buf, jpegResult.Header)
			if err != nil {
				errMu.Lock()
				if encodeErr == nil {
					encodeErr = err
				}
				errMu.Unlock()
				return
			}

			if err := encoder.EncodeRowRange(
				quantizationTables,
				jpegResult.ImageData,
				handoffs[tid].LumaYStart,
				handoffs[tid].LumaYEnd,
			); err != nil {
				errMu.Lock()
				if encodeErr == nil {
					encodeErr = err
				}
				errMu.Unlock()
				return
			}

			if err := encoder.Finish(); err != nil {
				errMu.Lock()
				if encodeErr == nil {
					encodeErr = err
				}
				errMu.Unlock()
				return
			}

			encodedPartitions[tid] = buf.Bytes()
		}(threadID)
	}

	wg.Wait()

	if encodeErr != nil {
		return encodeErr
	}

	// Multiplex the encoded data from all threads
	multiplexedData := multiplexEncodedData(encodedPartitions)

	// Write Lepton header (includes CMP marker)
	headerSize, compressedHeaderSize, err := writeLeptonHeader(writer, jpegResult, handoffs, len(jpegData))
	if err != nil {
		return err
	}

	// Write the multiplexed data
	if _, err := writer.Write(multiplexedData); err != nil {
		return err
	}

	// Write final file size
	// Total size = 28 (fixed header) + compressed header + 3 (CMP) + multiplexed data + 4 (footer)
	finalSize := uint32(28 + compressedHeaderSize + 3 + len(multiplexedData) + 4)
	_ = headerSize // unused but kept for clarity
	if err := binary.Write(writer, binary.LittleEndian, finalSize); err != nil {
		return err
	}

	return nil
}

// writeLeptonHeader writes the Lepton file header
// Returns the header size and compressed header size
func writeLeptonHeader(writer io.Writer, result *JpegReadResult, handoffs []ThreadHandoff, originalJpegSize int) (int, int, error) {
	// Build the uncompressed header data
	var headerData bytes.Buffer

	// HDR marker + raw JPEG header (without SOI - decoder adds it)
	// The RawHeader from parsing the JPEG includes SOI (ff d8), but the
	// Lepton format expects the header WITHOUT SOI since the decoder writes SOI separately
	rawHeaderWithoutSOI := result.RawHeader
	if len(rawHeaderWithoutSOI) >= 2 && rawHeaderWithoutSOI[0] == 0xff && rawHeaderWithoutSOI[1] == 0xd8 {
		rawHeaderWithoutSOI = rawHeaderWithoutSOI[2:]
	}
	headerData.Write(LeptonHeaderMarker[:])
	binary.Write(&headerData, binary.LittleEndian, uint32(len(rawHeaderWithoutSOI)))
	headerData.Write(rawHeaderWithoutSOI)

	// P0D marker + pad bit
	headerData.Write(LeptonHeaderPadMarker[:])
	padBit := uint8(0)
	if result.PadBit != nil {
		padBit = *result.PadBit
	}
	headerData.WriteByte(padBit)

	// HH marker + thread handoffs
	headerData.Write(LeptonHeaderLumaSplitMarker[:])
	headerData.WriteByte(byte(len(handoffs)))
	for _, h := range handoffs {
		// LumaYStart is stored as uint16 in the file format
		binary.Write(&headerData, binary.LittleEndian, uint16(h.LumaYStart))
		binary.Write(&headerData, binary.LittleEndian, h.SegmentSize)
		headerData.WriteByte(h.OverhangByte)
		headerData.WriteByte(h.NumOverhangBits)
		// LastDC array: 4 values stored as int16
		for i := 0; i < 4; i++ {
			binary.Write(&headerData, binary.LittleEndian, h.LastDC[i])
		}
	}

	// GRB marker + garbage data (always include EOI if no garbage)
	garbage := result.GarbageData
	if len(garbage) == 0 {
		garbage = []byte{0xFF, 0xD9} // EOI marker
	}
	headerData.Write(LeptonHeaderGarbageMarker[:])
	binary.Write(&headerData, binary.LittleEndian, uint32(len(garbage)))
	headerData.Write(garbage)

	// Compress the header
	var compressedHeader bytes.Buffer
	zlibWriter := zlib.NewWriter(&compressedHeader)
	zlibWriter.Write(headerData.Bytes())
	zlibWriter.Close()

	// Write fixed header (28 bytes)
	fixedHeader := make([]byte, 28)

	// Bytes 0-1: Magic number
	fixedHeader[0] = LeptonFileHeader[0]
	fixedHeader[1] = LeptonFileHeader[1]

	// Byte 2: Version
	fixedHeader[2] = LeptonVersion

	// Byte 3: JPEG type
	if result.Header.JpegType == JpegTypeProgressive {
		fixedHeader[3] = LeptonHeaderProgressiveJpegType[0]
	} else {
		fixedHeader[3] = LeptonHeaderBaselineJpegType[0]
	}

	// Byte 4: Number of threads
	fixedHeader[4] = byte(len(handoffs))

	// Bytes 5-7: Reserved (zeros)

	// Bytes 8-9: 'MS' marker for extended info
	fixedHeader[8] = 'M'
	fixedHeader[9] = 'S'

	// Bytes 10-13: Uncompressed header size
	binary.LittleEndian.PutUint32(fixedHeader[10:14], uint32(headerData.Len()))

	// Byte 14: Flags (0x83 = 0x80 | 0x01 | 0x02 for both 16-bit options)
	fixedHeader[14] = 0x83

	// Byte 15: Encoder version (matching Rust 0.5.6 = 56 = 0x38)
	fixedHeader[15] = 0x38

	// Bytes 16-19: Git revision (zeros)

	// Bytes 20-23: Original JPEG file size
	binary.LittleEndian.PutUint32(fixedHeader[20:24], uint32(originalJpegSize))

	// Bytes 24-27: Compressed header size
	binary.LittleEndian.PutUint32(fixedHeader[24:28], uint32(compressedHeader.Len()))

	// Write fixed header
	if _, err := writer.Write(fixedHeader); err != nil {
		return 0, 0, err
	}

	// Write compressed header
	if _, err := writer.Write(compressedHeader.Bytes()); err != nil {
		return 0, 0, err
	}

	// Write completion marker (CMP)
	if _, err := writer.Write(LeptonHeaderCompletionMarker[:]); err != nil {
		return 0, 0, err
	}

	return 28 + compressedHeader.Len(), compressedHeader.Len(), nil
}

// countingWriter wraps a writer and counts bytes written
type countingWriter struct {
	writer io.Writer
	count  int
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.count += n
	return n, err
}

// getNumberOfThreadsForEncoding determines the number of threads to use based on file size
// This matches the Rust implementation in lepton_file_writer.rs
func getNumberOfThreadsForEncoding(numRows, framebufferByteSize, maxThreadsToUse int) int {
	numThreads := maxThreadsToUse
	if numThreads > MaxThreadsSupportedByLeptonFormat {
		numThreads = MaxThreadsSupportedByLeptonFormat
	}

	// Need at least 2 rows per thread
	if numRows/2 < numThreads {
		numThreads = numRows / 2
		if numThreads < 1 {
			numThreads = 1
		}
	}

	// Reduce threads for small files
	if framebufferByteSize < SmallFileBytesPerEncodingThread {
		numThreads = 1
	} else if framebufferByteSize < SmallFileBytesPerEncodingThread*2 {
		if numThreads > 2 {
			numThreads = 2
		}
	} else if framebufferByteSize < SmallFileBytesPerEncodingThread*4 {
		if numThreads > 4 {
			numThreads = 4
		}
	}

	return numThreads
}

// splitPartitionsToThreads merges JPEG partitions into thread handoffs
// This matches the Rust split_row_handoffs_to_threads logic
// totalSegmentSize is the total JPEG scan data size (for calculating partition sizes)
// jpegType indicates baseline or progressive - progressive uses simplified handoffs
func splitPartitionsToThreads(partitions []JpegPartition, lumaHeight uint32, numThreads int, totalSegmentSize int, jpegType JpegType) []ThreadHandoff {
	// If no partitions or single thread, create one covering entire image
	if len(partitions) == 0 || numThreads <= 1 {
		return []ThreadHandoff{{
			LumaYStart:  0,
			LumaYEnd:    lumaHeight,
			SegmentSize: uint32(totalSegmentSize),
		}}
	}

	numPartitions := len(partitions)
	handoffs := make([]ThreadHandoff, numThreads)

	// Split partitions evenly among threads (Rust's simplified split logic)
	partitionsPerThread := float32(numPartitions) / float32(numThreads)

	// Calculate split indices (same as Rust)
	splitIndices := make([]int, numThreads-1)
	for i := 0; i < numThreads-1; i++ {
		splitIndices[i] = int(partitionsPerThread * float32(i+1))
	}

	for i := 0; i < numThreads; i++ {
		var beginPartition, endPartition int
		if i == 0 {
			beginPartition = 0
		} else {
			beginPartition = splitIndices[i-1] + 1
		}
		if i == numThreads-1 {
			endPartition = numPartitions - 1
		} else {
			endPartition = splitIndices[i]
		}

		// For progressive JPEGs, Rust uses simplified handoffs:
		// - All segment sizes are 0 except the last thread (which gets the total)
		// - All overhang/lastDC values are zeroed
		// This is because progressive JPEGs have multiple scans and partition info
		// from the first scan doesn't apply to the encoded output.
		if jpegType == JpegTypeProgressive {
			handoffs[i] = ThreadHandoff{
				LumaYStart: partitions[beginPartition].LumaYStart,
				LumaYEnd:   partitions[endPartition].LumaYEnd,
				// SegmentSize, OverhangByte, NumOverhangBits, LastDC all stay zero
			}
			// Only the last thread gets the total segment size
			if i == numThreads-1 {
				handoffs[i].SegmentSize = uint32(totalSegmentSize)
			}
			continue
		}

		// For baseline JPEGs, use actual partition positions for segment size calculation
		var combinedSize int64
		if endPartition == numPartitions-1 {
			// Last partition extends to end of segment
			combinedSize = int64(totalSegmentSize) - partitions[beginPartition].Position
		} else {
			// Segment size is from begin partition to start of next partition after end
			combinedSize = partitions[endPartition+1].Position - partitions[beginPartition].Position
		}

		handoffs[i] = ThreadHandoff{
			LumaYStart:      partitions[beginPartition].LumaYStart,
			LumaYEnd:        partitions[endPartition].LumaYEnd,
			SegmentSize:     uint32(combinedSize),
			OverhangByte:    partitions[beginPartition].OverhangByte,
			NumOverhangBits: partitions[beginPartition].NumOverhangBits,
			LastDC:          partitions[beginPartition].LastDC,
		}
	}

	return handoffs
}

// encodedPartition holds the result of encoding a single partition
type encodedPartition struct {
	threadID int
	data     []byte
	err      error
}

// multiplexEncodedData interleaves encoded data from multiple threads
// using the same block format as Rust (round-robin with 64KB blocks)
func multiplexEncodedData(partitions [][]byte) []byte {
	var result bytes.Buffer

	// Track position in each partition
	positions := make([]int, len(partitions))
	const blockSize = 65536

	// Round-robin through partitions
	for {
		allDone := true
		for threadID := 0; threadID < len(partitions); threadID++ {
			remaining := len(partitions[threadID]) - positions[threadID]
			if remaining <= 0 {
				continue
			}
			allDone = false

			// Determine block size for this write
			writeSize := remaining
			if writeSize > blockSize {
				writeSize = blockSize
			}

			// Write block header
			tid := byte(threadID)
			lenMinus1 := writeSize - 1

			// Check if length is a special power of 2 (4096, 16384, 65536)
			if lenMinus1 == 4095 || lenMinus1 == 16383 || lenMinus1 == 65535 {
				// Compact header: single byte
				// log2(4096)=12, log2(16384)=14, log2(65536)=16
				// Formula: (log2(len)/2 - 4) << 4 | tid
				var shift byte
				switch lenMinus1 {
				case 4095:
					shift = 1 // (12/2 - 4) = 2, but Rust uses (ilog2 >> 1) - 4 = (11 >> 1) - 4 = 1
				case 16383:
					shift = 2 // (14/2 - 4) = 3, but (13 >> 1) - 4 = 2
				case 65535:
					shift = 3 // (16/2 - 4) = 4, but (15 >> 1) - 4 = 3
				}
				result.WriteByte(tid | (shift << 4))
			} else {
				// Variable length header: 3 bytes
				result.WriteByte(tid)
				result.WriteByte(byte(lenMinus1 & 0xff))
				result.WriteByte(byte((lenMinus1 >> 8) & 0xff))
			}

			// Write block data
			result.Write(partitions[threadID][positions[threadID] : positions[threadID]+writeSize])
			positions[threadID] += writeSize
		}

		if allDone {
			break
		}
	}

	return result.Bytes()
}

// EncodeVerify encodes JPEG to Lepton and verifies by decoding back
func EncodeVerify(jpegData []byte) ([]byte, error) {
	var leptonData bytes.Buffer

	if err := Encode(bytes.NewReader(jpegData), &leptonData); err != nil {
		return nil, err
	}

	// Verify by decoding
	decoded, err := DecodeLeptonBytes(leptonData.Bytes())
	if err != nil {
		return nil, err
	}

	// Compare
	if !bytes.Equal(jpegData, decoded) {
		return nil, NewLeptonError(ExitCodeVerificationContentMismatch, "verification failed")
	}

	return leptonData.Bytes(), nil
}
