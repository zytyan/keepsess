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
	return 0
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
