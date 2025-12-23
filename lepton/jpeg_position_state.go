package lepton

import "fmt"

// JpegPositionState keeps track of position while encoding or decoding a JPEG
type JpegPositionState struct {
	// cmp is the current component
	cmp int

	// mcu is the current minimum coded unit
	mcu uint32

	// csc is the index of component within scan
	csc int

	// sub is the offset within mcu
	sub uint32

	// dpos is the current block position in image for this component
	dpos uint32

	// rstw is the number of blocks left until reset interval
	rstw uint32

	// Eobrun tracks long zero byte runs in progressive images
	Eobrun uint16

	// PrevEobrun tracks if the previous value was also an eobrun
	PrevEobrun uint16
}

// NewJpegPositionState creates a new JpegPositionState
func NewJpegPositionState(jh *JpegHeader, mcu uint32) *JpegPositionState {
	cmp := jh.ScanComponentOrder[0]
	mcumul := jh.CmpInfo[cmp].Sfv * jh.CmpInfo[cmp].Sfh

	var rstw uint32
	if jh.RestartInterval != 0 {
		rstw = uint32(jh.RestartInterval) - (mcu % uint32(jh.RestartInterval))
	}

	return &JpegPositionState{
		cmp:  cmp,
		mcu:  mcu,
		csc:  0,
		sub:  0,
		dpos: mcu * mcumul,
		rstw: rstw,
	}
}

// GetMcu returns the current MCU
func (s *JpegPositionState) GetMcu() uint32 {
	return s.mcu
}

// GetDpos returns the current block position
func (s *JpegPositionState) GetDpos() uint32 {
	return s.dpos
}

// GetCmp returns the current component
func (s *JpegPositionState) GetCmp() int {
	return s.cmp
}

// GetCumulativeResetMarkers returns the number of reset markers that have passed
func (s *JpegPositionState) GetCumulativeResetMarkers(jh *JpegHeader) uint32 {
	if s.rstw != 0 {
		return s.mcu / uint32(jh.RestartInterval)
	}
	return 0
}

// ResetRstw resets the restart interval counter
func (s *JpegPositionState) ResetRstw(jh *JpegHeader) {
	s.rstw = uint32(jh.RestartInterval)
	// eobruns don't span reset intervals
	s.PrevEobrun = 0
}

// nextMcuPosNonInterleaved calculates next position for non-interleaved scan
func (s *JpegPositionState) nextMcuPosNonInterleaved(jh *JpegHeader) JpegDecodeStatus {
	// increment position
	s.dpos++

	cmpInfo := &jh.CmpInfo[s.cmp]

	// fix for non interleaved mcu - horizontal
	if cmpInfo.Bch != cmpInfo.Nch && s.dpos%cmpInfo.Bch == cmpInfo.Nch {
		s.dpos += cmpInfo.Bch - cmpInfo.Nch
	}

	// fix for non interleaved mcu - vertical
	if cmpInfo.Bcv != cmpInfo.Ncv && s.dpos/cmpInfo.Bch == cmpInfo.Ncv {
		s.dpos = cmpInfo.Bc
	}

	// now we've updated dpos, update the current MCU to be a fraction of that
	if jh.JpegType == JpegTypeSequential {
		s.mcu = s.dpos / (cmpInfo.Sfv * cmpInfo.Sfh)
	}

	// check position
	if s.dpos >= cmpInfo.Bc {
		return ScanCompleted
	} else if jh.RestartInterval > 0 {
		s.rstw--
		if s.rstw == 0 {
			return RestartIntervalExpired
		}
	}

	return DecodeInProgress
}

