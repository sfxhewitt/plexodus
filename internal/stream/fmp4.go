package stream

import (
	"encoding/binary"
	"errors"
	"os"
)

// Incremental reader for a fragmented MP4 that ffmpeg is still writing. The
// file is ftyp + moov (the init segment) followed by moof+mdat pairs, one per
// video keyframe. Each pair is a fragment; we record where it sits in the
// file and the presentation time of its first video sample, which is what
// ties it back to the keyframe index of the source file.

type frag struct {
	off, size int64 // byte range in the run file
	ptsMs     int64 // first video sample, in source-file time
}

type fmp4Scan struct {
	pos       int64 // next unread byte
	initSize  int64 // ftyp+moov length, 0 until the moov has been seen
	timescale int64 // video track timescale from mdhd
	videoID   uint32
	offsetMs  int64 // output_ts_offset we gave ffmpeg, removed from times
	frags     []frag
	err       error
}

// scan reads whatever complete boxes have been appended since the last call.
func (sc *fmp4Scan) scan(f *os.File) {
	if sc.err != nil {
		return
	}
	st, err := f.Stat()
	if err != nil {
		return
	}
	size := st.Size()
	for {
		hdr := make([]byte, 16)
		if sc.pos+8 > size {
			return
		}
		if _, err := f.ReadAt(hdr[:8], sc.pos); err != nil {
			return
		}
		bsize := int64(binary.BigEndian.Uint32(hdr[:4]))
		typ := string(hdr[4:8])
		hlen := int64(8)
		if bsize == 1 {
			if sc.pos+16 > size {
				return
			}
			if _, err := f.ReadAt(hdr[8:16], sc.pos+8); err != nil {
				return
			}
			bsize = int64(binary.BigEndian.Uint64(hdr[8:16]))
			hlen = 16
		}
		if bsize < hlen {
			sc.err = errors.New("bad box size")
			return
		}
		if sc.pos+bsize > size {
			return // box not fully written yet
		}
		switch typ {
		case "moov":
			b := make([]byte, bsize)
			if _, err := f.ReadAt(b, sc.pos); err != nil {
				return
			}
			sc.parseMoov(b[hlen:])
			sc.initSize = sc.pos + bsize
		case "moof":
			// A fragment is moof followed by its mdat; wait for both.
			mpos := sc.pos + bsize
			if mpos+8 > size {
				return
			}
			if _, err := f.ReadAt(hdr[:8], mpos); err != nil {
				return
			}
			msize := int64(binary.BigEndian.Uint32(hdr[:4]))
			mh := int64(8)
			if msize == 1 {
				if mpos+16 > size {
					return
				}
				f.ReadAt(hdr[8:16], mpos+8)
				msize = int64(binary.BigEndian.Uint64(hdr[8:16]))
				mh = 16
			}
			if string(hdr[4:8]) != "mdat" || msize < mh {
				sc.err = errors.New("moof not followed by mdat")
				return
			}
			if mpos+msize > size {
				return
			}
			b := make([]byte, bsize)
			if _, err := f.ReadAt(b, sc.pos); err != nil {
				return
			}
			pts, ok := sc.parseMoof(b[hlen:])
			if ok {
				sc.frags = append(sc.frags, frag{off: sc.pos, size: bsize + msize, ptsMs: pts})
			}
			bsize += msize
		}
		sc.pos += bsize
	}
}

// boxes calls fn for each child box in b.
func boxes(b []byte, fn func(typ string, body []byte)) {
	for len(b) >= 8 {
		size := int(binary.BigEndian.Uint32(b[:4]))
		typ := string(b[4:8])
		h := 8
		if size == 1 && len(b) >= 16 {
			size = int(binary.BigEndian.Uint64(b[8:16]))
			h = 16
		}
		if size < h || size > len(b) {
			return
		}
		fn(typ, b[h:size])
		b = b[size:]
	}
}

// parseMoov finds the first video track's ID and timescale.
func (sc *fmp4Scan) parseMoov(b []byte) {
	boxes(b, func(typ string, body []byte) {
		if typ != "trak" || sc.videoID != 0 {
			return
		}
		var id uint32
		var ts int64
		var isVideo bool
		boxes(body, func(t string, bb []byte) {
			switch t {
			case "tkhd":
				if len(bb) >= 4 {
					if bb[0] == 1 && len(bb) >= 24 {
						id = binary.BigEndian.Uint32(bb[20:24])
					} else if len(bb) >= 16 {
						id = binary.BigEndian.Uint32(bb[12:16])
					}
				}
			case "mdia":
				boxes(bb, func(t2 string, b2 []byte) {
					switch t2 {
					case "mdhd":
						if len(b2) >= 4 {
							if b2[0] == 1 && len(b2) >= 24 {
								ts = int64(binary.BigEndian.Uint32(b2[20:24]))
							} else if len(b2) >= 16 {
								ts = int64(binary.BigEndian.Uint32(b2[12:16]))
							}
						}
					case "hdlr":
						if len(b2) >= 12 && string(b2[8:12]) == "vide" {
							isVideo = true
						}
					}
				})
			}
		})
		if isVideo && ts > 0 {
			sc.videoID, sc.timescale = id, ts
		}
	})
}

// parseMoof returns the presentation time (ms, source-file time) of the
// first video sample in the fragment.
func (sc *fmp4Scan) parseMoof(b []byte) (int64, bool) {
	var found bool
	var pts int64
	boxes(b, func(typ string, body []byte) {
		if typ != "traf" || found {
			return
		}
		var id uint32
		var dts int64
		var cts int64
		var haveDTS bool
		boxes(body, func(t string, bb []byte) {
			switch t {
			case "tfhd":
				if len(bb) >= 8 {
					id = binary.BigEndian.Uint32(bb[4:8])
				}
			case "tfdt":
				if len(bb) >= 8 {
					if bb[0] == 1 && len(bb) >= 12 {
						dts = int64(binary.BigEndian.Uint64(bb[4:12]))
					} else {
						dts = int64(binary.BigEndian.Uint32(bb[4:8]))
					}
					haveDTS = true
				}
			case "trun":
				if len(bb) < 8 {
					return
				}
				flags := binary.BigEndian.Uint32(bb[:4]) & 0xffffff
				count := binary.BigEndian.Uint32(bb[4:8])
				p := 8
				if flags&0x1 != 0 {
					p += 4
				}
				if flags&0x4 != 0 {
					p += 4
				}
				if count == 0 || flags&0x800 == 0 {
					return
				}
				if flags&0x100 != 0 {
					p += 4
				}
				if flags&0x200 != 0 {
					p += 4
				}
				if flags&0x400 != 0 {
					p += 4
				}
				if len(bb) >= p+4 {
					cts = int64(int32(binary.BigEndian.Uint32(bb[p : p+4])))
				}
			}
		})
		if id == sc.videoID && haveDTS {
			found = true
			pts = dts + cts
		}
	})
	if !found || sc.timescale <= 0 {
		return 0, false
	}
	return pts*1000/sc.timescale - sc.offsetMs, true
}
