package lepton

import (
	"bytes"
	"fmt"
	"io"
)

// limitedWriter wraps a writer and limits output to a maximum size
type limitedWriter struct {
	inner     io.Writer
	remaining int64
	written   int64
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.remaining <= 0 {
		// Silently discard excess data
		return len(p), nil
	}
	toWrite := p
	if int64(len(p)) > w.remaining {
		toWrite = p[:w.remaining]
	}
	n, err := w.inner.Write(toWrite)
	w.remaining -= int64(n)
	w.written += int64(n)
	if err != nil {
		return n, err
	}
	// Report full length written (even if truncated)
	return len(p), nil
}

// DecodeLepton decodes a Lepton file and writes the reconstructed JPEG to output
func DecodeLepton(input io.Reader, output io.Writer) error {
	// Read and parse the Lepton header
	header, err := ReadLeptonHeader(input)
	if err != nil {
		return fmt.Errorf("failed to read Lepton header: %w", err)
	}

	// Create block-based images for each component
	images := make([]*BlockBasedImage, header.JpegHeader.Cmpc)
	for i := 0; i < header.JpegHeader.Cmpc; i++ {
		ci := &header.JpegHeader.CmpInfo[i]
		luma := &header.JpegHeader.CmpInfo[0]
		images[i] = NewBlockBasedImage(ci, luma)
	}

	// For single-threaded decoding, read the completion marker first,
	// then read all scan data
	completionMarker := make([]byte, 3)
	if _, err := io.ReadFull(input, completionMarker); err != nil {
		return fmt.Errorf("failed to read completion marker: %w", err)
	}

	if !bytes.Equal(completionMarker, LeptonHeaderCompletionMarker[:]) {
		return ErrExitCode(ExitCodeBadLeptonFile,
			fmt.Sprintf("invalid completion marker: %v", completionMarker))
	}

	// Read all remaining data (multiplexed segment data + 4-byte footer)
	remainingData, err := io.ReadAll(input)
	if err != nil {
		return fmt.Errorf("failed to read segment data: %w", err)
	}

	// The last 4 bytes are the file size footer
	if len(remainingData) < 4 {
		return ErrExitCode(ExitCodeBadLeptonFile, "missing file size footer")
	}
	multiplexedData := remainingData[:len(remainingData)-4]

	// Demultiplex the data for each thread
	demuxer := newDemultiplexer(multiplexedData, len(header.ThreadHandoffs))

	// Decode scan data for each thread partition
	for threadIdx := 0; threadIdx < len(header.ThreadHandoffs); threadIdx++ {
		handoff := &header.ThreadHandoffs[threadIdx]

		// Get the demultiplexed segment data for this thread
		segmentData := demuxer.getPartitionData(threadIdx)

		// Decode the segment
		segmentReader := bytes.NewReader(segmentData)
		decoder, err := NewLeptonDecoder(segmentReader, header.JpegHeader)
		if err != nil {
			return fmt.Errorf("failed to create decoder for thread %d: %w", threadIdx, err)
		}

		err = decoder.DecodeRowRange(images, handoff.LumaYStart, handoff.LumaYEnd, handoff.LastDC,
			header.RecoveryInfo.MaxDpos, header.RecoveryInfo.EarlyEofEncountered)
		if err != nil {
			return fmt.Errorf("failed to decode thread %d: %w", threadIdx, err)
		}
	}

	// Wrap output with size limiter to match original file size exactly
	limitedOutput := &limitedWriter{
		inner:     output,
		remaining: int64(header.OriginalFileSize),
	}

	// Reconstruct the JPEG
	jpegWriter, err := NewJpegWriter(header, limitedOutput)
	if err != nil {
		return fmt.Errorf("failed to create JPEG writer: %w", err)
	}

	if err := jpegWriter.WriteJpeg(images); err != nil {
		return fmt.Errorf("failed to write JPEG: %w", err)
	}

	return nil
}

// demultiplexer reads multiplexed segment data and provides demultiplexed data per partition
type demultiplexer struct {
	partitionData [][]byte
}

// newDemultiplexer creates a demultiplexer from multiplexed data
func newDemultiplexer(data []byte, numPartitions int) *demultiplexer {
	d := &demultiplexer{
		partitionData: make([][]byte, numPartitions),
	}

	for i := range d.partitionData {
		d.partitionData[i] = make([]byte, 0)
	}

	pos := 0
	for pos < len(data) {
		// Read header byte
		header := data[pos]
		pos++

		partitionID := int(header & 0x0f)
		var blockLen int

		if header < 16 {
			// Variable length: next 2 bytes are length - 1
			if pos+2 > len(data) {
				break
			}
			b0 := int(data[pos])
			b1 := int(data[pos+1])
			pos += 2
			blockLen = (b1 << 8) + b0 + 1
		} else {
			// Fixed length encoded in header
			flags := (header >> 4) & 3
			blockLen = 1024 << (2 * flags)
		}

		// Read block data
		if pos+blockLen > len(data) {
			blockLen = len(data) - pos
		}

		if partitionID < numPartitions {
			d.partitionData[partitionID] = append(d.partitionData[partitionID], data[pos:pos+blockLen]...)
		}
		pos += blockLen
	}

	return d
}

// getPartitionData returns the demultiplexed data for a given partition
func (d *demultiplexer) getPartitionData(partitionID int) []byte {
	if partitionID < len(d.partitionData) {
		return d.partitionData[partitionID]
	}
	return nil
}

// DecodeLeptonBytes is a convenience function that takes byte slices
func DecodeLeptonBytes(input []byte) ([]byte, error) {
	var output bytes.Buffer
	err := DecodeLepton(bytes.NewReader(input), &output)
	if err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