// NextMcuPos calculates next position for MCU
func (s *JpegPositionState) NextMcuPos(jh *JpegHeader) JpegDecodeStatus {
	// if there is just one component, go the simple route
	if len(jh.ScanComponentOrder) == 1 {
		return s.nextMcuPosNonInterleaved(jh)
	}

	sta := DecodeInProgress
	localMcuh := jh.Mcuh
	localMcu := s.mcu
	localCmp := s.cmp

	// increment all counts where needed
	s.sub++
	localSub := s.sub
	if localSub >= jh.CmpInfo[localCmp].Mbs {
		s.sub = 0
		localSub = 0

		s.csc++

		if s.csc >= len(jh.ScanComponentOrder) {
			s.csc = 0
			s.cmp = jh.ScanComponentOrder[0]
			localCmp = s.cmp

			s.mcu++
			localMcu = s.mcu

			mcuc := jh.Mcuh * jh.Mcuv
			if localMcu >= mcuc {
				sta = ScanCompleted
			} else if jh.RestartInterval > 0 {
				s.rstw--
				if s.rstw == 0 {
					sta = RestartIntervalExpired
				}
			}
		} else {
			s.cmp = jh.ScanComponentOrder[s.csc]
			localCmp = s.cmp
		}
	}

	sfh := jh.CmpInfo[localCmp].Sfh
	sfv := jh.CmpInfo[localCmp].Sfv

	// get correct position in image ( x & y )
	if sfh > 1 {
		// to fix mcu order
		mcuOverMcuh := localMcu / localMcuh
		subOverSfv := localSub / sfv
		mcuModMcuh := localMcu - (mcuOverMcuh * localMcuh)
		subModSfv := localSub - (subOverSfv * sfv)
		localDpos := (mcuOverMcuh * sfh) + subOverSfv

		localDpos *= jh.CmpInfo[localCmp].Bch
		localDpos += (mcuModMcuh * sfv) + subModSfv

		s.dpos = localDpos
	} else if sfv > 1 {
		// simple calculation to speed up things if simple fixing is enough
		s.dpos = (localMcu * jh.CmpInfo[localCmp].Mbs) + localSub
	} else {
		// no calculations needed without subsampling
		s.dpos = s.mcu
	}

	return sta
}

// SkipEobrun skips the eobrun and calculates next position
func (s *JpegPositionState) SkipEobrun(jh *JpegHeader) (JpegDecodeStatus, error) {
	if len(jh.ScanComponentOrder) != 1 {
		panic("SkipEobrun only works for non-interleaved scans")
	}

	if s.Eobrun == 0 {
		return DecodeInProgress, nil
	}

	// compare rst wait counter if needed
	if jh.RestartInterval > 0 {
		if uint32(s.Eobrun) > s.rstw {
			return 0, NewLeptonError(ExitCodeUnsupportedJpeg,
				"skip_eobrun: eob run extends passed end of reset interval")
		}
		s.rstw -= uint32(s.Eobrun)
	}

	cmpInfo := &jh.CmpInfo[s.cmp]

	// fix for non interleaved mcu - horizontal
	if cmpInfo.Bch != cmpInfo.Nch {
		s.dpos += (((s.dpos % cmpInfo.Bch) + uint32(s.Eobrun)) / cmpInfo.Nch) *
			(cmpInfo.Bch - cmpInfo.Nch)
	}

	// fix for non interleaved mcu - vertical
	if cmpInfo.Bcv != cmpInfo.Ncv && s.dpos/cmpInfo.Bch >= cmpInfo.Ncv {
		s.dpos += (cmpInfo.Bcv - cmpInfo.Ncv) * cmpInfo.Bch
	}

	// skip blocks
	s.dpos += uint32(s.Eobrun)

	// reset eobrun
	s.Eobrun = 0

	// check position to see if we are done decoding
	if s.dpos == cmpInfo.Bc {
		return ScanCompleted, nil
	} else if s.dpos > cmpInfo.Bc {
		return 0, NewLeptonError(ExitCodeUnsupportedJpeg,
			"skip_eobrun: position extended passed block count")
	} else if jh.RestartInterval > 0 && s.rstw == 0 {
		return RestartIntervalExpired, nil
	}

	return DecodeInProgress, nil
}

// CheckOptimalEobrun checks if we have optimal eob runs
func (s *JpegPositionState) CheckOptimalEobrun(isCurrentBlockEmpty bool, maxEobRun uint16) error {
	// if we got an empty block, make sure that the previous zero run was as high as it could be
	if isCurrentBlockEmpty {
		if s.PrevEobrun > 0 && s.PrevEobrun < maxEobRun-1 {
			return NewLeptonError(ExitCodeUnsupportedJpeg,
				fmt.Sprintf("non optimal eobruns not supported (could have encoded up to %d zero runs, but only did %d followed by %d)",
					maxEobRun, s.PrevEobrun+1, s.Eobrun+1))
		}
	}

	s.PrevEobrun = s.Eobrun

	return nil
}
