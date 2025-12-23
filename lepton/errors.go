package lepton

import (
	"errors"
	"fmt"
)

// ExitCode represents categorized error codes
type ExitCode int

const (
	ExitCodeAssertionFailure ExitCode = 1
	ExitCodeShortRead        ExitCode = 3
	ExitCodeUnsupported4Colors ExitCode = 4
	ExitCodeCoefficientOutOfRange ExitCode = 6
	ExitCodeStreamInconsistent ExitCode = 7
	ExitCodeProgressiveUnsupported ExitCode = 8
	ExitCodeSamplingBeyondTwoUnsupported ExitCode = 10
	ExitCodeVersionUnsupported ExitCode = 13
	ExitCodeOsError ExitCode = 33
	ExitCodeUnsupportedJpeg ExitCode = 42
	ExitCodeUnsupportedJpegWithZeroIdct0 ExitCode = 43
	ExitCodeInvalidResetCode ExitCode = 44
	ExitCodeInvalidPadding ExitCode = 45
	ExitCodeBadLeptonFile ExitCode = 102
	ExitCodeChannelFailure ExitCode = 103
	ExitCodeIntegerCastOverflow ExitCode = 1000
	ExitCodeVerificationLengthMismatch ExitCode = 1004
	ExitCodeVerificationContentMismatch ExitCode = 1005
	ExitCodeSyntaxError ExitCode = 1006
	ExitCodeFileNotFound ExitCode = 1007
	ExitCodeExternalVerificationFailed ExitCode = 1008
	ExitCodeOutOfMemory ExitCode = 2000
)

func (e ExitCode) String() string {
	switch e {
	case ExitCodeAssertionFailure:
		return "AssertionFailure"
	case ExitCodeShortRead:
		return "ShortRead"
	case ExitCodeUnsupported4Colors:
		return "Unsupported4Colors"
	case ExitCodeCoefficientOutOfRange:
		return "CoefficientOutOfRange"
	case ExitCodeStreamInconsistent:
		return "StreamInconsistent"
	case ExitCodeProgressiveUnsupported:
		return "ProgressiveUnsupported"
	case ExitCodeSamplingBeyondTwoUnsupported:
		return "SamplingBeyondTwoUnsupported"
	case ExitCodeVersionUnsupported:
		return "VersionUnsupported"
	case ExitCodeOsError:
		return "OsError"
	case ExitCodeUnsupportedJpeg:
		return "UnsupportedJpeg"
	case ExitCodeUnsupportedJpegWithZeroIdct0:
		return "UnsupportedJpegWithZeroIdct0"
	case ExitCodeInvalidResetCode:
		return "InvalidResetCode"
	case ExitCodeInvalidPadding:
		return "InvalidPadding"
	case ExitCodeBadLeptonFile:
		return "BadLeptonFile"
	case ExitCodeChannelFailure:
		return "ChannelFailure"
	case ExitCodeIntegerCastOverflow:
		return "IntegerCastOverflow"
	case ExitCodeVerificationLengthMismatch:
		return "VerificationLengthMismatch"
	case ExitCodeVerificationContentMismatch:
		return "VerificationContentMismatch"
	case ExitCodeSyntaxError:
		return "SyntaxError"
	case ExitCodeFileNotFound:
		return "FileNotFound"
	case ExitCodeExternalVerificationFailed:
		return "ExternalVerificationFailed"
	case ExitCodeOutOfMemory:
		return "OutOfMemory"
	default:
		return fmt.Sprintf("ExitCode(%d)", int(e))
	}
}

// LeptonError represents an error from Lepton processing
type LeptonError struct {
	Code    ExitCode
	Message string
}

func (e *LeptonError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// NewLeptonError creates a new LeptonError
func NewLeptonError(code ExitCode, message string) *LeptonError {
	return &LeptonError{Code: code, Message: message}
}

// ErrExitCode creates a LeptonError and returns it
func ErrExitCode(code ExitCode, message string) error {
	return &LeptonError{Code: code, Message: message}
}

// IsLeptonError checks if an error is a LeptonError and returns it
func IsLeptonError(err error) (*LeptonError, bool) {
	var lepErr *LeptonError
	if errors.As(err, &lepErr) {
		return lepErr, true
	}
	return nil, false
}

// Common errors
var (
	ErrShortRead = &LeptonError{Code: ExitCodeShortRead, Message: "short read"}
	ErrStreamInconsistent = &LeptonError{Code: ExitCodeStreamInconsistent, Message: "stream inconsistent"}
	ErrBadLeptonFile = &LeptonError{Code: ExitCodeBadLeptonFile, Message: "bad lepton file"}
)
