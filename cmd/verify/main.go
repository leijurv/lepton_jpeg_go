package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/leijurv/lepton_jpeg_go/lepton"
)

type testResult struct {
	decompressOK      bool
	compressOK        bool
	roundtripOK       bool
	rustCanCompress   bool // true if Rust can handle this file (when Go fails)
	errMsg            string
	originalLepSize   int // size of original .lep file
	recompressedSize  int // size of recompressed .lep data
}

func main() {
	dirPath := flag.String("dir", "/opt/lepton_dump", "Directory containing .lep files")
	limit := flag.Int("limit", 0, "Limit number of files to test (0 = no limit)")
	workers := flag.Int("workers", 16, "Number of parallel workers")
	verbose := flag.Bool("v", false, "Verbose output")
	testCompress := flag.Bool("compress", false, "Test compressor (decompress -> compress -> decompress)")
	rustBinary := flag.String("rust", "", "Path to Rust lepton binary (for comparing Go failures)")
	flag.Parse()

	// Try to find Rust binary if not specified
	rustPath := *rustBinary
	if rustPath == "" {
		// Try common locations
		candidates := []string{
			"./rust/target/release/lepton_jpeg_util",
			"../rust/target/release/lepton_jpeg_util",
			"/mnt/data/Dropbox/lepton_jpeg_rust/rust/target/release/lepton_jpeg_util",
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				rustPath = c
				break
			}
		}
	}

	files, err := ioutil.ReadDir(*dirPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading directory: %v\n", err)
		os.Exit(1)
	}

	var lepFiles []string
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".lep") {
			lepFiles = append(lepFiles, f.Name())
		}
	}

	if *limit > 0 && len(lepFiles) > *limit {
		lepFiles = lepFiles[:*limit]
	}

	if *testCompress {
		fmt.Printf("Testing %d files with %d workers (compress mode)...\n", len(lepFiles), *workers)
		if rustPath != "" {
			fmt.Printf("Using Rust binary: %s\n", rustPath)
		} else {
			fmt.Println("No Rust binary found - will not compare Go failures with Rust")
		}
	} else {
		fmt.Printf("Testing %d files with %d workers...\n", len(lepFiles), *workers)
	}

	var decompressPass, decompressFail int64
	var compressPass, compressFail int64
	var roundtripPass, roundtripFail int64
	var goOnlyFail int64      // Files that fail in Go but work in Rust (Go bugs)
	var rustAlsoFail int64    // Files that fail in both Go and Rust (Rust limitations)
	var skipped int64
	var mu sync.Mutex
	var failedFiles []string
	var compressFailedFiles []string
	var goOnlyFailedFiles []string
	var processed int64
	var totalOriginalLepBytes int64   // sum of original .lep sizes for ratio calculation
	var totalRecompressedBytes int64  // sum of recompressed sizes for ratio calculation

	jobs := make(chan string, len(lepFiles))
	var wg sync.WaitGroup

	done := make(chan struct{})
	var statusWg sync.WaitGroup
	statusWg.Add(1)
	go func() {
		defer statusWg.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				n := atomic.LoadInt64(&processed)
				dp := atomic.LoadInt64(&decompressPass)
				df := atomic.LoadInt64(&decompressFail)
				s := atomic.LoadInt64(&skipped)
				if *testCompress {
					cp := atomic.LoadInt64(&compressPass)
					rp := atomic.LoadInt64(&roundtripPass)
					origBytes := atomic.LoadInt64(&totalOriginalLepBytes)
					recompBytes := atomic.LoadInt64(&totalRecompressedBytes)
					ratioStr := "N/A"
					if origBytes > 0 {
						ratioStr = fmt.Sprintf("%.4f", float64(recompBytes)/float64(origBytes))
					}
					fmt.Printf("Progress: %d/%d (decompress: %d/%d, compress: %d, roundtrip: %d, skip: %d, recomp ratio: %s)\n",
						n, len(lepFiles), dp, dp+df, cp, rp, s, ratioStr)
				} else {
					fmt.Printf("Progress: %d/%d processed (%d passed, %d failed, %d skipped)\n",
						n, len(lepFiles), dp, df, s)
				}
			case <-done:
				return
			}
		}
	}()

	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for filename := range jobs {
				result := testFile(*dirPath, filename, *verbose, *testCompress, rustPath)
				atomic.AddInt64(&processed, 1)

				if result.errMsg == "skip" {
					atomic.AddInt64(&skipped, 1)
					continue
				}

				if result.decompressOK {
					atomic.AddInt64(&decompressPass, 1)
				} else {
					atomic.AddInt64(&decompressFail, 1)
					if result.errMsg != "" {
						mu.Lock()
						failedFiles = append(failedFiles, result.errMsg)
						mu.Unlock()
					}
				}

				if *testCompress && result.decompressOK {
					if result.compressOK {
						atomic.AddInt64(&compressPass, 1)
						if result.roundtripOK {
							atomic.AddInt64(&roundtripPass, 1)
							atomic.AddInt64(&totalOriginalLepBytes, int64(result.originalLepSize))
							atomic.AddInt64(&totalRecompressedBytes, int64(result.recompressedSize))
						} else {
							atomic.AddInt64(&roundtripFail, 1)
							if result.errMsg != "" {
								mu.Lock()
								compressFailedFiles = append(compressFailedFiles, result.errMsg)
								mu.Unlock()
							}
						}
					} else {
						atomic.AddInt64(&compressFail, 1)
						// Track whether this is a Go-only failure or Rust also fails
						if result.rustCanCompress {
							atomic.AddInt64(&goOnlyFail, 1)
							if result.errMsg != "" {
								mu.Lock()
								goOnlyFailedFiles = append(goOnlyFailedFiles, result.errMsg)
								mu.Unlock()
							}
						} else {
							atomic.AddInt64(&rustAlsoFail, 1)
						}
						if result.errMsg != "" {
							mu.Lock()
							compressFailedFiles = append(compressFailedFiles, result.errMsg)
							mu.Unlock()
						}
					}
				}
			}
		}()
	}

	for _, f := range lepFiles {
		jobs <- f
	}
	close(jobs)
	wg.Wait()
	close(done)
	statusWg.Wait()

	fmt.Println()
	if *testCompress {
		total := decompressPass + decompressFail
		fmt.Printf("Decompress: %d/%d passed (%.1f%%)\n",
			decompressPass, total, 100*float64(decompressPass)/float64(total))

		if decompressPass > 0 {
			fmt.Printf("Compress:   %d/%d passed (%.1f%%)\n",
				compressPass, decompressPass, 100*float64(compressPass)/float64(decompressPass))
			fmt.Printf("Roundtrip:  %d/%d passed (%.1f%%)\n",
				roundtripPass, decompressPass, 100*float64(roundtripPass)/float64(decompressPass))

			// Show compression ratio for successful roundtrips
			if totalOriginalLepBytes > 0 {
				ratio := float64(totalRecompressedBytes) / float64(totalOriginalLepBytes)
				fmt.Printf("Recompression ratio: %.4f (recompressed %d bytes / original %d bytes)\n",
					ratio, totalRecompressedBytes, totalOriginalLepBytes)
			}

			// Show breakdown of compression failures
			if compressFail > 0 && rustPath != "" {
				fmt.Println()
				fmt.Printf("Compression failures breakdown:\n")
				fmt.Printf("  Go bugs (Rust works):       %d\n", goOnlyFail)
				fmt.Printf("  Rust limitations (both fail): %d\n", rustAlsoFail)
			}
		}

		if skipped > 0 {
			fmt.Printf("\nSkipped: %d\n", skipped)
		}
	} else {
		fmt.Printf("Results: %d passed, %d failed, %d skipped\n", decompressPass, decompressFail, skipped)
	}

	if len(failedFiles) > 0 && len(failedFiles) <= 20 {
		fmt.Println("\nDecompress failed files:")
		for _, f := range failedFiles {
			fmt.Println("  " + f)
		}
	}

	if *testCompress && len(goOnlyFailedFiles) > 0 && len(goOnlyFailedFiles) <= 20 {
		fmt.Println("\nGo-only failures (Rust can handle these - potential Go bugs):")
		for _, f := range goOnlyFailedFiles {
			fmt.Println("  " + f)
		}
	}

	if *testCompress && len(compressFailedFiles) > 0 && len(compressFailedFiles) <= 50 {
		fmt.Println("\nAll compress/roundtrip failed files:")
		for _, f := range compressFailedFiles {
			fmt.Println("  " + f)
		}
	}

	// Force GC to prevent memory buildup
	runtime.GC()
}

