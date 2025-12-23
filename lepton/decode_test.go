package lepton

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestDecodeBasicImages tests decoding of basic Lepton files
func TestDecodeBasicImages(t *testing.T) {
	testCases := []string{
		"tiny",
		"android",
		"iphone",
		"grayscale",
	}

	imagesDir := "../rust/images"

	for _, tc := range testCases {
		t.Run(tc, func(t *testing.T) {
			leptonPath := filepath.Join(imagesDir, tc+".lep")
			jpegPath := filepath.Join(imagesDir, tc+".jpg")

			// Fatal if files don't exist
			if _, err := os.Stat(leptonPath); os.IsNotExist(err) {
				t.Fatalf("Lepton file not found: %s", leptonPath)
			}
			if _, err := os.Stat(jpegPath); os.IsNotExist(err) {
				t.Fatalf("JPEG file not found: %s", jpegPath)
			}

			// Read original JPEG
			originalJpeg, err := os.ReadFile(jpegPath)
			if err != nil {
				t.Fatalf("Failed to read original JPEG: %v", err)
			}

			// Read Lepton file
			leptonData, err := os.ReadFile(leptonPath)
			if err != nil {
				t.Fatalf("Failed to read Lepton file: %v", err)
			}

			// Decode
			decodedJpeg, err := DecodeLeptonBytes(leptonData)
			if err != nil {
				t.Fatalf("Failed to decode Lepton: %v", err)
			}

			// Compare
			if len(decodedJpeg) != len(originalJpeg) {
				t.Errorf("Length mismatch: decoded %d bytes, original %d bytes",
					len(decodedJpeg), len(originalJpeg))
			}

			if !bytes.Equal(decodedJpeg, originalJpeg) {
				// Find first difference
				for i := 0; i < len(decodedJpeg) && i < len(originalJpeg); i++ {
					if decodedJpeg[i] != originalJpeg[i] {
						t.Errorf("Content mismatch at byte %d: decoded 0x%02x, original 0x%02x",
							i, decodedJpeg[i], originalJpeg[i])
						break
					}
				}
			}
		})
	}
}

// TestDecodeBaselineImages tests decoding non-progressive images from Rust test suite
func TestDecodeBaselineImages(t *testing.T) {
	// Baseline (non-progressive) images from Rust test suite
	testCases := []string{
		"android",
		"androidcrop",
		"androidcropoptions",
		"androidtrail",
		"colorswap",
		"gray2sf",
		"grayscale",
		"hq",
		"iphone",
		"iphonecity",
		"iphonecity_with_16KGarbage",
		"iphonecity_with_1MGarbage",
		"iphonecrop",
		"iphonecrop2",
		"out_of_order_dqt",
		"narrowrst",
		"nofsync",
		"slrcity",
		"slrhills",
		"slrindoor",
		"zeros_in_dqt_tables",
		"tiny",
		"trailingrst",
		"trailingrst2",
		"trunc",
		"eof_and_trailingrst",
		"eof_and_trailinghdrdata",
		"pixelated",
		"truncate4",
	}

	imagesDir := "../rust/images"

	for _, tc := range testCases {
		t.Run(tc, func(t *testing.T) {
			leptonPath := filepath.Join(imagesDir, tc+".lep")
			jpegPath := filepath.Join(imagesDir, tc+".jpg")

			// Fatal if files don't exist
			if _, err := os.Stat(leptonPath); os.IsNotExist(err) {
				t.Fatalf("Lepton file not found: %s", leptonPath)
			}
			if _, err := os.Stat(jpegPath); os.IsNotExist(err) {
				t.Fatalf("JPEG file not found: %s", jpegPath)
			}

			// Read original JPEG
			originalJpeg, err := os.ReadFile(jpegPath)
			if err != nil {
				t.Fatalf("Failed to read original JPEG: %v", err)
			}

			// Read Lepton file
			leptonData, err := os.ReadFile(leptonPath)
			if err != nil {
				t.Fatalf("Failed to read Lepton file: %v", err)
			}

			// Decode
			decodedJpeg, err := DecodeLeptonBytes(leptonData)
			if err != nil {
				t.Fatalf("Failed to decode Lepton: %v", err)
			}

			// Compare
			if len(decodedJpeg) != len(originalJpeg) {
				t.Errorf("Length mismatch: decoded %d bytes, original %d bytes",
					len(decodedJpeg), len(originalJpeg))
			}

			if !bytes.Equal(decodedJpeg, originalJpeg) {
				// Find first difference
				for i := 0; i < len(decodedJpeg) && i < len(originalJpeg); i++ {
					if decodedJpeg[i] != originalJpeg[i] {
						t.Errorf("Content mismatch at byte %d: decoded 0x%02x, original 0x%02x",
							i, decodedJpeg[i], originalJpeg[i])
						break
					}
				}
			}
		})
	}
}

