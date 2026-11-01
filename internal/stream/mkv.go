package stream

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
)

// Matroska keyframe index. An MKV carries a Cues element listing every video
// keyframe with its timestamp. Reading it costs a couple of small reads (the
// header, then the cues, usually at the end of the file) instead of scanning
// the whole file, and it tells us exactly where ffmpeg's HLS muxer will cut
// segments when copying the video stream. That lets the server hand the
// player a complete, exact playlist up front, so playback and seeking behave
// like a normal file instead of a live stream.

const (
	idEBML            = 0x1A45DFA3
	idSegment         = 0x18538067
	idSeekHead        = 0x114D9B74
	idSeek            = 0x4DBB
	idSeekID          = 0x53AB
	idSeekPosition    = 0x53AC
	idInfo            = 0x1549A966
	idTimestampScale  = 0x2AD7B1
	idDuration        = 0x4489
	idTracks          = 0x1654AE6B
	idTrackEntry      = 0xAE
	idTrackNumber     = 0xD7
	idTrackType       = 0x83
	idCluster         = 0x1F43B675
	idCues            = 0x1C53BB6B
	idCuePoint        = 0xBB
	idCueTime         = 0xB3
	idCueTrackPos     = 0xB7
	idCueTrack        = 0xF7
	idVoid            = 0xEC
	idCRC             = 0xBF
	unknownSize       = -1
	trackTypeVideo    = 1
	maxHeaderScanSize = 64 << 20 // give up on files whose cues aren't findable within this much of the head
)

// Keyframes holds the video keyframe timestamps of a file, in milliseconds,
// and its duration.
type Keyframes struct {
	TimesMs    []int64
	DurationMs int64
}

var errNoCues = errors.New("no cue index")

type ebmlReader struct {
	r   io.ReadSeeker
	br  *bufio.Reader
	pos int64
}

func newEBML(r io.ReadSeeker, pos int64) (*ebmlReader, error) {
	if _, err := r.Seek(pos, io.SeekStart); err != nil {
		return nil, err
	}
	return &ebmlReader{r: r, br: bufio.NewReaderSize(r, 64<<10), pos: pos}, nil
}

func (e *ebmlReader) readByte() (byte, error) {
	b, err := e.br.ReadByte()
	if err == nil {
		e.pos++
	}
	return b, err
}

// readID reads an element ID (1–4 bytes, length marker kept).
func (e *ebmlReader) readID() (uint32, error) {
	b, err := e.readByte()
	if err != nil {
		return 0, err
	}
	n := 0
	for m := byte(0x80); m != 0 && b&m == 0; m >>= 1 {
		n++
	}
	if n > 3 {
		return 0, fmt.Errorf("bad element id at %d", e.pos-1)
	}
	v := uint32(b)
	for i := 0; i < n; i++ {
		c, err := e.readByte()
		if err != nil {
			return 0, err
		}
		v = v<<8 | uint32(c)
	}
	return v, nil
}

// readSize reads an element data size; returns unknownSize for the
// "unknown" marker used by live-written files.
func (e *ebmlReader) readSize() (int64, error) {
	b, err := e.readByte()
	if err != nil {
		return 0, err
	}
	n := 0
	m := byte(0x80)
	for ; m != 0 && b&m == 0; m >>= 1 {
		n++
	}
	if n > 7 {
		return 0, fmt.Errorf("bad element size at %d", e.pos-1)
	}
	v := int64(b & (m - 1))
	all := v == int64(m-1)
	for i := 0; i < n; i++ {
		c, err := e.readByte()
		if err != nil {
			return 0, err
		}
		if c != 0xFF {
			all = false
		}
		v = v<<8 | int64(c)
	}
	if all {
		return unknownSize, nil
	}
	return v, nil
}

func (e *ebmlReader) readUint(size int64) (uint64, error) {
	if size > 8 {
		return 0, fmt.Errorf("uint too large: %d", size)
	}
	var v uint64
	for i := int64(0); i < size; i++ {
		c, err := e.readByte()
		if err != nil {
			return 0, err
		}
		v = v<<8 | uint64(c)
	}
	return v, nil
}

