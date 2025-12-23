package lepton

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"io"
)

// Encode compresses a JPEG image to Lepton format
func Encode(reader io.Reader, writer io.Writer) error {
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

	// Create a single thread handoff for the entire image
	handoff := ThreadHandoff{
		LumaYStart:      0,
		LumaYEnd:        lumaHeight,
		SegmentSize:     0, // Will be filled in after encoding
		OverhangByte:    0,
		NumOverhangBits: 0,
	}

	// Set up header flags
	jpegResult.Header.Use16BitDCEstimate = true
	jpegResult.Header.Use16BitAdvPredict = true

	// Encode the image data to a buffer first so we know the size
	var encodedData bytes.Buffer
	encoder, err := NewLeptonEncoder(&encodedData, jpegResult.Header)
	if err != nil {
		return err
	}

	if err := encoder.EncodeRowRange(
		quantizationTables,
		jpegResult.ImageData,
		0,
		lumaHeight,
	); err != nil {
		return err
	}

	if err := encoder.Finish(); err != nil {
		return err
	}

	// Now multiplex the encoded data (for single thread, simple format)
	multiplexedData := multiplexSingleThread(encodedData.Bytes())

	// Write Lepton header (includes CMP marker)
	headerSize, compressedHeaderSize, err := writeLeptonHeader(writer, jpegResult, []ThreadHandoff{handoff}, len(jpegData))
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

// multiplexSingleThread wraps encoded data in the multiplexer format for a single thread
func multiplexSingleThread(data []byte) []byte {
	var result bytes.Buffer

	// For single-thread encoding, we wrap data in variable-length blocks
	// Header byte format: lower 4 bits = thread ID (0), upper 4 bits = 0 for variable length
	// Followed by 2 bytes little-endian length-1
	pos := 0
	for pos < len(data) {
		// Use blocks of up to 65536 bytes
		blockSize := len(data) - pos
		if blockSize > 65536 {
			blockSize = 65536
		}

		// Write header byte (thread 0, variable length)
		result.WriteByte(0)

		// Write length-1 in little endian
		lenMinus1 := uint16(blockSize - 1)
		result.WriteByte(byte(lenMinus1 & 0xff))
		result.WriteByte(byte(lenMinus1 >> 8))

		// Write data
		result.Write(data[pos : pos+blockSize])

		pos += blockSize
	}

	return result.Bytes()
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

	// Byte 15: Encoder version
	fixedHeader[15] = 0x01

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