// TestDecodeProgressiveImages tests decoding progressive images from Rust test suite
func TestDecodeProgressiveImages(t *testing.T) {
	// Progressive images from Rust test suite
	testCases := []string{
		"androidprogressive",
		"androidprogressive_garbage",
		"iphoneprogressive",
		"iphoneprogressive2",
		"progressive_late_dht",
	}

	imagesDir := "../rust/images"

	for _, tc := range testCases {
		t.Run(tc, func(t *testing.T) {
			leptonPath := filepath.Join(imagesDir, tc+".lep")
			jpegPath := filepath.Join(imagesDir, tc+".jpg")

			// Fatal if files don't exist
			if _, err := os.Stat(leptonPath); os.IsNotExist(err) {
				t.Fatalf("Lepton file not found: %s", leptonPath)
			}
			if _, err := os.Stat(jpegPath); os.IsNotExist(err) {
				t.Fatalf("JPEG file not found: %s", jpegPath)
			}

			// Read original JPEG
			originalJpeg, err := os.ReadFile(jpegPath)
			if err != nil {
				t.Fatalf("Failed to read original JPEG: %v", err)
			}

			// Read Lepton file
			leptonData, err := os.ReadFile(leptonPath)
			if err != nil {
				t.Fatalf("Failed to read Lepton file: %v", err)
			}

			// Decode
			decodedJpeg, err := DecodeLeptonBytes(leptonData)
			if err != nil {
				t.Fatalf("Failed to decode Lepton: %v", err)
			}

			// Compare
			if len(decodedJpeg) != len(originalJpeg) {
				t.Errorf("Length mismatch: decoded %d bytes, original %d bytes",
					len(decodedJpeg), len(originalJpeg))
			}

			if !bytes.Equal(decodedJpeg, originalJpeg) {
				// Find first difference
				for i := 0; i < len(decodedJpeg) && i < len(originalJpeg); i++ {
					if decodedJpeg[i] != originalJpeg[i] {
						t.Errorf("Content mismatch at byte %d: decoded 0x%02x, original 0x%02x",
							i, decodedJpeg[i], originalJpeg[i])
						break
					}
				}
			}
		})
	}
}

// TestDecodeLeptonHeader tests parsing of Lepton headers
func TestDecodeLeptonHeader(t *testing.T) {
	imagesDir := "../rust/images"
	leptonPath := filepath.Join(imagesDir, "tiny.lep")

	if _, err := os.Stat(leptonPath); os.IsNotExist(err) {
		t.Fatal("Test file not found")
	}

	leptonFile, err := os.Open(leptonPath)
	if err != nil {
		t.Fatalf("Failed to open Lepton file: %v", err)
	}
	defer leptonFile.Close()

	header, err := ReadLeptonHeader(leptonFile)
	if err != nil {
		t.Fatalf("Failed to read Lepton header: %v", err)
	}

	// Verify header fields
	if header.Version != LeptonVersion {
		t.Errorf("Expected version %d, got %d", LeptonVersion, header.Version)
	}

	if header.JpegHeader == nil {
		t.Error("JPEG header is nil")
	}

	if header.JpegHeader.Cmpc < 1 || header.JpegHeader.Cmpc > 4 {
		t.Errorf("Invalid component count: %d", header.JpegHeader.Cmpc)
	}

	t.Logf("Image size: %dx%d", header.JpegHeader.Width, header.JpegHeader.Height)
	t.Logf("Components: %d", header.JpegHeader.Cmpc)
	t.Logf("Thread count: %d", header.ThreadCount)
}

// TestBranch tests the Branch probability tracking
func TestBranch(t *testing.T) {
	b := NewBranch()

	// Initial probability should be 128 (50/50)
	initialProb := b.GetProbability()
	if initialProb != 128 {
		t.Errorf("Expected initial probability 128, got %d", initialProb)
	}

	// Record some false bits and check probability increases
	for i := 0; i < 10; i++ {
		b.RecordAndUpdateBit(false)
	}
	probAfterFalse := b.GetProbability()
	if probAfterFalse <= initialProb {
		t.Errorf("Probability should increase after false bits, got %d", probAfterFalse)
	}

	// Reset and record true bits
	b = NewBranch()
	for i := 0; i < 10; i++ {
		b.RecordAndUpdateBit(true)
	}
	probAfterTrue := b.GetProbability()
	if probAfterTrue >= initialProb {
		t.Errorf("Probability should decrease after true bits, got %d", probAfterTrue)
	}
}