func (e *ebmlReader) readFloat(size int64) (float64, error) {
	buf := make([]byte, size)
	if _, err := io.ReadFull(e.br, buf); err != nil {
		return 0, err
	}
	e.pos += size
	switch size {
	case 4:
		return float64(math.Float32frombits(binary.BigEndian.Uint32(buf))), nil
	case 8:
		return math.Float64frombits(binary.BigEndian.Uint64(buf)), nil
	case 0:
		return 0, nil
	}
	return 0, fmt.Errorf("bad float size %d", size)
}

func (e *ebmlReader) skip(n int64) error {
	if n < 0 {
		return errors.New("cannot skip unknown size")
	}
	if n <= int64(e.br.Buffered()) {
		if _, err := e.br.Discard(int(n)); err != nil {
			return err
		}
		e.pos += n
		return nil
	}
	e.pos += n
	if _, err := e.r.Seek(e.pos, io.SeekStart); err != nil {
		return err
	}
	e.br.Reset(e.r)
	return nil
}

// ReadKeyframes reads the video keyframe index of a Matroska file.
func ReadKeyframes(path string) (*Keyframes, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readKeyframes(f)
}

func readKeyframes(f io.ReadSeeker) (*Keyframes, error) {
	e, err := newEBML(f, 0)
	if err != nil {
		return nil, err
	}
	id, err := e.readID()
	if err != nil || id != idEBML {
		return nil, errors.New("not a Matroska file")
	}
	size, err := e.readSize()
	if err != nil {
		return nil, err
	}
	if err := e.skip(size); err != nil {
		return nil, err
	}
	id, err = e.readID()
	if err != nil || id != idSegment {
		return nil, errors.New("no Segment element")
	}
	segSize, err := e.readSize()
	if err != nil {
		return nil, err
	}
	segStart := e.pos
	segEnd := int64(-1)
	if segSize != unknownSize {
		segEnd = segStart + segSize
	}

	kf := &Keyframes{}
	scale := int64(1_000_000) // nanoseconds per timestamp unit; 1 ms by default
	var durationUnits float64
	videoTrack := uint64(0)
	cuesPos := int64(-1)
	cuesRead := false

	// Walk the top-level children until we have cues (directly, or via
	// SeekHead) or hit the first cluster.
	for e.pos < segStart+maxHeaderScanSize && (segEnd < 0 || e.pos < segEnd) {
		id, err := e.readID()
		if err != nil {
			break
		}
		size, err := e.readSize()
		if err != nil {
			return nil, err
		}
		switch id {
		case idSeekHead:
			end := e.pos + size
			for e.pos < end {
				sid, err := e.readID()
				if err != nil {
					return nil, err
				}
				ssize, err := e.readSize()
				if err != nil {
					return nil, err
				}
				if sid != idSeek {
					e.skip(ssize)
					continue
				}
				send := e.pos + ssize
				var target uint64
				var position int64 = -1
				for e.pos < send {
					fid, err := e.readID()
					if err != nil {
						return nil, err
					}
					fsize, err := e.readSize()
					if err != nil {
						return nil, err
					}
					switch fid {
					case idSeekID:
						target, _ = e.readUint(fsize)
					case idSeekPosition:
						v, _ := e.readUint(fsize)
						position = int64(v)
					default:
						e.skip(fsize)
					}
				}
				if target == idCues && position >= 0 {
					cuesPos = segStart + position
				}
			}
		case idInfo:
			end := e.pos + size
			for e.pos < end {
				fid, err := e.readID()
				if err != nil {
					return nil, err
				}
				fsize, err := e.readSize()
				if err != nil {
					return nil, err
				}
				switch fid {
				case idTimestampScale:
					v, _ := e.readUint(fsize)
					if v > 0 {
						scale = int64(v)
					}
				case idDuration:
					durationUnits, _ = e.readFloat(fsize)
				default:
					e.skip(fsize)
				}
			}
		case idTracks:
			end := e.pos + size
			for e.pos < end {
				fid, err := e.readID()
				if err != nil {
					return nil, err
				}
				fsize, err := e.readSize()
				if err != nil {
					return nil, err
				}
				if fid != idTrackEntry {
					e.skip(fsize)
					continue
				}
				tend := e.pos + fsize
				var num, typ uint64
				for e.pos < tend {
					tid, err := e.readID()
					if err != nil {
						return nil, err
					}
					tsize, err := e.readSize()
					if err != nil {
						return nil, err
					}
					switch tid {
					case idTrackNumber:
						num, _ = e.readUint(tsize)
					case idTrackType:
						typ, _ = e.readUint(tsize)
					default:
						e.skip(tsize)
					}
				}
				if typ == trackTypeVideo && videoTrack == 0 {
					videoTrack = num
				}
			}
		case idCues:
			if err := readCues(e, size, videoTrack, scale, kf); err != nil {
				return nil, err
			}
			cuesRead = true
		case idCluster:
			// Media data starts here; anything we still need is behind us or
			// reachable through the SeekHead.
			goto done
		default:
			if size == unknownSize {
				goto done
			}
			if err := e.skip(size); err != nil {
				return nil, err
			}
		}
		if cuesRead && videoTrack != 0 && durationUnits > 0 {
			break
		}
	}
done:
	if !cuesRead {
		if cuesPos < 0 {
			return nil, errNoCues
		}
		e, err = newEBML(f, cuesPos)
		if err != nil {
			return nil, err
		}
		id, err := e.readID()
		if err != nil || id != idCues {
			return nil, errNoCues
		}
		size, err := e.readSize()
		if err != nil {
			return nil, err
		}
		if err := readCues(e, size, videoTrack, scale, kf); err != nil {
			return nil, err
		}
	}
	if len(kf.TimesMs) == 0 {
		return nil, errNoCues
	}
	sort.Slice(kf.TimesMs, func(i, j int) bool { return kf.TimesMs[i] < kf.TimesMs[j] })
	// Drop duplicates (several cue points can share a time).
	out := kf.TimesMs[:1]
	for _, t := range kf.TimesMs[1:] {
		if t != out[len(out)-1] {
			out = append(out, t)
		}
	}
	kf.TimesMs = out
	kf.DurationMs = int64(durationUnits * float64(scale) / 1e6)
	if kf.DurationMs <= kf.TimesMs[len(kf.TimesMs)-1] {
		return nil, errors.New("duration unknown or shorter than the cue index")
	}
	return kf, nil
}

