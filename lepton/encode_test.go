package lepton

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestEncodeBasicImages tests roundtrip encoding: JPEG -> Lepton -> JPEG
func TestEncodeBasicImages(t *testing.T) {
	testCases := []string{
		"tiny",
		"android",
		"iphone",
		"grayscale",
	}

	imagesDir := "../rust/images"

	for _, tc := range testCases {
		t.Run(tc, func(t *testing.T) {
			jpegPath := filepath.Join(imagesDir, tc+".jpg")

			if _, err := os.Stat(jpegPath); os.IsNotExist(err) {
				t.Fatalf("JPEG file not found: %s", jpegPath)
			}

			// Read original JPEG
			originalJpeg, err := os.ReadFile(jpegPath)
			if err != nil {
				t.Fatalf("Failed to read original JPEG: %v", err)
			}

			// Encode to Lepton
			var leptonData bytes.Buffer
			if err := Encode(bytes.NewReader(originalJpeg), &leptonData); err != nil {
				t.Fatalf("Failed to encode to Lepton: %v", err)
			}

			// Decode back to JPEG
			decodedJpeg, err := DecodeLeptonBytes(leptonData.Bytes())
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

// TestEncodeBaselineImages tests encoding of baseline (non-progressive) JPEGs
// This matches Rust's verify_encode test for baseline images
func TestEncodeBaselineImages(t *testing.T) {
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
		"slrcity",
		"slrhills",
		"slrindoor",
		"tiny",
		"trailingrst",
		"trailingrst2",
		"trunc",
		"truncate4",
	}

	imagesDir := "../rust/images"

	for _, tc := range testCases {
		t.Run(tc, func(t *testing.T) {
			jpegPath := filepath.Join(imagesDir, tc+".jpg")

			// Skip if file doesn't exist
			if _, err := os.Stat(jpegPath); os.IsNotExist(err) {
				t.Fatalf("JPEG file not found: %s", jpegPath)
			}

			// Read original JPEG
			originalJpeg, err := os.ReadFile(jpegPath)
			if err != nil {
				t.Fatalf("Failed to read original JPEG: %v", err)
			}

			// Encode to Lepton
			var leptonData bytes.Buffer
			if err := Encode(bytes.NewReader(originalJpeg), &leptonData); err != nil {
				t.Fatalf("Failed to encode to Lepton: %v", err)
			}

			// Decode back to JPEG
			decodedJpeg, err := DecodeLeptonBytes(leptonData.Bytes())
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

// TestEncodeProgressiveImages tests encoding of progressive JPEGs
// This matches Rust's verify_encode test for progressive images
func TestEncodeProgressiveImages(t *testing.T) {
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
			jpegPath := filepath.Join(imagesDir, tc+".jpg")

			// Skip if file doesn't exist
			if _, err := os.Stat(jpegPath); os.IsNotExist(err) {
				t.Fatalf("JPEG file not found: %s", jpegPath)
			}

			// Read original JPEG
			originalJpeg, err := os.ReadFile(jpegPath)
			if err != nil {
				t.Fatalf("Failed to read original JPEG: %v", err)
			}

			// Encode to Lepton
			var leptonData bytes.Buffer
			if err := Encode(bytes.NewReader(originalJpeg), &leptonData); err != nil {
				t.Fatalf("Failed to encode to Lepton: %v", err)
			}

			// Decode back to JPEG
			decodedJpeg, err := DecodeLeptonBytes(leptonData.Bytes())
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

// TestEncodeVerify tests the EncodeVerify function
func TestEncodeVerify(t *testing.T) {
	testCases := []string{
		"tiny",
		"android",
		"iphone",
	}

	imagesDir := "../rust/images"

	for _, tc := range testCases {
		t.Run(tc, func(t *testing.T) {
			jpegPath := filepath.Join(imagesDir, tc+".jpg")

			if _, err := os.Stat(jpegPath); os.IsNotExist(err) {
				t.Fatalf("JPEG file not found: %s", jpegPath)
			}

			originalJpeg, err := os.ReadFile(jpegPath)
			if err != nil {
				t.Fatalf("Failed to read original JPEG: %v", err)
			}

			// EncodeVerify should return encoded data and not error
			leptonData, err := EncodeVerify(originalJpeg)
			if err != nil {
				t.Fatalf("EncodeVerify failed: %v", err)
			}

			if len(leptonData) == 0 {
				t.Error("EncodeVerify returned empty data")
			}
		})
	}
}

// TestEncodeCompareWithRust tests that our encoding produces output that can be decoded
// and matches the original JPEG
func TestEncodeCompareWithRust(t *testing.T) {
	// Test images that have .lep files from the Rust implementation
	testCases := []string{
		"tiny",
		"android",
		"iphone",
		"grayscale",
	}

	imagesDir := "../rust/images"

	for _, tc := range testCases {
		t.Run(tc, func(t *testing.T) {
			jpegPath := filepath.Join(imagesDir, tc+".jpg")
			leptonPath := filepath.Join(imagesDir, tc+".lep")

			// Skip if files don't exist
			if _, err := os.Stat(jpegPath); os.IsNotExist(err) {
				t.Errorf("JPEG file not found: %s", jpegPath)
			}
			if _, err := os.Stat(leptonPath); os.IsNotExist(err) {
				t.Errorf("Lepton file not found: %s", leptonPath)
			}

			// Read original JPEG
			originalJpeg, err := os.ReadFile(jpegPath)
			if err != nil {
				t.Fatalf("Failed to read original JPEG: %v", err)
			}

			// Read existing Lepton file from Rust
			rustLepton, err := os.ReadFile(leptonPath)
			if err != nil {
				t.Fatalf("Failed to read Rust Lepton file: %v", err)
			}

			// Decode the Rust-encoded lepton to get the original JPEG
			rustDecoded, err := DecodeLeptonBytes(rustLepton)
			if err != nil {
				t.Fatalf("Failed to decode Rust Lepton: %v", err)
			}

			// Encode with Go
			var goLepton bytes.Buffer
			if err := Encode(bytes.NewReader(originalJpeg), &goLepton); err != nil {
				t.Fatalf("Failed to encode with Go: %v", err)
			}

			// Decode Go-encoded lepton
			goDecoded, err := DecodeLeptonBytes(goLepton.Bytes())
			if err != nil {
				t.Fatalf("Failed to decode Go Lepton: %v", err)
			}

			// Both should produce the same JPEG
			if !bytes.Equal(rustDecoded, goDecoded) {
				t.Error("Rust and Go decoders produced different results")
			}

			// And both should match the original
			if !bytes.Equal(originalJpeg, goDecoded) {
				t.Error("Go roundtrip did not produce original JPEG")
			}
		})
	}
}

// TestVPXBoolWriterRoundtrip tests that VPXBoolWriter produces data
// that VPXBoolReader can decode correctly
func TestVPXBoolWriterRoundtrip(t *testing.T) {
	var buf bytes.Buffer

	// Write some bits
	writer, err := NewVPXBoolWriter(&buf)
	if err != nil {
		t.Fatalf("Failed to create writer: %v", err)
	}

	// Create a branch and write some bits
	branch := NewBranch()
	if err := writer.PutBit(true, &branch); err != nil {
		t.Fatalf("Failed to write bit: %v", err)
	}
	if err := writer.PutBit(false, &branch); err != nil {
		t.Fatalf("Failed to write bit: %v", err)
	}
	if err := writer.PutBit(true, &branch); err != nil {
		t.Fatalf("Failed to write bit: %v", err)
	}

	if err := writer.Finish(); err != nil {
		t.Fatalf("Failed to finish writer: %v", err)
	}

	// Read back
	reader, err := NewVPXBoolReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Failed to create reader: %v", err)
	}

	branch2 := NewBranch()

	bit1, err := reader.GetBit(&branch2)
	if err != nil {
		t.Fatalf("Failed to read bit: %v", err)
	}
	if !bit1 {
		t.Error("Expected true, got false")
	}

	bit2, err := reader.GetBit(&branch2)
	if err != nil {
		t.Fatalf("Failed to read bit: %v", err)
	}
	if bit2 {
		t.Error("Expected false, got true")
	}

	bit3, err := reader.GetBit(&branch2)
	if err != nil {
		t.Fatalf("Failed to read bit: %v", err)
	}
	if !bit3 {
		t.Error("Expected true, got false")
	}
}

// TestVPXBoolWriterGridRoundtrip tests grid encoding/decoding
func TestVPXBoolWriterGridRoundtrip(t *testing.T) {
	testValues := []uint8{0, 1, 7, 15}

	for _, val := range testValues {
		t.Run(string(rune('0'+val)), func(t *testing.T) {
			var buf bytes.Buffer

			writer, err := NewVPXBoolWriter(&buf)
			if err != nil {
				t.Fatalf("Failed to create writer: %v", err)
			}

			branches := make([]Branch, 16)
			for i := range branches {
				branches[i] = NewBranch()
			}

			if err := writer.PutGrid(val, branches); err != nil {
				t.Fatalf("Failed to write grid: %v", err)
			}

			if err := writer.Finish(); err != nil {
				t.Fatalf("Failed to finish writer: %v", err)
			}

			// Read back
			reader, err := NewVPXBoolReader(bytes.NewReader(buf.Bytes()))
			if err != nil {
				t.Fatalf("Failed to create reader: %v", err)
			}

			branches2 := make([]Branch, 16)
			for i := range branches2 {
				branches2[i] = NewBranch()
			}

			readVal, err := reader.GetGrid(branches2)
			if err != nil {
				t.Fatalf("Failed to read grid: %v", err)
			}

			if readVal != int(val) {
				t.Errorf("Expected %d, got %d", val, readVal)
			}
		})
	}
}