// TestBranchUpdateFalse tests branch updates from Rust test cases
func TestBranchUpdateFalse(t *testing.T) {
	testCases := []struct {
		initial  uint16
		expected uint16
	}{
		{0x0101, 0x0201},
		{0x80ff, 0x81ff},
		{0xff01, 0xff01},
		{0xff02, 0x8101},
		{0xffff, 0x8180},
	}

	for _, tc := range testCases {
		b := Branch{}
		b.SetCounts(tc.initial)
		b.RecordAndUpdateBit(false)
		if b.GetCounts() != tc.expected {
			t.Errorf("For initial 0x%04x + false, expected 0x%04x, got 0x%04x",
				tc.initial, tc.expected, b.GetCounts())
		}
	}
}

// TestBranchUpdateTrue tests branch updates from Rust test cases
func TestBranchUpdateTrue(t *testing.T) {
	testCases := []struct {
		initial  uint16
		expected uint16
	}{
		{0x0101, 0x0102},
		{0xff80, 0xff81},
		{0x01ff, 0x01ff},
		{0x02ff, 0x0181},
		{0xffff, 0x8081},
	}

	for _, tc := range testCases {
		b := Branch{}
		b.SetCounts(tc.initial)
		b.RecordAndUpdateBit(true)
		if b.GetCounts() != tc.expected {
			t.Errorf("For initial 0x%04x + true, expected 0x%04x, got 0x%04x",
				tc.initial, tc.expected, b.GetCounts())
		}
	}
}

// TestAlignedBlock tests the AlignedBlock operations
func TestAlignedBlock(t *testing.T) {
	block := NewAlignedBlock()

	// Test DC coefficient
	block.SetDC(100)
	if block.GetDC() != 100 {
		t.Errorf("Expected DC 100, got %d", block.GetDC())
	}

	// Test coefficient access
	block.SetCoefficient(10, 50)
	if block.GetCoefficient(10) != 50 {
		t.Errorf("Expected coefficient 50, got %d", block.GetCoefficient(10))
	}

	// Test non-zero count in 7x7 interior (rows 1-7, cols 1-7)
	block2 := NewAlignedBlock()
	block2.RawData[9] = 1  // row 1, col 1 - 7x7 interior
	block2.RawData[17] = 2 // row 2, col 1 - 7x7 interior
	block2.RawData[25] = 3 // row 3, col 1 - 7x7 interior
	count := block2.GetCountOfNonZeros7x7()
	if count != 3 {
		t.Errorf("Expected 3 non-zeros, got %d", count)
	}
}

// TestBitWriter tests the BitWriter functionality
func TestBitWriter(t *testing.T) {
	w := NewBitWriter(1024)

	// Write some bits
	w.Write(0x1, 4) // 0001
	w.Write(0x2, 4) // 0010
	w.Write(0x3, 4) // 0011
	w.Write(0x4, 4) // 0100

	// Pad and get result
	w.Pad(0xFF)
	result := w.DetachBuffer()

	// Should be 0x12 0x34
	if len(result) != 2 {
		t.Errorf("Expected 2 bytes, got %d", len(result))
	}
	if result[0] != 0x12 || result[1] != 0x34 {
		t.Errorf("Expected 0x12 0x34, got %02x %02x", result[0], result[1])
	}
}

// TestQuantizationTables tests quantization table creation
func TestQuantizationTables(t *testing.T) {
	// Standard luminance quantization table
	table := [64]uint16{
		16, 11, 10, 16, 24, 40, 51, 61,
		12, 12, 14, 19, 26, 58, 60, 55,
		14, 13, 16, 24, 40, 57, 69, 56,
		14, 17, 22, 29, 51, 87, 80, 62,
		18, 22, 37, 56, 68, 109, 103, 77,
		24, 35, 55, 64, 81, 104, 113, 92,
		49, 64, 78, 87, 103, 121, 120, 101,
		72, 92, 95, 98, 112, 100, 103, 99,
	}

	qt := NewQuantizationTables(table)

	// Verify table was stored (converted to transposed order)
	if qt.GetQ(0) != 16 {
		t.Errorf("Expected Q[0] = 16, got %d", qt.GetQ(0))
	}
}
