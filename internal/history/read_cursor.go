package history

import "os"

const readCursorLimit = 128

type readCursorKey struct {
	instance, segment string
	after             uint64
}
type readCursor struct {
	offset int64
	file   os.FileInfo
}

// Keep only bounded file positions, never history contents or open descriptors.
// Sequential pages can resume without decoding the start of a segment again.
func (s *Store) readPosition(key readCursorKey, file os.FileInfo) int64 {
	s.readCursorMu.Lock()
	defer s.readCursorMu.Unlock()
	cursor, ok := s.readCursors[key]
	if ok && cursor.offset <= file.Size() && os.SameFile(cursor.file, file) {
		return cursor.offset
	}
	// A new observation cursor may lie between earlier page boundaries. Reuse
	// the nearest lower position in the same append-only file, then let Read
	// filter the remaining sequence numbers instead of decoding the segment again.
	var best uint64
	var offset int64
	for candidate, cursor := range s.readCursors {
		if candidate.instance != key.instance || candidate.segment != key.segment || candidate.after > key.after || candidate.after < best {
			continue
		}
		if cursor.offset <= file.Size() && os.SameFile(cursor.file, file) {
			best = candidate.after
			offset = cursor.offset
		}
	}
	return offset
}
func (s *Store) rememberReadPosition(key readCursorKey, offset int64, file os.FileInfo) {
	s.readCursorMu.Lock()
	defer s.readCursorMu.Unlock()
	if s.readCursors == nil {
		s.readCursors = make(map[readCursorKey]readCursor)
	}
	if _, ok := s.readCursors[key]; !ok {
		if len(s.readCursorKeys) == readCursorLimit {
			delete(s.readCursors, s.readCursorKeys[s.readCursorNext])
			s.readCursorKeys[s.readCursorNext] = key
			s.readCursorNext = (s.readCursorNext + 1) % readCursorLimit
		} else {
			s.readCursorKeys = append(s.readCursorKeys, key)
		}
	}
	s.readCursors[key] = readCursor{offset: offset, file: file}
}