func testFile(dirPath, filename string, verbose, testCompress bool, rustPath string) testResult {
	result := testResult{}

	// Extract expected SHA256 from filename
	expectedHash := strings.TrimSuffix(filename, ".lep")
	if len(expectedHash) != 64 {
		result.errMsg = "skip"
		return result
	}

	// Read lepton file
	lepPath := filepath.Join(dirPath, filename)
	lepData, err := ioutil.ReadFile(lepPath)
	if err != nil {
		result.errMsg = fmt.Sprintf("%s: read error: %v", filename, err)
		return result
	}

	// Step 1: Decode lepton -> JPEG
	decoded, err := lepton.DecodeLeptonBytes(lepData)
	if err != nil {
		result.errMsg = fmt.Sprintf("%s: decode error: %v", filename, err)
		return result
	}

	// Verify SHA256
	hash := sha256.Sum256(decoded)
	actualHash := hex.EncodeToString(hash[:])

	if actualHash != expectedHash {
		result.errMsg = fmt.Sprintf("%s: hash mismatch (got %s)", filename, actualHash[:16]+"...")
		return result
	}

	result.decompressOK = true

	if verbose {
		fmt.Printf("DECOMPRESS PASS: %s\n", filename)
	}

	if !testCompress {
		return result
	}

	// Step 2: Compress JPEG -> Lepton
	var recompressed bytes.Buffer
	err = lepton.Encode(bytes.NewReader(decoded), &recompressed)
	if err != nil {
		result.errMsg = fmt.Sprintf("%s: compress error: %v", filename, err)
		// Test if Rust can handle this file
		if rustPath != "" {
			result.rustCanCompress = testRustCompress(decoded, rustPath, verbose)
			if verbose {
				if result.rustCanCompress {
					fmt.Printf("RUST CAN COMPRESS: %s (Go bug)\n", filename)
				} else {
					fmt.Printf("RUST ALSO FAILS: %s (Rust limitation)\n", filename)
				}
			}
		}
		return result
	}

	result.compressOK = true

	if verbose {
		fmt.Printf("COMPRESS PASS: %s (original: %d bytes, recompressed: %d bytes)\n",
			filename, len(lepData), recompressed.Len())
	}

	// Step 3: Decode recompressed lepton -> JPEG
	redecoded, err := lepton.DecodeLeptonBytes(recompressed.Bytes())
	if err != nil {
		result.errMsg = fmt.Sprintf("%s: roundtrip decode error: %v", filename, err)
		return result
	}

	// Verify SHA256 of roundtripped JPEG
	hash2 := sha256.Sum256(redecoded)
	actualHash2 := hex.EncodeToString(hash2[:])

	if actualHash2 != expectedHash {
		result.errMsg = fmt.Sprintf("%s: roundtrip hash mismatch (got %s)", filename, actualHash2[:16]+"...")
		return result
	}

	result.roundtripOK = true
	result.originalLepSize = len(lepData)
	result.recompressedSize = recompressed.Len()

	if verbose {
		fmt.Printf("ROUNDTRIP PASS: %s\n", filename)
	}

	return result
}

// testRustCompress tests if Rust can compress the given JPEG data using stdin/stdout pipes
func testRustCompress(jpegData []byte, rustPath string, verbose bool) bool {
	cmd := exec.Command(rustPath)
	cmd.Stdin = bytes.NewReader(jpegData)

	// Capture stdout (the lepton output) - we just need to know it succeeded
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	// Suppress stderr
	cmd.Stderr = nil

	err := cmd.Run()
	if err != nil {
		return false
	}

	// Check that we got some output (lepton data)
	return stdout.Len() > 0
}