func readCues(e *ebmlReader, size int64, videoTrack uint64, scale int64, kf *Keyframes) error {
	end := e.pos + size
	for e.pos < end {
		id, err := e.readID()
		if err != nil {
			return err
		}
		csize, err := e.readSize()
		if err != nil {
			return err
		}
		if id != idCuePoint {
			if err := e.skip(csize); err != nil {
				return err
			}
			continue
		}
		cend := e.pos + csize
		var t uint64
		forVideo := videoTrack == 0 // no track info: accept every cue
		for e.pos < cend {
			fid, err := e.readID()
			if err != nil {
				return err
			}
			fsize, err := e.readSize()
			if err != nil {
				return err
			}
			switch fid {
			case idCueTime:
				t, _ = e.readUint(fsize)
			case idCueTrackPos:
				pend := e.pos + fsize
				for e.pos < pend {
					pid, err := e.readID()
					if err != nil {
						return err
					}
					psize, err := e.readSize()
					if err != nil {
						return err
					}
					if pid == idCueTrack {
						n, _ := e.readUint(psize)
						if n == videoTrack {
							forVideo = true
						}
					} else {
						e.skip(psize)
					}
				}
			default:
				e.skip(fsize)
			}
		}
		if forVideo {
			kf.TimesMs = append(kf.TimesMs, int64(t)*scale/1_000_000)
		}
	}
	return nil
}

// Segments splits the file into HLS segments the way ffmpeg's hls muxer
// does when copying video with -hls_time targetSec: a new segment starts at
// the first keyframe at least targetSec after the current segment began.
// Returned values are start times in ms; the last segment ends at duration.
func (k *Keyframes) Segments(targetSec int) []int64 {
	target := int64(targetSec) * 1000
	starts := []int64{k.TimesMs[0]}
	cur := k.TimesMs[0]
	for _, t := range k.TimesMs[1:] {
		if t-cur >= target {
			starts = append(starts, t)
			cur = t
		}
	}
	return starts
}
